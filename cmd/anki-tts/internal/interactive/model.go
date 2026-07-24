package interactive

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/interactive/step"
)

type screenOutcome struct {
	value any
	back  bool
	err   error
}

type screenRequest struct {
	screen  step.Screen
	reply   chan screenOutcome
	display step.Display
}

type screenRequestedMsg struct{ request screenRequest }
type workflowFinishedMsg struct{ err error }

// screenClient is the workflow-owned endpoint for presenting screens.
type screenClient struct {
	requests chan<- screenRequest
}

var _ step.Client = screenClient{}

func (c screenClient) Prompt(ctx context.Context, screen step.Screen, display step.Display) (any, error) {
	reply := make(chan screenOutcome, 1)
	select {
	case c.requests <- screenRequest{screen: screen, reply: reply, display: display}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case outcome := <-reply:
		if outcome.err != nil {
			return nil, outcome.err
		}
		if outcome.back {
			return nil, step.ErrBack
		}
		return outcome.value, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// screenHost owns the active Bubble Tea model and all access to its state.
type screenHost struct {
	ctx      context.Context
	cancel   context.CancelFunc
	requests <-chan screenRequest
	done     <-chan error

	active  step.Screen
	reply   chan screenOutcome
	failure *step.ErrorScreen
	width   int
	height  int
	number  int
	context string
}

func newScreenHost(ctx context.Context, cancel context.CancelFunc, requests <-chan screenRequest, done <-chan error) *screenHost {
	return &screenHost{
		ctx:      ctx,
		cancel:   cancel,
		requests: requests,
		done:     done,
		width:    80,
		height:   24,
	}
}

func (m *screenHost) Init() tea.Cmd {
	return tea.Batch(waitForScreen(m.ctx, m.requests), waitForWorkflow(m.ctx, m.done))
}

func (m *screenHost) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case screenRequestedMsg:
		m.active = msg.request.screen
		m.reply = msg.request.reply
		m.number = msg.request.display.Step
		m.context = msg.request.display.Context
		m.failure = nil
		m.resize()
		if msg.request.display.Resume {
			return m, nil
		}
		return m, m.active.Init()
	case workflowFinishedMsg:
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			m.failure = step.NewErrorScreen(msg.err, nil)
			m.resize()
			return m, nil
		}
		return m, tea.Quit
	case step.CompletedMsg:
		return m.complete(screenOutcome{value: msg.Value})
	case step.RetryMsg:
		m.failure = nil
		var spinner tea.Cmd
		if retryable, ok := m.active.(step.Retryable); ok {
			spinner = retryable.Retry()
		}
		return m, tea.Batch(spinner, msg.Cmd)
	case step.DismissErrorMsg:
		m.failure = nil
		if m.active == nil {
			return m, tea.Quit
		}
		return m, nil
	case step.FailedMsg:
		m.failure = step.NewErrorScreen(msg.Err, msg.Retry)
		m.resize()
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.cancel()
			return m, tea.Quit
		}
		active := m.current()
		if active != nil && !active.Filtering() {
			switch msg.String() {
			case "q":
				m.cancel()
				return m, tea.Quit
			case "esc":
				disabled, _ := active.(step.BackDisabled)
				if m.failure == nil && (disabled == nil || !disabled.BackDisabled()) {
					return m.complete(screenOutcome{back: true})
				}
			}
		}
	}

	active := m.current()
	if active == nil {
		return m, nil
	}
	updated, cmd := active.Update(message)
	if m.failure != nil {
		m.failure = updated.(*step.ErrorScreen)
	} else {
		m.active = updated.(step.Screen)
	}
	return m, cmd
}

func (m *screenHost) View() tea.View {
	active := m.current()
	if active == nil {
		return tea.NewView("Anki TTS\n")
	}
	header := "Anki TTS"
	if m.failure == nil && m.number > 0 {
		header += fmt.Sprintf(" · Step %d/6", m.number)
	}
	if m.context != "" {
		header += "\n" + m.context
	}
	return tea.NewView(header + "\n\n" + active.View().Content)
}

func (m *screenHost) current() step.Screen {
	if m.failure != nil {
		return m.failure
	}
	return m.active
}

func (m *screenHost) complete(outcome screenOutcome) (tea.Model, tea.Cmd) {
	if m.reply == nil {
		return m, nil
	}
	reply := m.reply
	m.active, m.reply, m.failure = nil, nil, nil
	reply <- outcome
	return m, waitForScreen(m.ctx, m.requests)
}

func (m *screenHost) resize() {
	active := m.current()
	if active == nil {
		return
	}
	headerHeight := 3
	if m.context != "" {
		headerHeight++
	}
	active.SetSize(m.width, max(1, m.height-headerHeight))
}

func waitForScreen(ctx context.Context, requests <-chan screenRequest) tea.Cmd {
	return func() tea.Msg {
		select {
		case request := <-requests:
			return screenRequestedMsg{request: request}
		case <-ctx.Done():
			return workflowFinishedMsg{err: ctx.Err()}
		}
	}
}

func waitForWorkflow(ctx context.Context, done <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case err := <-done:
			return workflowFinishedMsg{err: err}
		case <-ctx.Done():
			return workflowFinishedMsg{err: ctx.Err()}
		}
	}
}
