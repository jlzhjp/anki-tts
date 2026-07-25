package ankitts

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

	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/pipeline"
)

const maxFinalAudioSize = 32 << 20 // 32 MiB

const persistenceCommitTimeout = 30 * time.Second

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
	Stage  string
	Result GenerateResult
	Err    error
}

// BatchResult contains exactly one item per planned note in plan order.
type BatchResult struct{ Items []ItemResult }

// PartialPersistenceError reports media stored before a note update failed.
type PartialPersistenceError struct {
	Filename string
	Err      error
}

func (e *PartialPersistenceError) Error() string {
	return fmt.Sprintf("media %q was stored but the note update failed: %v", e.Filename, e.Err)
}

func (e *PartialPersistenceError) Unwrap() error { return e.Err }

// StageError attaches note and dynamic component context to a failure.
type StageError struct {
	NoteID int64
	Stage  string
	Err    error
}

func (e *StageError) Error() string {
	return fmt.Sprintf("note %d %s: %v", e.NoteID, e.Stage, e.Err)
}

func (e *StageError) Unwrap() error { return e.Err }

type synthesizedAudio struct {
	data      []byte
	format    string
	mediaType string
	cost      *float64
	costErr   error
}

type generationItem struct {
	job   preparedJob
	audio synthesizedAudio
}

type storedItem struct {
	item     generationItem
	filename string
}

// Execute runs a prepared plan through its dynamically assembled component stages.
func (a *Application) Execute(ctx context.Context, plan Plan, options ExecuteOptions) (BatchResult, error) {
	result := BatchResult{Items: make([]ItemResult, len(plan.jobs))}
	for index, job := range plan.jobs {
		result.Items[index] = ItemResult{Index: index, NoteID: job.noteID}
	}
	if len(plan.jobs) == 0 {
		return result, nil
	}

	serviceConfig := a.config[plan.serviceName]
	synthesizeWithRetry, err := pipeline.Retry(serviceConfig.Retry, "synthesize",
		func(ctx context.Context, job preparedJob) (generationItem, error) {
			audio, err := synthesize(ctx, job)
			return generationItem{job: job, audio: audio}, err
		})
	if err != nil {
		return result, err
	}
	jobs := pipeline.FromSlice(plan.jobs)
	generated, err := pipeline.MapConcurrent(jobs, plan.serviceName, serviceConfig.Concurrency,
		func(ctx context.Context, job preparedJob) (generationItem, error) {
			return synthesizeWithRetry(withProgress(ctx, options.Progress, ProgressEvent{
				Index: job.index, NoteID: job.noteID, Stage: plan.serviceName,
			}), job)
		})
	if err != nil {
		return result, err
	}
	for _, configured := range a.processors {
		processor := configured
		processorConfig := a.config[processor.Name]
		processWithRetry, retryErr := pipeline.Retry(processorConfig.Retry, "transform",
			func(ctx context.Context, item generationItem) (generationItem, error) {
				audio, err := processAudio(ctx, processor.Transformer, item.audio)
				return generationItem{job: item.job, audio: audio}, err
			})
		if retryErr != nil {
			return result, retryErr
		}
		generated, err = pipeline.MapConcurrent(generated, processor.Name, processorConfig.Concurrency,
			func(ctx context.Context, item generationItem) (generationItem, error) {
				return processWithRetry(withProgress(ctx, options.Progress, ProgressEvent{
					Index: item.job.index, NoteID: item.job.noteID, Stage: processor.Name,
				}), item)
			})
		if err != nil {
			return result, err
		}
	}
	persistenceConfig := a.config[persistenceStage]
	storeWithRetry, err := pipeline.Retry(persistenceConfig.Retry, "store media",
		func(ctx context.Context, item generationItem) (storedItem, error) {
			ReportProgress(ctx, "Storing media in Anki")
			filename := audioFilename(item)
			storedFilename, err := a.anki.StoreMediaFile(ctx, filename, item.audio.data)
			return storedItem{item: item, filename: storedFilename}, err
		})
	if err != nil {
		return result, err
	}
	updateWithRetry, err := pipeline.Retry(persistenceConfig.Retry, "update note",
		func(ctx context.Context, stored storedItem) (GenerateResult, error) {
			ReportProgress(ctx, "Updating note in Anki")
			tag := "[sound:" + stored.filename + "]"
			err := a.anki.UpdateNote(ctx, anki.NoteUpdate{
				ID:     stored.item.job.noteID,
				Fields: map[string]string{stored.item.job.destinationField: tag},
			})
			if err != nil {
				return GenerateResult{}, err
			}
			return GenerateResult{
				Filename: stored.filename,
				Cost:     stored.item.audio.cost, CostErr: stored.item.audio.costErr,
			}, nil
		})
	if err != nil {
		return result, err
	}
	persist := func(ctx context.Context, item generationItem) (GenerateResult, error) {
		stored, err := storeWithRetry(ctx, item)
		if err != nil {
			return GenerateResult{}, err
		}
		// Once media is stored, give the note update a bounded opportunity to
		// complete even if the parent batch is canceled.
		commitCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			persistenceCommitTimeout,
		)
		defer cancel()
		generated, err := updateWithRetry(commitCtx, stored)
		if err != nil {
			return GenerateResult{}, &PartialPersistenceError{Filename: stored.filename, Err: err}
		}
		reportProgress(commitCtx, ProgressItemCompleted, "")
		return generated, nil
	}
	persisted, err := pipeline.MapConcurrent(generated, persistenceStage, persistenceConfig.Concurrency,
		func(ctx context.Context, item generationItem) (GenerateResult, error) {
			return persist(withProgress(ctx, options.Progress, ProgressEvent{
				Index: item.job.index, NoteID: item.job.noteID, Stage: persistenceStage,
			}), item)
		})
	if err != nil {
		return result, err
	}
	observer := pipeline.ObserverFunc(func(event pipeline.Event) {
		if options.Progress == nil {
			return
		}
		job := plan.jobs[event.Index]
		options.Progress.Report(ProgressEvent{
			Kind: progressKind(event.Kind), Index: event.Index, NoteID: job.noteID,
			Stage: event.Stage, Attempt: event.Attempt,
			MaxAttempts: event.MaxAttempts, RetryAt: event.RetryAt, Err: event.Err,
		})
	})
	outcomes, executionErr := pipeline.Collect(ctx, persisted, observer)
	completed := make([]bool, len(plan.jobs))
	for _, outcome := range outcomes {
		job := plan.jobs[outcome.Index]
		entry := ItemResult{Index: outcome.Index, NoteID: job.noteID, Stage: outcome.Stage, Result: outcome.Value}
		if outcome.Err != nil {
			entry.Err = &StageError{NoteID: job.noteID, Stage: outcome.Stage, Err: outcome.Err}
		}
		result.Items[outcome.Index] = entry
		completed[outcome.Index] = true
	}
	if executionErr != nil {
		for index, done := range completed {
			if !done {
				result.Items[index].Err = executionErr
			}
		}
	}
	return result, executionErr
}

func synthesize(ctx context.Context, job preparedJob) (synthesizedAudio, error) {
	voice, err := job.service.Generate(ctx, Input{Text: job.text})
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

func processAudio(ctx context.Context, transformer Transformer, source synthesizedAudio) (synthesizedAudio, error) {
	voice := Voice(&bufferedVoice{Reader: bytes.NewReader(source.data), format: source.format, mediaType: source.mediaType, cost: source.cost, costErr: source.costErr})
	transformed, err := transformer.Transform(ctx, voice)
	if err != nil {
		return synthesizedAudio{}, err
	}
	if transformed == nil {
		_ = voice.Close()
		return synthesizedAudio{}, errors.New("audio processor returned no voice")
	}
	data, format, mediaType, err := readVoice(ctx, transformed)
	if err != nil {
		return synthesizedAudio{}, err
	}
	return synthesizedAudio{data: data, format: format, mediaType: mediaType, cost: source.cost, costErr: source.costErr}, nil
}

func audioFilename(item generationItem) string {
	hash := sha256.Sum256(item.audio.data)
	return fmt.Sprintf("anki-tts-%d-%x.%s", item.job.noteID, hash[:6], item.audio.format)
}

func readVoice(ctx context.Context, voice Voice) ([]byte, string, string, error) {
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
