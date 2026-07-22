package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/workflow"
)

type batchPhase uint8

const (
	batchConfirm batchPhase = iota
	batchConfirmOverwrite
	batchRunning
	batchDone
)

type batchFinishedMsg struct {
	result workflow.BatchResult
	err    error
}

type startBatchMsg struct{}
type progressTickMsg time.Time

type noteProgress struct {
	step        workflow.Step
	attempt     int
	maxAttempts int
	retryAt     time.Time
	err         error
	working     bool
	done        bool
}

type batchModel struct {
	notes        []workflow.PlannedNote
	decision     chan<- bool
	events       <-chan tea.Msg
	phase        batchPhase
	width        int
	height       int
	initialized  bool
	altScreen    bool
	offset       int
	progress     map[int]noteProgress
	result       workflow.BatchResult
	executionErr error
}

func newBatchModel(notes []workflow.PlannedNote, yes bool, decision chan<- bool, events <-chan tea.Msg) batchModel {
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
	case workflow.ProgressEvent:
		state := m.progress[msg.Index]
		state.step = msg.Step
		state.attempt = msg.Attempt
		state.maxAttempts = msg.MaxAttempts
		state.retryAt = msg.RetryAt
		state.err = msg.Err
		state.working = msg.Kind == workflow.ProgressStarted || msg.Kind == workflow.ProgressRetrying
		if msg.Kind == workflow.ProgressCompleted && msg.Step == workflow.StepUpdateNote {
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

func (m batchModel) confirmationView(overwritesOnly bool) string {
	title := fmt.Sprintf("Generate audio for %d selected note(s)?", len(m.notes))
	if overwritesOnly {
		title = red(fmt.Sprintf("Replace %d non-empty destination field(s)?", m.overwriteCount()))
	}
	rows := make([]string, 0, len(m.notes))
	for _, note := range m.notes {
		if overwritesOnly && !note.WillOverwrite {
			continue
		}
		preview := compactPreview(note.SourceText, max(12, m.width-45))
		status := "empty destination"
		if note.WillOverwrite {
			status = red("WILL OVERWRITE")
		}
		rows = append(rows, fmt.Sprintf("  %d  %-20s  %s  [%s]", note.Note.ID, truncate(note.Note.ModelName, 20), preview, status))
	}
	rows = m.visibleRows(rows)
	body := strings.Join(rows, "\n")
	help := "y/enter confirm • n/esc/q cancel"
	if m.altScreen {
		help = "↑/↓ scroll • " + help
	}
	return "Anki TTS\n\n" + title + "\n\n" + body + "\n\n" + help + "\n"
}

func (m batchModel) progressView() string {
	active := make([]int, 0, len(m.progress))
	failedNotes := make([]int, 0)
	succeeded, failed := 0, 0
	for index, state := range m.progress {
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
	pending := len(m.notes) - succeeded - failed - len(active)
	fmt.Fprintf(&builder, "Anki TTS · Processing %d notes\n\nActive %d · Pending %d · Completed %d/%d · Failed %d\n", len(m.notes), len(active), pending, succeeded, len(m.notes), failed)
	for _, index := range active {
		state := m.progress[index]
		noteID := m.notes[index].Note.ID
		fmt.Fprintf(&builder, "\n  note %d · %s", noteID, state.step)
		if !state.retryAt.IsZero() {
			remaining := max(time.Until(state.retryAt), 0)
			fmt.Fprintf(&builder, " · retry %d/%d in %s", state.attempt, state.maxAttempts, remaining.Round(100*time.Millisecond))
		}
		if state.err != nil {
			fmt.Fprintf(&builder, "\n    %s", red(state.err.Error()))
		}
	}
	if len(failedNotes) > 0 {
		builder.WriteString("\n\nErrors:")
		for _, index := range failedNotes {
			state := m.progress[index]
			fmt.Fprintf(&builder, "\n  note %d · %s: %s", m.notes[index].Note.ID, state.step, red(state.err.Error()))
		}
	}
	builder.WriteString("\n\nq/ctrl+c to cancel\n")
	return builder.String()
}

func (m batchModel) summaryView() string {
	succeeded := 0
	var failures []workflow.ItemResult
	for _, item := range m.result.Items {
		if item.Err == nil {
			succeeded++
		} else {
			failures = append(failures, item)
		}
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Anki TTS\n\nSummary: %d succeeded, %d failed.\n", succeeded, len(failures))
	for _, item := range failures {
		fmt.Fprintf(&builder, "  note %d: %s\n", item.NoteID, red(item.Err.Error()))
	}
	if m.executionErr != nil && !errors.Is(m.executionErr, context.Canceled) {
		fmt.Fprintf(&builder, "\n%s\n", red(m.executionErr.Error()))
	}
	return builder.String()
}

func (m batchModel) confirmationRows() int {
	// Header, title, blank separators, help, and trailing line.
	return len(m.notes) + 7
}

func (m batchModel) overwriteCount() int {
	count := 0
	for _, note := range m.notes {
		if note.WillOverwrite {
			count++
		}
	}
	return count
}

func (m *batchModel) clampOffset() {
	rowCount := len(m.notes)
	if m.phase == batchConfirmOverwrite {
		rowCount = m.overwriteCount()
	}
	limit := max(0, rowCount-m.visibleRowCount())
	m.offset = min(max(m.offset, 0), limit)
}

func (m batchModel) visibleRows(rows []string) []string {
	if !m.altScreen || len(rows) <= m.visibleRowCount() {
		return rows
	}
	start := min(m.offset, max(0, len(rows)-m.visibleRowCount()))
	return rows[start:min(len(rows), start+m.visibleRowCount())]
}

func (m batchModel) visibleRowCount() int { return max(1, m.height-7) }

func waitBatchEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func batchTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(now time.Time) tea.Msg { return progressTickMsg(now) })
}

func compactPreview(value string, limit int) string {
	return truncate(strings.Join(strings.Fields(value), " "), limit)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func red(value string) string { return "\x1b[1;31m" + value + "\x1b[0m" }

type channelProgressReporter struct {
	ctx    context.Context
	events chan<- tea.Msg
}

func (r channelProgressReporter) Report(event workflow.ProgressEvent) {
	select {
	case r.events <- event:
	case <-r.ctx.Done():
	}
}

func runBatchTUI(ctx context.Context, appWorkflow *workflow.Service, plan workflow.Plan, yes bool, input io.Reader, output io.Writer) (workflow.BatchResult, error, bool) {
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
				return workflow.BatchResult{}, nil, false
			}
		case err := <-programDone:
			if err != nil && !errors.Is(err, tea.ErrInterrupted) {
				return workflow.BatchResult{}, fmt.Errorf("run confirmation TUI: %w", err), false
			}
			return workflow.BatchResult{}, nil, false
		case <-ctx.Done():
			cancel()
			<-programDone
			return workflow.BatchResult{}, ctx.Err(), false
		}
	}

	result, executionErr := appWorkflow.Execute(runCtx, plan, workflow.ExecuteOptions{
		Progress: channelProgressReporter{ctx: runCtx, events: events},
	})
	select {
	case events <- batchFinishedMsg{result: result, err: executionErr}:
	case <-runCtx.Done():
	}
	programErr := <-programDone
	if programErr != nil && !errors.Is(programErr, tea.ErrInterrupted) && !errors.Is(programErr, context.Canceled) {
		return result, fmt.Errorf("run progress TUI: %w", programErr), true
	}
	return result, executionErr, true
}
