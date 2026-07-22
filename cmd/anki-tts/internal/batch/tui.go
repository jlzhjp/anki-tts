package batch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"

	ankitts "jlzhjp.dev/anki-tts"
)

type batchPhase uint8

const (
	batchConfirm batchPhase = iota
	batchConfirmOverwrite
	batchRunning
	batchDone
)

type batchFinishedMsg struct {
	result ankitts.BatchResult
	err    error
}

type startBatchMsg struct{}
type progressTickMsg time.Time

type noteProgress struct {
	operation   ankitts.Operation
	attempt     int
	maxAttempts int
	retryAt     time.Time
	err         error
	working     bool
	done        bool
}

type batchModel struct {
	notes        []ankitts.PlannedNote
	decision     chan<- bool
	events       <-chan tea.Msg
	phase        batchPhase
	width        int
	height       int
	initialized  bool
	altScreen    bool
	offset       int
	progress     map[int]noteProgress
	result       ankitts.BatchResult
	executionErr error
}

func newBatchModel(notes []ankitts.PlannedNote, yes bool, decision chan<- bool, events <-chan tea.Msg) batchModel {
	phase := batchConfirm
	if yes {
		phase = batchRunning
	}
	return batchModel{
		notes: notes, decision: decision, events: events, phase: phase,
		width: 80, height: 24, progress: make(map[int]noteProgress),
	}
}

func (m batchModel) Init() tea.Cmd {
	if m.phase == batchRunning {
		return tea.Batch(waitBatchEvent(m.events), batchTick())
	}
	return nil
}

func (m batchModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.initialized {
			m.initialized = true
			m.altScreen = m.confirmationRows() > m.height
		}
		m.clampOffset()
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc", "n":
			if m.phase == batchConfirm || m.phase == batchConfirmOverwrite {
				select {
				case m.decision <- false:
				default:
				}
			}
			return m, tea.Quit
		case "up", "k":
			if m.altScreen && m.offset > 0 {
				m.offset--
			}
			return m, nil
		case "down", "j":
			if m.altScreen {
				m.offset++
				m.clampOffset()
			}
			return m, nil
		case "y", "enter":
			if m.phase == batchConfirm {
				if m.overwriteCount() > 0 {
					m.phase = batchConfirmOverwrite
					m.offset = 0
					return m, nil
				}
				return m.beginRunning()
			}
			if m.phase == batchConfirmOverwrite {
				return m.beginRunning()
			}
		}
	case startBatchMsg:
		select {
		case m.decision <- true:
		default:
		}
		return m, tea.Batch(waitBatchEvent(m.events), batchTick())
	case ankitts.ProgressEvent:
		state := m.progress[msg.Index]
		state.operation = msg.Operation
		state.attempt = msg.Attempt
		state.maxAttempts = msg.MaxAttempts
		state.retryAt = msg.RetryAt
		state.err = msg.Err
		state.working = msg.Kind == ankitts.ProgressStarted || msg.Kind == ankitts.ProgressRetrying
		if msg.Kind == ankitts.ProgressCompleted && msg.Operation == ankitts.OperationUpdateNote {
			state.done = true
			state.err = nil
		}
		m.progress[msg.Index] = state
		return m, waitBatchEvent(m.events)
	case progressTickMsg:
		if m.phase == batchRunning {
			return m, batchTick()
		}
	case batchFinishedMsg:
		m.phase = batchDone
		m.altScreen = false
		m.result, m.executionErr = msg.result, msg.err
		return m, tea.Quit
	}
	return m, nil
}

func (m batchModel) beginRunning() (tea.Model, tea.Cmd) {
	m.phase = batchRunning
	m.altScreen = false
	// Delay the handoff by one render so an alternate confirmation screen is
	// restored before worker output starts.
	return m, tea.Tick(20*time.Millisecond, func(time.Time) tea.Msg { return startBatchMsg{} })
}

func (m batchModel) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = m.altScreen
	if !m.initialized && (m.phase == batchConfirm || m.phase == batchConfirmOverwrite) {
		return view
	}
	switch m.phase {
	case batchConfirm:
		view.Content = m.confirmationView(false)
	case batchConfirmOverwrite:
		view.Content = m.confirmationView(true)
	case batchRunning:
		view.Content = m.progressView()
	case batchDone:
		view.Content = m.summaryView()
	}
	return view
}

func (m *batchModel) clampOffset() {
	rowCount := len(m.notes)
	if m.phase == batchConfirmOverwrite {
		rowCount = m.overwriteCount()
	}
	limit := max(0, rowCount-m.visibleRowCount())
	m.offset = min(max(m.offset, 0), limit)
}

func waitBatchEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func batchTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(now time.Time) tea.Msg { return progressTickMsg(now) })
}

type channelProgressReporter struct {
	ctx    context.Context
	events chan<- tea.Msg
}

func (r channelProgressReporter) Report(event ankitts.ProgressEvent) {
	select {
	case r.events <- event:
	case <-r.ctx.Done():
	}
}

func runBatchTUI(ctx context.Context, app Application, plan ankitts.Plan, yes bool, input io.Reader, output io.Writer) (ankitts.BatchResult, error, bool) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan tea.Msg, 256)
	decision := make(chan bool, 1)
	model := newBatchModel(plan.Items(), yes, decision, events)
	program := tea.NewProgram(model, tea.WithContext(runCtx), tea.WithInput(input), tea.WithOutput(output))
	programDone := make(chan error, 1)
	go func() {
		_, err := program.Run()
		programDone <- err
		cancel()
	}()

	if !yes {
		select {
		case accepted := <-decision:
			if !accepted {
				<-programDone
				return ankitts.BatchResult{}, nil, false
			}
		case err := <-programDone:
			if err != nil && !errors.Is(err, tea.ErrInterrupted) {
				return ankitts.BatchResult{}, fmt.Errorf("run interactive confirmation: %w", err), false
			}
			return ankitts.BatchResult{}, nil, false
		case <-ctx.Done():
			cancel()
			<-programDone
			return ankitts.BatchResult{}, ctx.Err(), false
		}
	}

	result, executionErr := app.Execute(runCtx, plan, ankitts.ExecuteOptions{
		Progress: channelProgressReporter{ctx: runCtx, events: events},
	})
	select {
	case events <- batchFinishedMsg{result: result, err: executionErr}:
	case <-runCtx.Done():
	}
	programErr := <-programDone
	if programErr != nil && !errors.Is(programErr, tea.ErrInterrupted) && !errors.Is(programErr, context.Canceled) {
		return result, fmt.Errorf("run interactive progress: %w", programErr), true
	}
	return result, executionErr, true
}
