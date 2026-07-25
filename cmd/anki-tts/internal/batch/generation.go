package batch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/ankitts"
)

// executionApplication is the capability required by batch generation.
type executionApplication interface {
	Execute(
		context.Context,
		ankitts.Plan,
		ankitts.ExecuteOptions,
	) (ankitts.BatchResult, error)
}

// outcome contains the complete result of a batch execution.
type outcome struct {
	err    error
	result ankitts.BatchResult
}

type finishedMsg struct {
	err    error
	result ankitts.BatchResult
}

type progressTickMsg time.Time

type noteProgress struct {
	retryAt     time.Time
	err         error
	stage       string
	description string
	attempt     int
	maxAttempts int
	working     bool
	done        bool
}

// generationScreen executes a plan and displays progress and its summary.
type generationScreen struct {
	ctx          context.Context
	app          executionApplication
	executionErr error
	events       chan ankitts.ProgressEvent
	progress     map[int]noteProgress
	plan         ankitts.Plan
	notes        []ankitts.PlannedNote
	result       ankitts.BatchResult
	finished     bool
}

// generate executes a prepared batch while presenting progress.
func generate(
	ctx context.Context,
	client client,
	app executionApplication,
	plan ankitts.Plan,
	display display,
) (outcome, error) {
	screen := &generationScreen{
		ctx:      ctx,
		app:      app,
		plan:     plan,
		notes:    plan.Items(),
		events:   make(chan ankitts.ProgressEvent, 256),
		progress: make(map[int]noteProgress),
	}
	return prompt[outcome](ctx, client, screen, display)
}

func (s *generationScreen) Init() tea.Cmd {
	return tea.Batch(s.execute(), s.waitForProgress(), progressTick())
}

func (s *generationScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case ankitts.ProgressEvent:
		state := s.progress[msg.Index]
		if msg.Kind == ankitts.ProgressStarted {
			state.description = ""
		}
		if msg.Stage != "" {
			state.stage = msg.Stage
		}
		if msg.Description != "" {
			state.description = msg.Description
		}
		state.attempt = msg.Attempt
		state.maxAttempts = msg.MaxAttempts
		state.retryAt = msg.RetryAt
		state.err = msg.Err
		state.working = msg.Kind == ankitts.ProgressStarted ||
			msg.Kind == ankitts.ProgressUpdated ||
			msg.Kind == ankitts.ProgressRetrying
		if msg.Kind == ankitts.ProgressItemCompleted {
			state.done = true
			state.working = false
			state.err = nil
		}
		s.progress[msg.Index] = state
		command := s.waitForProgress()
		return s, command

	case progressTickMsg:
		if !s.finished {
			return s, progressTick()
		}

	case finishedMsg:
		s.finished = true
		s.result = msg.result
		s.executionErr = msg.err
		return s, complete(outcome(msg))
	}
	return s, nil
}

func (s *generationScreen) View() tea.View {
	if s.finished {
		return tea.NewView(s.summaryView())
	}
	return tea.NewView(s.progressView())
}

func (*generationScreen) Filtering() bool    { return false }
func (*generationScreen) BackDisabled() bool { return true }

func (s *generationScreen) execute() tea.Cmd {
	return func() tea.Msg {
		result, err := s.app.Execute(s.ctx, s.plan, ankitts.ExecuteOptions{
			Progress: progressReporter{ctx: s.ctx, events: s.events},
		})
		return finishedMsg{result: result, err: err}
	}
}

func (s *generationScreen) waitForProgress() tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-s.events:
			return event
		case <-s.ctx.Done():
			return finishedMsg{err: s.ctx.Err()}
		}
	}
}

func progressTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(now time.Time) tea.Msg {
		return progressTickMsg(now)
	})
}

type progressReporter struct {
	ctx    context.Context
	events chan<- ankitts.ProgressEvent
}

func (r progressReporter) Report(event ankitts.ProgressEvent) {
	select {
	case r.events <- event:
	case <-r.ctx.Done():
	}
}

func (s *generationScreen) progressView() string {
	active := make([]int, 0, len(s.progress))
	failedNotes := make([]int, 0)
	succeeded, failed := 0, 0
	for index, state := range s.progress {
		switch {
		case state.done:
			succeeded++
		case state.err != nil && !state.working:
			failed++
			failedNotes = append(failedNotes, index)
		case state.working:
			active = append(active, index)
		}
	}
	slices.Sort(active)
	slices.Sort(failedNotes)

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
		description := state.description
		if description == "" {
			description = state.stage
		}
		fmt.Fprintf(
			&builder,
			"\n  note %d · %s",
			s.notes[index].NoteID,
			description,
		)
		if !state.retryAt.IsZero() {
			remaining := max(time.Until(state.retryAt), 0)
			fmt.Fprintf(
				&builder,
				" · retry %d/%d in %s",
				state.attempt+1,
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
			description := state.description
			if description == "" {
				description = state.stage
			}
			fmt.Fprintf(
				&builder,
				"\n  note %d · %s: %s",
				s.notes[index].NoteID,
				description,
				red(state.err.Error()),
			)
		}
	}
	builder.WriteString("\n\nq/ctrl+c to cancel\n")
	return builder.String()
}

func (s *generationScreen) summaryView() string {
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
