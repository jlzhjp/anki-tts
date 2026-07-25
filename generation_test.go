package ankitts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/pipeline"
)

func TestGenerateStoresAndUpdates(t *testing.T) {
	t.Parallel()
	client := &fakeAnki{}
	provider := &fakeTTS{voice: voice("audio bytes", "mp3")}
	service := newTestApplication(t, client, provider, nil)
	result, err := executeOne(t.Context(), service, spec())
	if err != nil {
		t.Fatal(err)
	}
	if result.Cost == nil || *result.Cost != 0.00125 {
		t.Fatalf("result=%+v", result)
	}
	if provider.input.Text != "Hello world" {
		t.Fatalf("input=%q", provider.input.Text)
	}
	hash := sha256.Sum256([]byte("audio bytes"))
	wantFilename := fmt.Sprintf("anki-tts-42-%x.mp3", hash[:6])
	if client.mediaFilename != wantFilename {
		t.Fatalf("filename=%q want=%q", client.mediaFilename, wantFilename)
	}
	wantField := "[sound:" + wantFilename + "]"
	if client.update.Fields["Audio"] != wantField {
		t.Fatalf("Audio=%q want=%q", client.update.Fields["Audio"], wantField)
	}
}

func TestTransformationDeterminesUploadedMedia(t *testing.T) {
	t.Parallel()
	client := &fakeAnki{}
	provider := &fakeTTS{voice: voice("provider audio", "wav")}
	transformer := &fakeTransformer{output: "transformed audio", format: "mp3"}
	service := newTestApplication(t, client, provider, transformer)
	_, err := executeOne(t.Context(), service, spec())
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte("transformed audio"))
	wantFilename := fmt.Sprintf("anki-tts-42-%x.mp3", wantHash[:6])
	if client.mediaFilename != wantFilename || string(client.mediaData) != "transformed audio" {
		t.Fatalf("filename=%q data=%q", client.mediaFilename, client.mediaData)
	}
}

func TestProgressUsesConfiguredComponentNames(t *testing.T) {
	t.Parallel()
	client := &fakeAnki{}
	provider := &fakeTTS{
		voice:       voice("provider audio", "wav"),
		description: "Generating speech with test-model",
	}
	transformer := &fakeTransformer{
		output: "transformed audio", format: "mp3",
		description: "Converting audio to MP3 with test processor",
	}
	app := newTestApplication(t, client, provider, transformer)
	plan, err := app.Prepare(spec())
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	var descriptions []string
	var completed int
	_, err = app.Execute(t.Context(), plan, ExecuteOptions{Progress: ProgressReporterFunc(func(event ProgressEvent) {
		if event.Kind == ProgressStarted {
			stages = append(stages, event.Stage)
		}
		if event.Kind == ProgressUpdated {
			descriptions = append(descriptions, event.Description)
		}
		if event.Kind == ProgressItemCompleted {
			completed++
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"openrouter", "ffmpeg", "anki", "anki"}
	if fmt.Sprint(stages) != fmt.Sprint(want) {
		t.Fatalf("stages=%v want=%v", stages, want)
	}
	wantDescriptions := []string{
		"Generating speech with test-model",
		"Converting audio to MP3 with test processor",
		"Storing media in Anki",
		"Updating note in Anki",
	}
	if fmt.Sprint(descriptions) != fmt.Sprint(wantDescriptions) {
		t.Fatalf("descriptions=%v want=%v", descriptions, wantDescriptions)
	}
	if completed != 1 {
		t.Fatalf("item completion events=%d", completed)
	}
}

func TestMultipleAudioProcessorsRunInRegistrationOrder(t *testing.T) {
	t.Parallel()
	client := &fakeAnki{}
	provider := &fakeTTS{voice: voice("audio", "wav")}
	services := container(t, provider)
	config := testPipelineConfig(false)
	config["first"] = config["anki"]
	config["second"] = config["anki"]
	app, err := New(client, services, []AudioProcessor{
		{Name: "first", Transformer: appendTransformer{suffix: "-first"}},
		{Name: "second", Transformer: appendTransformer{suffix: "-second"}},
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeOne(t.Context(), app, spec()); err != nil {
		t.Fatal(err)
	}
	if got := string(client.mediaData); got != "audio-first-second" {
		t.Fatalf("media data=%q", got)
	}
}

func TestFailuresBeforeUploadLeaveAnkiUnchanged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		provider    *fakeTTS
		transformer Transformer
		want        string
	}{
		{name: "empty source", provider: &fakeTTS{voice: voice("audio", "mp3")}, want: "no speakable text"},
		{name: "nil voice", provider: &fakeTTS{}, want: "no voice"},
		{name: "transform failure", provider: &fakeTTS{voice: voice("audio", "wav")}, transformer: &fakeTransformer{err: errors.New("FFmpeg failed")}, want: "FFmpeg failed"},
		{name: "stream failure", provider: &fakeTTS{voice: voice("audio", "wav")}, transformer: &fakeTransformer{streamErr: errors.New("stream failed"), format: "mp3"}, want: "stream failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeAnki{}
			service := newTestApplication(t, client, test.provider, test.transformer)
			req := spec()
			if test.name == "empty source" {
				req = specWithSource("<br>")
			}
			_, err := executeOne(t.Context(), service, req)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want containing %q", err, test.want)
			}
			if client.storeCalls != 0 || client.updateCalls != 0 {
				t.Fatalf("Anki calls store=%d update=%d", client.storeCalls, client.updateCalls)
			}
		})
	}
}

func TestCostFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	client := &fakeAnki{}
	provider := &fakeTTS{voice: &fakeVoice{ReadCloser: io.NopCloser(strings.NewReader("audio")), format: "mp3", costErr: errors.New("cost unavailable")}}
	service := newTestApplication(t, client, provider, nil)
	result, err := executeOne(t.Context(), service, spec())
	if err != nil {
		t.Fatal(err)
	}
	if result.Cost != nil || result.CostErr == nil || client.updateCalls != 1 {
		t.Fatalf("result=%+v updates=%d", result, client.updateCalls)
	}
}

func TestFinalAudioValidationAndClosure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		format string
		want   string
		data   []byte
	}{
		{name: "empty", format: "mp3", want: "empty data"},
		{name: "invalid format", data: []byte("audio"), format: "../mp3", want: "invalid format"},
		{name: "oversized", data: make([]byte, maxFinalAudioSize+1), format: "mp3", want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeAnki{}
			voice := &fakeVoice{ReadCloser: io.NopCloser(bytes.NewReader(test.data)), format: test.format}
			provider := &fakeTTS{voice: voice}
			service := newTestApplication(t, client, provider, nil)
			_, err := executeOne(t.Context(), service, spec())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want containing %q", err, test.want)
			}
			if voice.closeCalls != 1 {
				t.Fatalf("close calls=%d", voice.closeCalls)
			}
			if client.storeCalls != 0 {
				t.Fatalf("store calls=%d", client.storeCalls)
			}
		})
	}
}

func TestMissingTTSServiceIsRejected(t *testing.T) {
	t.Parallel()
	service, newErr := New(&fakeAnki{}, NewServiceContainer(), nil, testPipelineConfig(false))
	if newErr != nil {
		t.Fatal(newErr)
	}
	_, err := service.Prepare(GenerationRequest{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error=%v", err)
	}
}

func TestNoteUpdateFailureReportsStoredMedia(t *testing.T) {
	t.Parallel()
	client := &fakeAnki{updateErr: errors.New("update failed")}
	provider := &fakeTTS{voice: voice("audio", "mp3")}
	service := newTestApplication(t, client, provider, nil)
	_, err := executeOne(t.Context(), service, spec())
	if err == nil || !strings.Contains(err.Error(), "was stored") || client.storeCalls != 1 {
		t.Fatalf("error=%v stores=%d", err, client.storeCalls)
	}
}

func TestAnkiUpdateRetryDoesNotRepeatStoredMedia(t *testing.T) {
	t.Parallel()
	client := &fakeAnki{updateErrs: []error{errors.New("temporary"), errors.New("temporary"), nil}}
	provider := &fakeTTS{voice: voice("audio", "mp3")}
	config := testPipelineConfig(false)
	ankiStage := config["anki"]
	ankiStage.Retry.MaxAttempts = 3
	config["anki"] = ankiStage
	app, err := New(client, container(t, provider), nil, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeOne(t.Context(), app, spec()); err != nil {
		t.Fatal(err)
	}
	if client.storeCalls != 1 || client.updateCalls != 3 {
		t.Fatalf("store calls=%d update calls=%d", client.storeCalls, client.updateCalls)
	}
}

func TestCancellationAfterUploadCompletesNoteUpdate(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	client := &fakeAnki{afterStore: cancel}
	provider := &fakeTTS{voice: voice("audio", "mp3")}
	app := newTestApplication(t, client, provider, nil)
	plan, err := app.Prepare(spec())
	if err != nil {
		t.Fatal(err)
	}

	result, err := app.Execute(ctx, plan, ExecuteOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if client.updateCalls != 1 {
		t.Fatalf("update calls=%d", client.updateCalls)
	}
	if client.updateContextErr != nil {
		t.Fatalf("update context error=%v", client.updateContextErr)
	}
	if remaining := time.Until(client.updateDeadline); remaining < 29*time.Second || remaining > persistenceCommitTimeout {
		t.Fatalf("update deadline remaining=%s", remaining)
	}
	if result.Items[0].Err != nil || result.Items[0].Result.Filename == "" {
		t.Fatalf("item=%+v", result.Items[0])
	}
}

func TestCancellationMarksNotesThatNeverEnteredPipeline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	client := &fakeAnki{}
	services := container(t, cancelingTTS{cancel: cancel})
	config := testPipelineConfig(false)
	stage := config["openrouter"]
	stage.Concurrency = 1
	config["openrouter"] = stage
	app, err := New(client, services, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	request := spec()
	request.Notes = NoteResults(
		anki.Note{ID: 1, Fields: requestFields()},
		anki.Note{ID: 2, Fields: requestFields()},
		anki.Note{ID: 3, Fields: requestFields()},
	)
	plan, err := app.Prepare(request)
	if err != nil {
		t.Fatal(err)
	}

	result, err := app.Execute(ctx, plan, ExecuteOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items=%d", len(result.Items))
	}
	for index, item := range result.Items {
		if item.Index != index || item.NoteID != int64(index+1) || !errors.Is(item.Err, context.Canceled) {
			t.Fatalf("item %d=%+v", index, item)
		}
	}
}

func container(t *testing.T, service Service) *ServiceContainer {
	t.Helper()
	services := NewServiceContainer()
	if err := services.Add("openrouter", service); err != nil {
		t.Fatal(err)
	}
	return services
}

func requestFields() map[string]anki.Field {
	return map[string]anki.Field{
		"Front": {Value: "Hello"},
		"Audio": {},
	}
}

func spec() GenerationRequest {
	return specWithSource(`<b>Hello</b>&nbsp;world`)
}

func specWithSource(source string) GenerationRequest {
	return GenerationRequest{
		Notes: NoteResults(anki.Note{ID: 42, Fields: map[string]anki.Field{
			"Front": {Value: source},
			"Audio": {Value: "existing"},
		}}),
		SourceField: "Front", DestinationField: "Audio",
		Service: "openrouter",
	}
}

func executeOne(ctx context.Context, service *Application, request GenerationRequest) (GenerateResult, error) {
	plan, err := service.Prepare(request)
	if err != nil {
		return GenerateResult{}, err
	}
	batch, err := service.Execute(ctx, plan, ExecuteOptions{})
	if err != nil {
		return GenerateResult{}, err
	}
	if len(batch.Items) != 1 {
		return GenerateResult{}, fmt.Errorf("got %d results", len(batch.Items))
	}
	return batch.Items[0].Result, batch.Items[0].Err
}

func newTestApplication(t *testing.T, client AnkiClient, service Service, transformer Transformer) *Application {
	t.Helper()
	processors := []AudioProcessor(nil)
	if transformer != nil {
		processors = []AudioProcessor{{Name: "ffmpeg", Transformer: transformer}}
	}
	app, err := New(client, container(t, service), processors, testPipelineConfig(transformer != nil))
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func testPipelineConfig(withFFmpeg bool) pipeline.Config {
	stage := func() pipeline.StageConfig {
		return pipeline.StageConfig{
			Concurrency: 2,
			Retry:       pipeline.RetryConfig{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		}
	}
	config := pipeline.Config{"openrouter": stage(), "anki": stage()}
	if withFFmpeg {
		config["ffmpeg"] = stage()
	}
	return config
}

type fakeAnki struct {
	updateDeadline   time.Time
	update           anki.NoteUpdate
	updateErr        error
	updateContextErr error
	afterStore       func()
	mediaFilename    string
	mediaData        []byte
	updateErrs       []error
	storeCalls       int
	updateCalls      int
}

func (*fakeAnki) FindNoteIDs(context.Context, string) ([]int64, error)    { return nil, nil }
func (*fakeAnki) NotesInfo(context.Context, []int64) ([]anki.Note, error) { return nil, nil }
func (f *fakeAnki) StoreMediaFile(_ context.Context, filename string, data []byte) (string, error) {
	f.storeCalls++
	f.mediaFilename = filename
	f.mediaData = slices.Clone(data)
	if f.afterStore != nil {
		f.afterStore()
	}
	return filename, nil
}

func (f *fakeAnki) UpdateNote(ctx context.Context, update anki.NoteUpdate) error {
	f.updateCalls++
	f.update = update
	f.updateContextErr = ctx.Err()
	f.updateDeadline, _ = ctx.Deadline()
	if len(f.updateErrs) > 0 {
		err := f.updateErrs[0]
		f.updateErrs = f.updateErrs[1:]
		return err
	}
	return f.updateErr
}

type fakeTTS struct {
	input       Input
	voice       Voice
	description string
}

type cancelingTTS struct {
	cancel context.CancelFunc
}

func (s cancelingTTS) Generate(ctx context.Context, _ Input) (Voice, error) {
	s.cancel()
	return nil, ctx.Err()
}

func (f *fakeTTS) Generate(ctx context.Context, input Input) (Voice, error) {
	ReportProgress(ctx, f.description)
	f.input = input
	return f.voice, nil
}

type fakeTransformer struct {
	output      string
	format      string
	err         error
	streamErr   error
	description string
}

type appendTransformer struct{ suffix string }

func (t appendTransformer) Transform(_ context.Context, input Voice) (Voice, error) {
	data, err := io.ReadAll(input)
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	return &fakeVoice{
		ReadCloser: io.NopCloser(strings.NewReader(string(data) + t.suffix)),
		format:     "mp3", source: input,
	}, nil
}

func (f *fakeTransformer) Transform(ctx context.Context, input Voice) (Voice, error) {
	ReportProgress(ctx, f.description)
	_, _ = io.ReadAll(input)
	if f.err != nil {
		_ = input.Close()
		return nil, f.err
	}
	output := io.NopCloser(bytes.NewBufferString(f.output))
	if f.streamErr != nil {
		output = io.NopCloser(errorReader{err: f.streamErr})
	}
	return &fakeVoice{ReadCloser: output, format: f.format, source: input}, nil
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func voice(data, format string) Voice {
	return &fakeVoice{ReadCloser: io.NopCloser(bytes.NewBufferString(data)), format: format, cost: 0.00125}
}

type fakeVoice struct {
	io.ReadCloser
	costErr    error
	source     Voice
	format     string
	cost       float64
	closeCalls int
}

func (v *fakeVoice) Format() string    { return v.format }
func (v *fakeVoice) MediaType() string { return "audio/" + v.format }
func (v *fakeVoice) LoadCost(ctx context.Context) (float64, error) {
	if v.source != nil {
		return v.source.LoadCost(ctx)
	}
	return v.cost, v.costErr
}

func (v *fakeVoice) Close() error {
	v.closeCalls++
	err := v.ReadCloser.Close()
	if v.source != nil {
		_ = v.source.Close()
	}
	return err
}
