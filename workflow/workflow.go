// Package workflow implements the application use cases shared by the TUI.
package workflow

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/textutil"
	"jlzhjp.dev/anki-tts/tts"
)

const maxFinalAudioSize = 32 << 20 // 32 MiB

const (
	DefaultSynthesisConcurrency = 4
	DefaultAudioConcurrency     = 2
)

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

// PipelineOptions controls bounded stage parallelism. OnResult is invoked
// serially as notes finish.
type PipelineOptions struct {
	SynthesisConcurrency int
	AudioConcurrency     int
	OnResult             func(ItemResult)
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

type synthesizedJob struct {
	job   preparedJob
	voice tts.Voice
}

type audioJob struct {
	job      preparedJob
	data     []byte
	filename string
	cost     *float64
	costErr  error
}

// Service coordinates browsing Anki and generating audio for notes.
type Service struct {
	anki        AnkiClient
	services    *tts.Container
	transformer tts.Transformer
}

// New creates a workflow service.
func New(client AnkiClient, services *tts.Container, transformer tts.Transformer) *Service {
	if services == nil {
		services = tts.NewContainer()
	}
	return &Service{anki: client, services: services, transformer: transformer}
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
func (s *Service) Execute(ctx context.Context, plan Plan, options PipelineOptions) (BatchResult, error) {
	if options.SynthesisConcurrency <= 0 {
		return BatchResult{}, errors.New("synthesis concurrency must be positive")
	}
	if options.AudioConcurrency <= 0 {
		return BatchResult{}, errors.New("audio concurrency must be positive")
	}
	results := BatchResult{Items: make([]ItemResult, len(plan.jobs))}
	if len(plan.jobs) == 0 {
		return results, nil
	}

	prepared := make(chan preparedJob)
	synthesized := make(chan synthesizedJob)
	audio := make(chan audioJob)
	outcomes := make(chan ItemResult)

	go feedJobs(ctx, plan.jobs, prepared, outcomes)
	go s.runSynthesisStage(ctx, prepared, synthesized, outcomes, options.SynthesisConcurrency)
	go s.runAudioStage(ctx, synthesized, audio, outcomes, options.AudioConcurrency)
	go s.runPersistenceStage(ctx, audio, outcomes)

	for outcome := range outcomes {
		results.Items[outcome.Index] = outcome
		if options.OnResult != nil {
			options.OnResult(outcome)
		}
	}
	return results, ctx.Err()
}

func feedJobs(ctx context.Context, jobs []preparedJob, output chan<- preparedJob, outcomes chan<- ItemResult) {
	defer close(output)
	for index, job := range jobs {
		select {
		case output <- job:
		case <-ctx.Done():
			for _, pending := range jobs[index:] {
				outcomes <- failedResult(pending, StageSynthesis, ctx.Err())
			}
			return
		}
	}
}

func (s *Service) runSynthesisStage(ctx context.Context, input <-chan preparedJob, output chan<- synthesizedJob, outcomes chan<- ItemResult, workers int) {
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for job := range input {
				if err := ctx.Err(); err != nil {
					outcomes <- failedResult(job, StageSynthesis, err)
					continue
				}
				voice, err := job.service.Service.Generate(ctx, tts.Input{Text: job.text})
				if err != nil {
					outcomes <- failedResult(job, StageSynthesis, err)
					continue
				}
				if voice == nil {
					outcomes <- failedResult(job, StageSynthesis, errors.New("TTS service returned no voice"))
					continue
				}
				select {
				case output <- synthesizedJob{job: job, voice: voice}:
				case <-ctx.Done():
					_ = voice.Close()
					outcomes <- failedResult(job, StageSynthesis, ctx.Err())
				}
			}
		}()
	}
	group.Wait()
	close(output)
}

func (s *Service) runAudioStage(ctx context.Context, input <-chan synthesizedJob, output chan<- audioJob, outcomes chan<- ItemResult, workers int) {
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for synthesized := range input {
				if err := ctx.Err(); err != nil {
					_ = synthesized.voice.Close()
					outcomes <- failedResult(synthesized.job, StageAudio, err)
					continue
				}
				item, err := s.materialize(ctx, synthesized)
				if err != nil {
					outcomes <- failedResult(synthesized.job, StageAudio, err)
					continue
				}
				select {
				case output <- item:
				case <-ctx.Done():
					outcomes <- failedResult(synthesized.job, StageAudio, ctx.Err())
				}
			}
		}()
	}
	group.Wait()
	close(output)
}

func (s *Service) materialize(ctx context.Context, synthesized synthesizedJob) (audioJob, error) {
	voice := synthesized.voice
	if s.transformer != nil {
		transformed, err := s.transformer.Transform(ctx, voice)
		if err != nil {
			return audioJob{}, err
		}
		if transformed == nil {
			_ = voice.Close()
			return audioJob{}, errors.New("audio pipeline returned no voice")
		}
		voice = transformed
	}

	format := safeFormat(voice.Format())
	if format == "" {
		_ = voice.Close()
		return audioJob{}, fmt.Errorf("audio pipeline returned invalid format %q", voice.Format())
	}
	var closeOnce sync.Once
	var closeErr error
	closeVoice := func() { closeOnce.Do(func() { closeErr = voice.Close() }) }
	stopCancellationClose := context.AfterFunc(ctx, closeVoice)
	data, readErr := io.ReadAll(io.LimitReader(voice, maxFinalAudioSize+1))
	stopCancellationClose()
	closeVoice()
	if readErr != nil {
		return audioJob{}, fmt.Errorf("read final audio: %w", readErr)
	}
	if closeErr != nil {
		return audioJob{}, fmt.Errorf("close final audio: %w", closeErr)
	}
	if len(data) == 0 {
		return audioJob{}, errors.New("audio pipeline returned empty data")
	}
	if len(data) > maxFinalAudioSize {
		return audioJob{}, fmt.Errorf("final audio exceeds %d bytes", maxFinalAudioSize)
	}

	var cost *float64
	costValue, costErr := voice.LoadCost(ctx)
	if costErr == nil {
		cost = &costValue
	}
	hash := sha256.Sum256(data)
	filename := fmt.Sprintf("anki-tts-%d-%x.%s", synthesized.job.note.ID, hash[:6], format)
	return audioJob{job: synthesized.job, data: data, filename: filename, cost: cost, costErr: costErr}, nil
}

func (s *Service) runPersistenceStage(ctx context.Context, input <-chan audioJob, outcomes chan<- ItemResult) {
	defer close(outcomes)
	for item := range input {
		if err := ctx.Err(); err != nil {
			outcomes <- failedResult(item.job, StagePersistence, err)
			continue
		}
		storedFilename, err := s.anki.StoreMediaFile(ctx, item.filename, item.data)
		if err != nil {
			outcomes <- failedResult(item.job, StagePersistence, err)
			continue
		}
		tag := "[sound:" + storedFilename + "]"
		err = s.anki.UpdateNote(ctx, anki.NoteUpdate{
			ID:     item.job.note.ID,
			Fields: map[string]string{item.job.destinationField: tag},
		})
		if err != nil {
			outcomes <- failedResult(item.job, StagePersistence, &PartialPersistenceError{Filename: storedFilename, Err: err})
			continue
		}
		outcomes <- ItemResult{
			Index: item.job.index, NoteID: item.job.note.ID, Stage: StagePersistence,
			Result: GenerateResult{Filename: storedFilename, Cost: item.cost, CostErr: item.costErr},
		}
	}
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
