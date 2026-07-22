package workflow

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/tts"
)

func TestPipelineHonorsStageLimitsAndSerializesPersistence(t *testing.T) {
	provider := &trackingTTS{delay: 20 * time.Millisecond}
	transformer := &trackingTransformer{delay: 20 * time.Millisecond}
	client := &trackingAnki{delay: 5 * time.Millisecond}
	config := DefaultPipelineConfig()
	config.Synthesis.Concurrency = 3
	config.Audio.Concurrency = 2
	config.Persistence.Concurrency = 1
	service, err := NewWithConfig(client, nil, transformer, config)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(pipelineSpec(provider, 8))
	if err != nil {
		t.Fatal(err)
	}

	var callbackMu sync.Mutex
	callbackCount := 0
	result, err := service.Execute(context.Background(), plan, ExecuteOptions{
		Progress: ProgressReporterFunc(func(event ProgressEvent) {
			if event.Kind == ProgressCompleted && event.Step == StepUpdateNote {
				callbackMu.Lock()
				callbackCount++
				callbackMu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.maximum() != 3 {
		t.Fatalf("maximum synthesis concurrency=%d, want 3", provider.maximum())
	}
	if transformer.maximum() != 2 {
		t.Fatalf("maximum audio concurrency=%d, want 2", transformer.maximum())
	}
	if client.maximum() != 1 {
		t.Fatalf("maximum persistence concurrency=%d, want 1", client.maximum())
	}
	if callbackCount != 8 || len(result.Items) != 8 {
		t.Fatalf("callbacks=%d results=%d", callbackCount, len(result.Items))
	}
	for index, item := range result.Items {
		wantID := int64(8 - index)
		if item.NoteID != wantID || item.Err != nil {
			t.Fatalf("result[%d]=%+v, want note %d success", index, item, wantID)
		}
	}
}

func TestPipelineCancellationReturnsEveryOutcome(t *testing.T) {
	provider := &cancellingTTS{started: make(chan struct{})}
	service := New(&trackingAnki{}, nil, nil)
	plan, err := service.Plan(pipelineSpec(provider, 10))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result BatchResult
	var executeErr error
	go func() {
		result, executeErr = service.Execute(ctx, plan, ExecuteOptions{})
		close(done)
	}()
	<-provider.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not drain after cancellation")
	}
	if !errors.Is(executeErr, context.Canceled) {
		t.Fatalf("error=%v, want context cancellation", executeErr)
	}
	if len(result.Items) != 10 {
		t.Fatalf("results=%d, want 10", len(result.Items))
	}
	for _, item := range result.Items {
		if !errors.Is(item.Err, context.Canceled) {
			t.Fatalf("note %d error=%v", item.NoteID, item.Err)
		}
	}
}

func TestAudioCancellationClosesOwnedVoiceExactlyOnce(t *testing.T) {
	provider := &blockingVoiceTTS{started: make(chan struct{})}
	service := New(&trackingAnki{}, nil, nil)
	plan, err := service.Plan(pipelineSpec(provider, 1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result BatchResult
	go func() {
		result, _ = service.Execute(ctx, plan, ExecuteOptions{})
		close(done)
	}()
	<-provider.started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("audio stage did not stop after cancellation")
	}
	if provider.voice == nil {
		t.Fatal("provider did not return a voice")
	}
	if closes := provider.voice.closeCount(); closes != 1 {
		t.Fatalf("voice closes=%d, want 1", closes)
	}
	if len(result.Items) != 1 || !errors.Is(result.Items[0].Err, context.Canceled) {
		t.Fatalf("result=%+v", result)
	}
}

func TestPipelineReportsCompletionLiveButReturnsPlanOrder(t *testing.T) {
	provider := delayedTTS{}
	service := New(&trackingAnki{}, nil, nil)
	specification := GenerationSpec{
		Notes: []anki.Note{
			{ID: 1, Fields: map[string]anki.Field{"Front": {Value: "slow"}, "Audio": {}}},
			{ID: 2, Fields: map[string]anki.Field{"Front": {Value: "fast"}, "Audio": {}}},
		},
		SourceField: "Front", DestinationField: "Audio",
		Service: tts.NamedService{Name: "test", Service: provider},
	}
	plan, err := service.Plan(specification)
	if err != nil {
		t.Fatal(err)
	}
	var completionMu sync.Mutex
	var completionOrder []int64
	result, err := service.Execute(context.Background(), plan, ExecuteOptions{
		Progress: ProgressReporterFunc(func(event ProgressEvent) {
			if event.Kind == ProgressCompleted && event.Step == StepUpdateNote {
				completionMu.Lock()
				completionOrder = append(completionOrder, event.NoteID)
				completionMu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completionOrder) != 2 || completionOrder[0] != 2 {
		t.Fatalf("completion order=%v, want fast note first", completionOrder)
	}
	if result.Items[0].NoteID != 1 || result.Items[1].NoteID != 2 {
		t.Fatalf("result order=%v", []int64{result.Items[0].NoteID, result.Items[1].NoteID})
	}
}

func TestPartialPersistenceErrorIsTyped(t *testing.T) {
	client := &trackingAnki{updateErr: errors.New("update failed")}
	config := DefaultPipelineConfig()
	config.Persistence.Retry.MaxAttempts = 1
	service, configErr := NewWithConfig(client, nil, nil, config)
	if configErr != nil {
		t.Fatal(configErr)
	}
	plan, err := service.Plan(pipelineSpec(&trackingTTS{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), plan, ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var partial *PartialPersistenceError
	if !errors.As(result.Items[0].Err, &partial) || partial.Filename == "" {
		t.Fatalf("error=%v, want PartialPersistenceError", result.Items[0].Err)
	}
}

func TestPipelineRetriesEachReplayableOperation(t *testing.T) {
	t.Run("synthesis", func(t *testing.T) {
		provider := &flakyTTS{failures: 2}
		service, err := NewWithConfig(&trackingAnki{}, nil, nil, fastRetryPipeline())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := service.Plan(pipelineSpec(provider, 1))
		if err != nil {
			t.Fatal(err)
		}
		var retries int
		result, err := service.Execute(context.Background(), plan, ExecuteOptions{Progress: ProgressReporterFunc(func(event ProgressEvent) {
			if event.Kind == ProgressRetrying && event.Step == StepSynthesize {
				retries++
			}
		})})
		if err != nil || result.Items[0].Err != nil {
			t.Fatalf("result=%+v error=%v", result, err)
		}
		if provider.calls != 3 || retries != 2 {
			t.Fatalf("provider calls=%d retries=%d", provider.calls, retries)
		}
	})

	t.Run("audio replays synthesized bytes", func(t *testing.T) {
		provider := &flakyTTS{}
		transformer := &flakyTransformer{failures: 1}
		service, err := NewWithConfig(&trackingAnki{}, nil, transformer, fastRetryPipeline())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := service.Plan(pipelineSpec(provider, 1))
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Execute(context.Background(), plan, ExecuteOptions{})
		if err != nil || result.Items[0].Err != nil {
			t.Fatalf("result=%+v error=%v", result, err)
		}
		if provider.calls != 1 || transformer.calls != 2 {
			t.Fatalf("provider calls=%d transformer calls=%d", provider.calls, transformer.calls)
		}
	})

	t.Run("persistence retries operations independently", func(t *testing.T) {
		client := &flakyPersistence{storeFailures: 1, updateFailures: 1}
		service, err := NewWithConfig(client, nil, nil, fastRetryPipeline())
		if err != nil {
			t.Fatal(err)
		}
		plan, err := service.Plan(pipelineSpec(&flakyTTS{}, 1))
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.Execute(context.Background(), plan, ExecuteOptions{})
		if err != nil || result.Items[0].Err != nil {
			t.Fatalf("result=%+v error=%v", result, err)
		}
		if client.storeCalls != 2 || client.updateCalls != 2 {
			t.Fatalf("store calls=%d update calls=%d", client.storeCalls, client.updateCalls)
		}
	})
}

func TestRetryBackoffStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	retrying := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := retry(ctx, RetryConfig{MaxAttempts: 3, InitialBackoff: time.Hour, MaxBackoff: time.Hour},
			func(int, time.Time, error) { close(retrying) },
			func() (struct{}, error) { return struct{}{}, errors.New("temporary") })
		done <- err
	}()
	<-retrying
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry backoff ignored cancellation")
	}
}

func fastRetryPipeline() PipelineConfig {
	config := DefaultPipelineConfig()
	for _, stage := range []*StageConfig{&config.Synthesis, &config.Audio, &config.Persistence} {
		stage.Concurrency = 1
		stage.Retry = RetryConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	}
	return config
}

func pipelineSpec(service tts.Service, count int) GenerationSpec {
	notes := make([]anki.Note, count)
	for index := range notes {
		notes[index] = anki.Note{ID: int64(count - index), Fields: map[string]anki.Field{
			"Front": {Value: "note"},
			"Audio": {},
		}}
	}
	return GenerationSpec{
		Notes: notes, SourceField: "Front", DestinationField: "Audio",
		Service: tts.NamedService{Name: "test", Service: service},
	}
}

type concurrencyTracker struct {
	mu     sync.Mutex
	active int
	max    int
}

func (t *concurrencyTracker) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active++
	if t.active > t.max {
		t.max = t.active
	}
}

func (t *concurrencyTracker) finish() {
	t.mu.Lock()
	t.active--
	t.mu.Unlock()
}

func (t *concurrencyTracker) maximum() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.max
}

type trackingTTS struct {
	concurrencyTracker
	delay time.Duration
}

func (s *trackingTTS) Generate(ctx context.Context, _ tts.Input) (tts.Voice, error) {
	s.start()
	defer s.finish()
	select {
	case <-time.After(s.delay):
		return &pipelineVoice{ReadCloser: io.NopCloser(strings.NewReader("audio"))}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type trackingTransformer struct {
	concurrencyTracker
	delay time.Duration
}

func (t *trackingTransformer) Transform(ctx context.Context, input tts.Voice) (tts.Voice, error) {
	t.start()
	defer t.finish()
	select {
	case <-time.After(t.delay):
		return &pipelineVoice{ReadCloser: io.NopCloser(strings.NewReader("transformed")), source: input}, nil
	case <-ctx.Done():
		_ = input.Close()
		return nil, ctx.Err()
	}
}

type pipelineVoice struct {
	io.ReadCloser
	source tts.Voice
}

func (*pipelineVoice) Format() string                            { return "mp3" }
func (*pipelineVoice) MediaType() string                         { return "audio/mpeg" }
func (*pipelineVoice) LoadCost(context.Context) (float64, error) { return 0, nil }
func (v *pipelineVoice) Close() error {
	err := v.ReadCloser.Close()
	if v.source != nil {
		_ = v.source.Close()
	}
	return err
}

type trackingAnki struct {
	concurrencyTracker
	delay     time.Duration
	updateErr error
}

func (*trackingAnki) ListDecks(context.Context) ([]string, error)                  { return nil, nil }
func (*trackingAnki) ListNoteTemplates(context.Context) ([]string, error)          { return nil, nil }
func (*trackingAnki) ListTemplateFields(context.Context, string) ([]string, error) { return nil, nil }
func (*trackingAnki) ListNotes(context.Context, string) ([]anki.Note, error)       { return nil, nil }
func (a *trackingAnki) StoreMediaFile(ctx context.Context, filename string, _ []byte) (string, error) {
	a.start()
	defer a.finish()
	select {
	case <-time.After(a.delay):
		return filename, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (a *trackingAnki) UpdateNote(ctx context.Context, _ anki.NoteUpdate) error {
	a.start()
	defer a.finish()
	select {
	case <-time.After(a.delay):
		return a.updateErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

type cancellingTTS struct {
	once    sync.Once
	started chan struct{}
}

func (s *cancellingTTS) Generate(ctx context.Context, _ tts.Input) (tts.Voice, error) {
	s.once.Do(func() { close(s.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

type delayedTTS struct{}

func (delayedTTS) Generate(ctx context.Context, input tts.Input) (tts.Voice, error) {
	if input.Text == "slow" {
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &pipelineVoice{ReadCloser: io.NopCloser(strings.NewReader("audio"))}, nil
}

type flakyTTS struct {
	failures int
	calls    int
}

func (s *flakyTTS) Generate(context.Context, tts.Input) (tts.Voice, error) {
	s.calls++
	if s.calls <= s.failures {
		return nil, errors.New("temporary synthesis failure")
	}
	return &pipelineVoice{ReadCloser: io.NopCloser(strings.NewReader("audio"))}, nil
}

type flakyTransformer struct {
	failures int
	calls    int
}

func (t *flakyTransformer) Transform(_ context.Context, input tts.Voice) (tts.Voice, error) {
	t.calls++
	data, err := io.ReadAll(input)
	_ = input.Close()
	if err != nil {
		return nil, err
	}
	if t.calls <= t.failures {
		return nil, errors.New("temporary audio failure")
	}
	return &pipelineVoice{ReadCloser: io.NopCloser(strings.NewReader(string(data)))}, nil
}

type flakyPersistence struct {
	storeFailures  int
	updateFailures int
	storeCalls     int
	updateCalls    int
}

func (*flakyPersistence) ListDecks(context.Context) ([]string, error)         { return nil, nil }
func (*flakyPersistence) ListNoteTemplates(context.Context) ([]string, error) { return nil, nil }
func (*flakyPersistence) ListTemplateFields(context.Context, string) ([]string, error) {
	return nil, nil
}
func (*flakyPersistence) ListNotes(context.Context, string) ([]anki.Note, error) { return nil, nil }
func (c *flakyPersistence) StoreMediaFile(_ context.Context, filename string, _ []byte) (string, error) {
	c.storeCalls++
	if c.storeCalls <= c.storeFailures {
		return "", errors.New("temporary store failure")
	}
	return filename, nil
}
func (c *flakyPersistence) UpdateNote(context.Context, anki.NoteUpdate) error {
	c.updateCalls++
	if c.updateCalls <= c.updateFailures {
		return errors.New("temporary update failure")
	}
	return nil
}

type blockingVoiceTTS struct {
	started chan struct{}
	voice   *blockingVoice
}

func (s *blockingVoiceTTS) Generate(ctx context.Context, _ tts.Input) (tts.Voice, error) {
	s.voice = &blockingVoice{ctx: ctx, started: s.started}
	return s.voice, nil
}

type blockingVoice struct {
	ctx       context.Context
	started   chan struct{}
	startOnce sync.Once
	mu        sync.Mutex
	closes    int
}

func (v *blockingVoice) Read([]byte) (int, error) {
	v.startOnce.Do(func() { close(v.started) })
	<-v.ctx.Done()
	return 0, v.ctx.Err()
}
func (v *blockingVoice) Close() error {
	v.mu.Lock()
	v.closes++
	v.mu.Unlock()
	return nil
}
func (*blockingVoice) Format() string                            { return "mp3" }
func (*blockingVoice) MediaType() string                         { return "audio/mpeg" }
func (*blockingVoice) LoadCost(context.Context) (float64, error) { return 0, nil }
func (v *blockingVoice) closeCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.closes
}
