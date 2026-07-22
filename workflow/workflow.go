// Package workflow implements the application use cases shared by the TUI.
package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/sync/errgroup"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/textutil"
	"jlzhjp.dev/anki-tts/tts"
)

const maxFinalAudioSize = 32 << 20 // 32 MiB

// AnkiClient contains the Anki operations used by the workflow.
type AnkiClient interface {
	ListDecks(context.Context) ([]string, error)
	ListNoteTemplates(context.Context) ([]string, error)
	ListTemplateFields(context.Context, string) ([]string, error)
	ListNotes(context.Context, string) ([]anki.Note, error)
	StoreMediaFile(context.Context, string, []byte) (string, error)
	UpdateNote(context.Context, anki.NoteUpdate) error
}

// GenerationSpec describes notes that will share generation settings.
type GenerationSpec struct {
	Notes            []anki.Note
	SourceField      string
	DestinationField string
	Service          tts.NamedService
}

// Stage identifies a generation pipeline stage.
type Stage string

const (
	StageSynthesis   Stage = "synthesis"
	StageAudio       Stage = "audio"
	StagePersistence Stage = "persistence"
)

// PlannedNote is safe presentation data produced before execution.
type PlannedNote struct {
	Index         int
	Note          anki.Note
	SourceText    string
	WillOverwrite bool
}

// Plan is a validated, prepared generation batch.
type Plan struct{ jobs []preparedJob }

// Items returns presentation copies in deterministic input order.
func (p Plan) Items() []PlannedNote {
	items := make([]PlannedNote, len(p.jobs))
	for index, job := range p.jobs {
		items[index] = PlannedNote{
			Index:         job.index,
			Note:          job.note,
			SourceText:    job.text,
			WillOverwrite: job.willOverwrite,
		}
	}
	return items
}

// ExecuteOptions supplies observers for one pipeline execution.
type ExecuteOptions struct {
	Progress ProgressReporter
}

// GenerateResult describes a successfully stored voice.
type GenerateResult struct {
	Filename string
	Cost     *float64
	CostErr  error
}

// ItemResult is the terminal outcome for one planned note.
type ItemResult struct {
	Index  int
	NoteID int64
	Stage  Stage
	Result GenerateResult
	Err    error
}

// BatchResult contains exactly one item per planned note in plan order.
type BatchResult struct{ Items []ItemResult }

// PartialPersistenceError reports media that was stored before the note update
// failed. The media is deliberately retained because it may have pre-existed.
type PartialPersistenceError struct {
	Filename string
	Err      error
}

func (e *PartialPersistenceError) Error() string {
	return fmt.Sprintf("media %q was stored but the note update failed: %v", e.Filename, e.Err)
}

func (e *PartialPersistenceError) Unwrap() error { return e.Err }

// StageError attaches note and stage context to a per-note failure.
type StageError struct {
	NoteID int64
	Stage  Stage
	Err    error
}

func (e *StageError) Error() string {
	return fmt.Sprintf("note %d %s: %v", e.NoteID, e.Stage, e.Err)
}

func (e *StageError) Unwrap() error { return e.Err }

type preparedJob struct {
	index            int
	note             anki.Note
	text             string
	destinationField string
	service          tts.NamedService
	willOverwrite    bool
}

type materializedAudio struct {
	data     []byte
	filename string
	cost     *float64
	costErr  error
}

type synthesizedAudio struct {
	data      []byte
	format    string
	mediaType string
	cost      *float64
	costErr   error
}

// Service coordinates browsing Anki and generating audio for notes.
type Service struct {
	anki        AnkiClient
	services    *tts.Container
	transformer tts.Transformer
	pipeline    PipelineConfig
}

// New creates a workflow service.
func New(client AnkiClient, services *tts.Container, transformer tts.Transformer) *Service {
	service, err := NewWithConfig(client, services, transformer, DefaultPipelineConfig())
	if err != nil {
		panic(err)
	}
	return service
}

// NewWithConfig creates a workflow service with an explicit execution policy.
func NewWithConfig(client AnkiClient, services *tts.Container, transformer tts.Transformer, pipeline PipelineConfig) (*Service, error) {
	if services == nil {
		services = tts.NewContainer()
	}
	if err := pipeline.validate(); err != nil {
		return nil, fmt.Errorf("configure generation pipeline: %w", err)
	}
	return &Service{anki: client, services: services, transformer: transformer, pipeline: pipeline}, nil
}

func (s *Service) ListDecks(ctx context.Context) ([]string, error) {
	return s.anki.ListDecks(ctx)
}

func (s *Service) ListNoteTemplates(ctx context.Context) ([]string, error) {
	return s.anki.ListNoteTemplates(ctx)
}

func (s *Service) ListTemplateFields(ctx context.Context, template string) ([]string, error) {
	return s.anki.ListTemplateFields(ctx, template)
}

// Services returns the configured TTS services in display order.
func (s *Service) Services() []tts.NamedService {
	return s.services.Services()
}

// TransformsAudio reports whether generated voices pass through a transformer.
func (s *Service) TransformsAudio() bool {
	return s.transformer != nil
}

// Plan validates a generation batch and extracts source text once, before any
// external generation work starts.
func (s *Service) Plan(spec GenerationSpec) (Plan, error) {
	if spec.Service.Service == nil {
		return Plan{}, errors.New("TTS service is not configured")
	}
	if strings.TrimSpace(spec.SourceField) == "" {
		return Plan{}, errors.New("source field is required")
	}
	if strings.TrimSpace(spec.DestinationField) == "" {
		return Plan{}, errors.New("destination field is required")
	}
	jobs := make([]preparedJob, 0, len(spec.Notes))
	var invalid []string
	for index, note := range spec.Notes {
		source, ok := note.Fields[spec.SourceField]
		if !ok {
			invalid = append(invalid, fmt.Sprintf("note %d: missing source field %q", note.ID, spec.SourceField))
			continue
		}
		destination, ok := note.Fields[spec.DestinationField]
		if !ok {
			invalid = append(invalid, fmt.Sprintf("note %d: missing destination field %q", note.ID, spec.DestinationField))
			continue
		}
		text, err := textutil.FromHTML(source.Value)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("note %d: prepare source field: %v", note.ID, err))
			continue
		}
		if strings.TrimSpace(text) == "" {
			invalid = append(invalid, fmt.Sprintf("note %d: source field %q has no speakable text", note.ID, spec.SourceField))
			continue
		}
		jobs = append(jobs, preparedJob{
			index: index, note: note, text: text,
			destinationField: spec.DestinationField,
			service:          spec.Service, willOverwrite: strings.TrimSpace(destination.Value) != "",
		})
	}
	if len(invalid) > 0 {
		return Plan{}, fmt.Errorf("selected notes cannot be processed:\n  %s", strings.Join(invalid, "\n  "))
	}
	return Plan{jobs: jobs}, nil
}

// Execute runs a prepared plan through the bounded concurrent pipeline.
func (s *Service) Execute(ctx context.Context, plan Plan, options ExecuteOptions) (BatchResult, error) {
	results := BatchResult{Items: make([]ItemResult, len(plan.jobs))}
	if len(plan.jobs) == 0 {
		return results, nil
	}

	group, pipelineCtx := errgroup.WithContext(ctx)
	prepared := make(chan pipelineItem[struct{}])
	synthesized := make(chan pipelineItem[synthesizedAudio])
	audio := make(chan pipelineItem[materializedAudio])
	persisted := make(chan pipelineItem[GenerateResult])

	group.Go(func() error { return feedJobs(pipelineCtx, plan.jobs, prepared) })
	group.Go(func() error {
		return mapConcurrent(pipelineCtx, s.pipeline.Synthesis.Concurrency, StageSynthesis, prepared, synthesized,
			func(ctx context.Context, job preparedJob, _ struct{}) (synthesizedAudio, error) {
				return executeStep(ctx, options.Progress, job, StageSynthesis, StepSynthesize, s.pipeline.Synthesis.Retry,
					func() (synthesizedAudio, error) { return s.synthesize(ctx, job) })
			}, nil)
	})
	group.Go(func() error {
		return mapConcurrent(pipelineCtx, s.pipeline.Audio.Concurrency, StageAudio, synthesized, audio,
			func(ctx context.Context, job preparedJob, source synthesizedAudio) (materializedAudio, error) {
				return executeStep(ctx, options.Progress, job, StageAudio, StepTransform, s.pipeline.Audio.Retry,
					func() (materializedAudio, error) { return s.materialize(ctx, job, source) })
			}, nil)
	})
	group.Go(func() error {
		return mapConcurrent(pipelineCtx, s.pipeline.Persistence.Concurrency, StagePersistence, audio, persisted,
			func(ctx context.Context, job preparedJob, audio materializedAudio) (GenerateResult, error) {
				return s.persist(ctx, options.Progress, job, audio)
			}, nil)
	})

	waitResult := make(chan error, 1)
	go func() { waitResult <- group.Wait() }()
	for item := range persisted {
		outcome := ItemResult{
			Index: item.job.index, NoteID: item.job.note.ID, Stage: StagePersistence,
			Result: item.value,
		}
		if item.failure != nil {
			outcome = *item.failure
		}
		results.Items[outcome.Index] = outcome
	}
	waitErr := <-waitResult
	if ctxErr := ctx.Err(); ctxErr != nil {
		return results, ctxErr
	}
	return results, waitErr
}

func feedJobs(ctx context.Context, jobs []preparedJob, output chan<- pipelineItem[struct{}]) error {
	defer close(output)
	for _, job := range jobs {
		item := pipelineItem[struct{}]{job: job}
		if err := ctx.Err(); err != nil {
			failure := failedResult(job, StageSynthesis, err)
			item.failure = &failure
		}
		output <- item
	}
	return nil
}

func (s *Service) synthesize(ctx context.Context, job preparedJob) (synthesizedAudio, error) {
	voice, err := job.service.Service.Generate(ctx, tts.Input{Text: job.text})
	if err != nil {
		if voice != nil {
			_ = voice.Close()
		}
		return synthesizedAudio{}, err
	}
	if voice == nil {
		return synthesizedAudio{}, errors.New("TTS service returned no voice")
	}
	data, format, mediaType, err := readVoice(ctx, voice)
	if err != nil {
		return synthesizedAudio{}, err
	}
	var cost *float64
	costValue, costErr := voice.LoadCost(ctx)
	if costErr == nil {
		cost = &costValue
	}
	return synthesizedAudio{data: data, format: format, mediaType: mediaType, cost: cost, costErr: costErr}, nil
}

func (s *Service) materialize(ctx context.Context, job preparedJob, source synthesizedAudio) (materializedAudio, error) {
	voice := tts.Voice(&bufferedVoice{Reader: bytes.NewReader(source.data), format: source.format, mediaType: source.mediaType, cost: source.cost, costErr: source.costErr})
	if s.transformer != nil {
		transformed, err := s.transformer.Transform(ctx, voice)
		if err != nil {
			return materializedAudio{}, err
		}
		if transformed == nil {
			_ = voice.Close()
			return materializedAudio{}, errors.New("audio pipeline returned no voice")
		}
		voice = transformed
	}
	data, format, _, err := readVoice(ctx, voice)
	if err != nil {
		return materializedAudio{}, err
	}
	hash := sha256.Sum256(data)
	filename := fmt.Sprintf("anki-tts-%d-%x.%s", job.note.ID, hash[:6], format)
	return materializedAudio{data: data, filename: filename, cost: source.cost, costErr: source.costErr}, nil
}

func readVoice(ctx context.Context, voice tts.Voice) ([]byte, string, string, error) {
	format := safeFormat(voice.Format())
	if format == "" {
		_ = voice.Close()
		return nil, "", "", fmt.Errorf("audio pipeline returned invalid format %q", voice.Format())
	}
	var closeOnce sync.Once
	var closeErr error
	closeVoice := func() { closeOnce.Do(func() { closeErr = voice.Close() }) }
	stopCancellationClose := context.AfterFunc(ctx, closeVoice)
	data, readErr := io.ReadAll(io.LimitReader(voice, maxFinalAudioSize+1))
	stopCancellationClose()
	closeVoice()
	if readErr != nil {
		return nil, "", "", fmt.Errorf("read audio: %w", readErr)
	}
	if closeErr != nil {
		return nil, "", "", fmt.Errorf("close audio: %w", closeErr)
	}
	if len(data) == 0 {
		return nil, "", "", errors.New("audio pipeline returned empty data")
	}
	if len(data) > maxFinalAudioSize {
		return nil, "", "", fmt.Errorf("audio exceeds %d bytes", maxFinalAudioSize)
	}
	return data, format, voice.MediaType(), nil
}

func (s *Service) persist(ctx context.Context, reporter ProgressReporter, job preparedJob, audio materializedAudio) (GenerateResult, error) {
	storedFilename, err := executeStep(ctx, reporter, job, StagePersistence, StepStoreMedia, s.pipeline.Persistence.Retry,
		func() (string, error) { return s.anki.StoreMediaFile(ctx, audio.filename, audio.data) })
	if err != nil {
		return GenerateResult{}, err
	}
	tag := "[sound:" + storedFilename + "]"
	_, err = executeStep(ctx, reporter, job, StagePersistence, StepUpdateNote, s.pipeline.Persistence.Retry,
		func() (struct{}, error) {
			return struct{}{}, s.anki.UpdateNote(ctx, anki.NoteUpdate{
				ID: job.note.ID, Fields: map[string]string{job.destinationField: tag},
			})
		})
	if err != nil {
		return GenerateResult{}, &PartialPersistenceError{Filename: storedFilename, Err: err}
	}
	return GenerateResult{Filename: storedFilename, Cost: audio.cost, CostErr: audio.costErr}, nil
}

func executeStep[T any](ctx context.Context, reporter ProgressReporter, job preparedJob, stage Stage, step Step, config RetryConfig, operation func() (T, error)) (T, error) {
	reportProgress(reporter, ProgressEvent{Kind: ProgressStarted, Index: job.index, NoteID: job.note.ID, Stage: stage, Step: step, Attempt: 1, MaxAttempts: config.MaxAttempts})
	value, err := retry(ctx, config, func(attempt int, retryAt time.Time, err error) {
		reportProgress(reporter, ProgressEvent{Kind: ProgressRetrying, Index: job.index, NoteID: job.note.ID, Stage: stage, Step: step, Attempt: attempt, MaxAttempts: config.MaxAttempts, RetryAt: retryAt, Err: err})
	}, operation)
	kind := ProgressCompleted
	if err != nil {
		kind = ProgressFailed
	}
	reportProgress(reporter, ProgressEvent{Kind: kind, Index: job.index, NoteID: job.note.ID, Stage: stage, Step: step, MaxAttempts: config.MaxAttempts, Err: err})
	return value, err
}

func reportProgress(reporter ProgressReporter, event ProgressEvent) {
	if reporter != nil {
		reporter.Report(event)
	}
}

type bufferedVoice struct {
	*bytes.Reader
	format    string
	mediaType string
	cost      *float64
	costErr   error
}

func (*bufferedVoice) Close() error        { return nil }
func (v *bufferedVoice) Format() string    { return v.format }
func (v *bufferedVoice) MediaType() string { return v.mediaType }
func (v *bufferedVoice) LoadCost(context.Context) (float64, error) {
	if v.costErr != nil {
		return 0, v.costErr
	}
	if v.cost == nil {
		return 0, nil
	}
	return *v.cost, nil
}

func failedResult(job preparedJob, stage Stage, err error) ItemResult {
	return ItemResult{
		Index: job.index, NoteID: job.note.ID, Stage: stage,
		Err: &StageError{NoteID: job.note.ID, Stage: stage, Err: err},
	}
}

func safeFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return ""
	}
	for _, r := range format {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ""
		}
	}
	return format
}
