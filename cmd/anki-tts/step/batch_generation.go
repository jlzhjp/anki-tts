package step

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts"
)

// BatchExecutionApplication is the capability required by batch generation.
type BatchExecutionApplication interface {
	Execute(
		context.Context,
		ankitts.Plan,
		ankitts.ExecuteOptions,
	) (ankitts.BatchResult, error)
}

// BatchOutcome contains the complete result of a batch execution.
type BatchOutcome struct {
	Result ankitts.BatchResult
	Err    error
}

type batchFinishedMsg struct {
	result ankitts.BatchResult
	err    error
}

type batchProgressTickMsg time.Time

type noteProgress struct {
	operation   ankitts.Operation
	attempt     int
	maxAttempts int
	retryAt     time.Time
	err         error
	working     bool
	done        bool
}

// BatchGenerationScreen executes a plan and displays progress and its summary.
type BatchGenerationScreen struct {
	ctx          context.Context
	app          BatchExecutionApplication
	plan         ankitts.Plan
	notes        []ankitts.PlannedNote
	events       chan ankitts.ProgressEvent
	progress     map[int]noteProgress
	result       ankitts.BatchResult
	executionErr error
	finished     bool
}

// GenerateBatch executes a prepared batch while presenting progress.
func GenerateBatch(
	ctx context.Context,
	client Client,
	app BatchExecutionApplication,
	plan ankitts.Plan,
	display Display,
) (BatchOutcome, error) {
	screen := &BatchGenerationScreen{
		ctx:      ctx,
		app:      app,
		plan:     plan,
		notes:    plan.Items(),
		events:   make(chan ankitts.ProgressEvent, 256),
		progress: make(map[int]noteProgress),
	}
	return prompt[BatchOutcome](ctx, client, screen, display)
}

func (s *BatchGenerationScreen) Init() tea.Cmd {
	return tea.Batch(s.execute(), s.waitForProgress(), batchProgressTick())
}

func (s *BatchGenerationScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case ankitts.ProgressEvent:
		state := s.progress[msg.Index]
		state.operation = msg.Operation
		state.attempt = msg.Attempt
		state.maxAttempts = msg.MaxAttempts
		state.retryAt = msg.RetryAt
		state.err = msg.Err
		state.working =
			msg.Kind == ankitts.ProgressStarted ||
				msg.Kind == ankitts.ProgressRetrying
		if msg.Kind == ankitts.ProgressCompleted &&
			msg.Operation == ankitts.OperationUpdateNote {
			state.done = true
			state.working = false
			state.err = nil
		}
		s.progress[msg.Index] = state
		return s, s.waitForProgress()

	case batchProgressTickMsg:
		if !s.finished {
			return s, batchProgressTick()
		}

	case batchFinishedMsg:
		s.finished = true
		s.result = msg.result
		s.executionErr = msg.err
		return s, complete(BatchOutcome{Result: msg.result, Err: msg.err})
	}
	return s, nil
}

func (s *BatchGenerationScreen) View() tea.View {
	if s.finished {
		return tea.NewView(s.summaryView())
	}
	return tea.NewView(s.progressView())
}

func (*BatchGenerationScreen) Filtering() bool    { return false }
func (*BatchGenerationScreen) BackDisabled() bool { return true }

func (s *BatchGenerationScreen) execute() tea.Cmd {
	return func() tea.Msg {
		result, err := s.app.Execute(s.ctx, s.plan, ankitts.ExecuteOptions{
			Progress: batchProgressReporter{ctx: s.ctx, events: s.events},
		})
		return batchFinishedMsg{result: result, err: err}
	}
}

func (s *BatchGenerationScreen) waitForProgress() tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-s.events:
			return event
		case <-s.ctx.Done():
			return batchFinishedMsg{err: s.ctx.Err()}
		}
	}
}

func batchProgressTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(now time.Time) tea.Msg {
		return batchProgressTickMsg(now)
	})
}

type batchProgressReporter struct {
	ctx    context.Context
	events chan<- ankitts.ProgressEvent
}

func (r batchProgressReporter) Report(event ankitts.ProgressEvent) {
	select {
	case r.events <- event:
	case <-r.ctx.Done():
	}
}

func (s *BatchGenerationScreen) progressView() string {
	active := make([]int, 0, len(s.progress))
	failedNotes := make([]int, 0)
	succeeded, failed := 0, 0
	for index, state := range s.progress {
		if state.done {
			succeeded++
		} else if state.err != nil && !state.working {
			failed++
			failedNotes = append(failedNotes, index)
		} else if state.working {
			active = append(active, index)
		}
	}
	sort.Ints(active)
	sort.Ints(failedNotes)

	var builder strings.Builder
	pending := len(s.notes) - succeeded - failed - len(active)
	fmt.Fprintf(
		&builder,
		"Processing %d notes\n\nActive %d · Pending %d · Completed %d/%d · Failed %d\n",
		len(s.notes),
		len(active),
		pending,
		succeeded,
		len(s.notes),
		failed,
	)
	for _, index := range active {
		state := s.progress[index]
		fmt.Fprintf(
			&builder,
			"\n  note %d · %s",
			s.notes[index].NoteID,
			state.operation,
		)
		if !state.retryAt.IsZero() {
			remaining := max(time.Until(state.retryAt), 0)
			fmt.Fprintf(
				&builder,
				" · retry %d/%d in %s",
				state.attempt,
				state.maxAttempts,
				remaining.Round(100*time.Millisecond),
			)
		}
		if state.err != nil {
			fmt.Fprintf(&builder, "\n    %s", red(state.err.Error()))
		}
	}
	if len(failedNotes) > 0 {
		builder.WriteString("\n\nErrors:")
		for _, index := range failedNotes {
			state := s.progress[index]
			fmt.Fprintf(
				&builder,
				"\n  note %d · %s: %s",
				s.notes[index].NoteID,
				state.operation,
				red(state.err.Error()),
			)
		}
	}
	builder.WriteString("\n\nq/ctrl+c to cancel\n")
	return builder.String()
}

func (s *BatchGenerationScreen) summaryView() string {
	succeeded := 0
	var failures []ankitts.ItemResult
	for _, item := range s.result.Items {
		if item.Err == nil {
			succeeded++
		} else {
			failures = append(failures, item)
		}
	}

	var builder strings.Builder
	fmt.Fprintf(
		&builder,
		"Summary: %d succeeded, %d failed.\n",
		succeeded,
		len(failures),
	)
	for _, item := range failures {
		fmt.Fprintf(
			&builder,
			"  note %d: %s\n",
			item.NoteID,
			red(item.Err.Error()),
		)
	}
	if s.executionErr != nil &&
		!errors.Is(s.executionErr, context.Canceled) {
		fmt.Fprintf(&builder, "\n%s\n", red(s.executionErr.Error()))
	}
	return builder.String()
}
