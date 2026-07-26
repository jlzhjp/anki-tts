package ankitts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"

	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/internal/streamutil"
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
	CostErr  error
	cost     CostLoader
	Cost     *float64
	Filename string
}

// ItemResult is the terminal outcome for one planned note.
type ItemResult struct {
	Result GenerateResult
	Err    error
	Stage  string
	Index  int
	NoteID int64
}

// CostSummary describes the cost information loaded for persisted items.
type CostSummary struct {
	KnownTotal       float64
	KnownItems       int
	UnavailableItems int
}

// BatchResult contains exactly one item per planned note in plan order.
type BatchResult struct {
	Items []ItemResult
	Cost  CostSummary
}

// PartialPersistenceError reports media stored before a note update failed.
type PartialPersistenceError struct {
	Err      error
	Filename string
}

func (e *PartialPersistenceError) Error() string {
	return fmt.Sprintf("media %q was stored but the note update failed: %v", e.Filename, e.Err)
}

func (e *PartialPersistenceError) Unwrap() error { return e.Err }

// StageError attaches note and dynamic component context to a failure.
type StageError struct {
	Err    error
	Stage  string
	NoteID int64
}

func (e *StageError) Error() string {
	return fmt.Sprintf("note %d %s: %v", e.NoteID, e.Stage, e.Err)
}

func (e *StageError) Unwrap() error { return e.Err }

type materializedVoice struct {
	buffer    *streamutil.ReplayBuffer
	loadCost  CostLoader
	format    string
	mediaType string
}

type generationItem struct {
	audio materializedVoice
	job   preparedJob
}

type storedItem struct {
	filename string
	item     generationItem
}

type batchAccumulator struct {
	completed []bool
	result    BatchResult
}

type costTask struct {
	load      CostLoader
	itemIndex int
}

type costObservation struct {
	err  error
	cost float64
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
		},
		pipeline.WithRetryPredicate(componentRetryPredicate(plan.jobs[0].service)),
	)
	if err != nil {
		return result, err
	}
	jobs := pipeline.FromSlice(plan.jobs)
	generated, err := pipeline.MapConcurrent(jobs, plan.serviceName, serviceConfig.Concurrency,
		func(ctx context.Context, job preparedJob) (generationItem, error) {
			return synthesizeWithRetry(withProgress(ctx, options.Progress, &ProgressEvent{
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
				audio, err := processAudio(ctx, processor.Transformer, &item.audio)
				return generationItem{job: item.job, audio: audio}, err
			},
			pipeline.WithRetryPredicate(componentRetryPredicate(processor.Transformer)),
		)
		if retryErr != nil {
			return result, retryErr
		}
		generated, err = pipeline.MapConcurrent(generated, processor.Name, processorConfig.Concurrency,
			func(ctx context.Context, item generationItem) (generationItem, error) {
				return processWithRetry(withProgress(ctx, options.Progress, &ProgressEvent{
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
			filename := audioFilename(&item)
			storedFilename, err := a.anki.StoreMediaFile(ctx, filename, item.audio.buffer.Bytes())
			return storedItem{item: item, filename: storedFilename}, err
		},
		pipeline.WithRetryPredicate(componentRetryPredicate(a.anki)),
	)
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
				cost:     stored.item.audio.loadCost,
			}, nil
		},
		pipeline.WithRetryPredicate(componentRetryPredicate(a.anki)),
	)
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
			return persist(withProgress(ctx, options.Progress, &ProgressEvent{
				Index: item.job.index, NoteID: item.job.noteID, Stage: persistenceStage,
			}), item)
		})
	if err != nil {
		return result, err
	}
	observer := pipeline.ObserverFunc(func(event *pipeline.Event) {
		if options.Progress == nil {
			return
		}
		job := plan.jobs[event.Index]
		options.Progress.Report(&ProgressEvent{
			Kind: progressKind(event.Kind), Index: event.Index, NoteID: job.noteID,
			Stage: event.Stage, Attempt: event.Attempt,
			MaxAttempts: event.MaxAttempts, RetryAt: event.RetryAt, Err: event.Err,
		})
	})
	accumulator := &batchAccumulator{
		result: result, completed: make([]bool, len(plan.jobs)),
	}
	accumulator, executionErr := pipeline.Reduce(
		ctx,
		persisted,
		observer,
		accumulator,
		func(accumulator *batchAccumulator, outcome pipeline.Result[GenerateResult]) *batchAccumulator {
			job := plan.jobs[outcome.Index]
			entry := ItemResult{
				Index: outcome.Index, NoteID: job.noteID,
				Stage: outcome.Stage, Result: outcome.Value,
			}
			if outcome.Err != nil {
				entry.Err = &StageError{
					NoteID: job.noteID, Stage: outcome.Stage, Err: outcome.Err,
				}
			}
			accumulator.result.Items[outcome.Index] = entry
			accumulator.completed[outcome.Index] = true
			return accumulator
		},
	)
	if executionErr != nil {
		for index, done := range accumulator.completed {
			if !done {
				accumulator.result.Items[index].Err = executionErr
			}
		}
	}
	costErr := loadBatchCosts(
		ctx,
		&accumulator.result,
		serviceConfig.Concurrency,
	)
	if executionErr != nil {
		return accumulator.result, executionErr
	}
	return accumulator.result, costErr
}

func synthesize(ctx context.Context, job preparedJob) (materializedVoice, error) {
	voice, err := job.service.Generate(ctx, Input{Text: job.text})
	if err != nil {
		if voice != nil {
			_ = voice.Close()
		}
		return materializedVoice{}, err
	}
	if voice == nil {
		return materializedVoice{}, permanentOperationFailure(
			errors.New("TTS service returned no voice"),
		)
	}
	return materializeVoice(ctx, voice)
}

func processAudio(ctx context.Context, transformer Transformer, source *materializedVoice) (materializedVoice, error) {
	voice := source.Open()
	transformed, err := transformer.Transform(ctx, voice)
	if err != nil {
		return materializedVoice{}, err
	}
	if transformed == nil {
		_ = voice.Close()
		return materializedVoice{}, permanentOperationFailure(
			errors.New("audio processor returned no voice"),
		)
	}
	return materializeVoice(ctx, transformed)
}

func audioFilename(item *generationItem) string {
	hash := sha256.Sum256(item.audio.buffer.Bytes())
	return fmt.Sprintf("anki-tts-%d-%x.%s", item.job.noteID, hash[:6], item.audio.format)
}

func materializeVoice(ctx context.Context, voice Voice) (materializedVoice, error) {
	rawFormat := voice.Format()
	format := normalizeAudioExtension(rawFormat)
	if format == "" {
		_ = voice.Close()
		return materializedVoice{}, permanentOperationFailure(
			fmt.Errorf("audio pipeline returned invalid format %q", rawFormat),
		)
	}
	mediaType := voice.MediaType()
	loadCost := voice.CostLoader()
	closeVoice := sync.OnceValue(voice.Close)
	stopCancellationClose := context.AfterFunc(ctx, func() {
		_ = closeVoice()
	})
	buffer, readErr := streamutil.NewReplayBuffer(voice, maxFinalAudioSize)
	stopCancellationClose()
	closeErr := closeVoice()
	if readErr != nil {
		if errors.Is(readErr, streamutil.ErrLimitExceeded) {
			return materializedVoice{}, permanentOperationFailure(
				fmt.Errorf("audio exceeds %d bytes", maxFinalAudioSize),
			)
		}
		return materializedVoice{}, fmt.Errorf("read audio: %w", readErr)
	}
	if closeErr != nil {
		return materializedVoice{}, fmt.Errorf("close audio: %w", closeErr)
	}
	if len(buffer.Bytes()) == 0 {
		return materializedVoice{}, permanentOperationFailure(
			errors.New("audio pipeline returned empty data"),
		)
	}
	return materializedVoice{
		buffer: buffer, format: format, mediaType: mediaType, loadCost: loadCost,
	}, nil
}

func (v *materializedVoice) Open() Voice {
	return &materializedVoiceReader{Reader: v.buffer.Reader(), source: v}
}

type materializedVoiceReader struct {
	*bytes.Reader
	source *materializedVoice
}

func (*materializedVoiceReader) Close() error        { return nil }
func (v *materializedVoiceReader) Format() string    { return v.source.format }
func (v *materializedVoiceReader) MediaType() string { return v.source.mediaType }
func (v *materializedVoiceReader) CostLoader() CostLoader {
	return v.source.loadCost
}

func loadBatchCosts(ctx context.Context, result *BatchResult, concurrency int) error {
	tasks := make([]costTask, 0, len(result.Items))
	for index := range result.Items {
		item := &result.Items[index]
		if item.Err == nil && item.Result.Filename != "" {
			tasks = append(tasks, costTask{itemIndex: index, load: item.Result.cost})
			item.Result.cost = nil
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		for _, task := range tasks {
			result.Items[task.itemIndex].Result.CostErr = err
		}
		summarizeCosts(result)
		return err
	}

	costs, err := pipeline.MapConcurrent(
		pipeline.FromSlice(tasks),
		"cost",
		concurrency,
		func(ctx context.Context, task costTask) (costObservation, error) {
			if task.load == nil {
				return costObservation{err: ErrCostUnavailable}, nil
			}
			cost, loadErr := task.load(ctx)
			if loadErr == nil && (cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0)) {
				loadErr = fmt.Errorf("invalid voice cost %v", cost)
			}
			return costObservation{cost: cost, err: loadErr}, nil
		},
	)
	if err != nil {
		return err
	}
	completed := make([]bool, len(tasks))
	completed, executionErr := pipeline.Reduce(
		ctx,
		costs,
		nil,
		completed,
		func(completed []bool, outcome pipeline.Result[costObservation]) []bool {
			task := tasks[outcome.Index]
			item := &result.Items[task.itemIndex]
			switch {
			case outcome.Err != nil:
				item.Result.CostErr = outcome.Err
			case outcome.Value.err != nil:
				item.Result.CostErr = outcome.Value.err
			default:
				cost := outcome.Value.cost
				item.Result.Cost = &cost
			}
			completed[outcome.Index] = true
			return completed
		},
	)
	if executionErr != nil {
		for index, done := range completed {
			if !done {
				result.Items[tasks[index].itemIndex].Result.CostErr = executionErr
			}
		}
	}
	summarizeCosts(result)
	return executionErr
}

func summarizeCosts(result *BatchResult) {
	result.Cost = CostSummary{}
	for _, item := range result.Items {
		if item.Err != nil || item.Result.Filename == "" {
			continue
		}
		if item.Result.Cost != nil {
			result.Cost.KnownTotal += *item.Result.Cost
			result.Cost.KnownItems++
		} else {
			result.Cost.UnavailableItems++
		}
	}
}

// normalizeAudioExtension validates provider-supplied format metadata before
// using it in an Anki media filename. The core remains provider-neutral by
// accepting any alphanumeric extension while rejecting path separators and
// punctuation.
func normalizeAudioExtension(format string) string {
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
