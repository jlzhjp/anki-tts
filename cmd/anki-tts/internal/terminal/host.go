package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
)

type screenOutcome struct {
	value any
	back  bool
}

type screenRequest struct {
	screen  Screen
	reply   chan screenOutcome
	display Display
}

type screenRequestedMsg struct{ request screenRequest }
type workflowFinishedMsg struct{ result Result }

// Result separates command failure from whether the active screen
// already explains that failure to the user.
type Result struct {
	Err            error
	ErrorPresented bool
}

type screenClient struct {
	requests chan<- screenRequest
}

var _ Client = screenClient{}

func (c screenClient) Prompt(ctx context.Context, screen Screen, display Display) (any, error) {
	reply := make(chan screenOutcome, 1)
	select {
	case c.requests <- screenRequest{screen: screen, reply: reply, display: display}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case outcome := <-reply:
		if outcome.back {
			return nil, ErrBack
		}
		return outcome.value, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type screenHost struct {
	ctx            context.Context
	cancel         context.CancelFunc
	requests       <-chan screenRequest
	done           <-chan Result
	forceAltScreen bool

	active              Screen
	reply               chan screenOutcome
	failure             *ErrorScreen
	width               int
	height              int
	sized               bool
	context             string
	workflowDone        bool
	userCanceled        bool
	cancelIsError       bool
	activeCancelIsError bool
}

func newScreenHost(
	ctx context.Context,
	cancel context.CancelFunc,
	requests <-chan screenRequest,
	done <-chan Result,
	forceAltScreen bool,
) *screenHost {
	return &screenHost{
		ctx:            ctx,
		cancel:         cancel,
		requests:       requests,
		done:           done,
		forceAltScreen: forceAltScreen,
		width:          80,
		height:         24,
	}
}

func (m *screenHost) Init() tea.Cmd {
	return tea.Batch(waitForScreen(m.ctx, m.requests), waitForWorkflow(m.ctx, m.done))
}

func (m *screenHost) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.sized = true
		m.resize()
		return m, nil

	case screenRequestedMsg:
		m.active = msg.request.screen
		m.reply = msg.request.reply
		m.context = msg.request.display.Context
		m.activeCancelIsError = msg.request.display.CancelIsError
		m.failure = nil
		m.resize()
		if msg.request.display.Resume {
			return m, nil
		}
		return m, m.active.Init()

	case workflowFinishedMsg:
		m.workflowDone = true
		if msg.result.Err != nil &&
			!errors.Is(msg.result.Err, context.Canceled) &&
			!msg.result.ErrorPresented {
			m.failure = NewErrorScreen(msg.result.Err, nil)
			m.resize()
			return m, nil
		}
		return m, tea.Quit

	case CompletedMsg:
		return m.complete(screenOutcome{value: msg.Value})

	case RetryMsg:
		m.failure = nil
		var spinner tea.Cmd
		if retryable, ok := m.active.(Retryable); ok {
			spinner = retryable.Retry()
		}
		return m, tea.Batch(spinner, msg.Cmd)

	case DismissErrorMsg:
		m.failure = nil
		if m.workflowDone || m.active == nil {
			return m, tea.Quit
		}
		return m, nil

	case FailedMsg:
		m.failure = NewErrorScreen(msg.Err, msg.Retry)
		m.resize()
		return m, nil

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.rememberCancellationPolicy()
			m.userCanceled = true
			m.cancel()
			return m, tea.Quit
		}
		active := m.current()
		if active != nil && !active.Filtering() {
			switch msg.String() {
			case "q":
				m.rememberCancellationPolicy()
				m.userCanceled = true
				m.cancel()
				return m, tea.Quit
			case "esc":
				disabled, _ := active.(BackDisabled)
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
		failure, ok := updated.(*ErrorScreen)
		if !ok {
			m.failure = NewErrorScreen(
				fmt.Errorf("error screen returned unexpected model %T", updated),
				nil,
			)
			m.resize()
			return m, nil
		}
		m.failure = failure
	} else {
		screen, ok := updated.(Screen)
		if !ok {
			m.failure = NewErrorScreen(
				fmt.Errorf("screen %T returned unexpected model %T", active, updated),
				nil,
			)
			m.resize()
			return m, nil
		}
		m.active = screen
	}
	return m, cmd
}

func (m *screenHost) View() tea.View {
	active := m.current()
	if active == nil {
		view := tea.NewView("Anki TTS\n")
		view.AltScreen = m.forceAltScreen
		return view
	}

	view := active.View()
	header := "Anki TTS"
	if m.context != "" {
		header += "\n" + m.context
	}
	view.Content = header + "\n\n" + view.Content
	view.AltScreen = view.AltScreen || m.forceAltScreen
	return view
}

func (m *screenHost) current() Screen {
	if m.failure != nil {
		return m.failure
	}
	return m.active
}

func (m *screenHost) rememberCancellationPolicy() {
	m.cancelIsError = m.activeCancelIsError
}

func (m *screenHost) complete(outcome screenOutcome) (tea.Model, tea.Cmd) {
	if m.reply == nil {
		return m, nil
	}
	reply := m.reply
	m.reply = nil
	m.failure = nil
	reply <- outcome
	return m, waitForScreen(m.ctx, m.requests)
}

func (m *screenHost) resize() {
	active := m.current()
	if active == nil || !m.sized {
		return
	}
	headerHeight := 3
	if m.context != "" {
		headerHeight++
	}
	if resizable, ok := active.(Resizable); ok {
		resizable.SetSize(m.width, max(1, m.height-headerHeight))
	}
}

func waitForScreen(ctx context.Context, requests <-chan screenRequest) tea.Cmd {
	return func() tea.Msg {
		select {
		case request := <-requests:
			return screenRequestedMsg{request: request}
		case <-ctx.Done():
			return workflowFinishedMsg{
				result: Result{Err: ctx.Err()},
			}
		}
	}
}

func waitForWorkflow(ctx context.Context, done <-chan Result) tea.Cmd {
	return func() tea.Msg {
		select {
		case result := <-done:
			return workflowFinishedMsg{result: result}
		case <-ctx.Done():
			return workflowFinishedMsg{
				result: Result{Err: ctx.Err()},
			}
		}
	}
}

func Run(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	forceAltScreen bool,
	workflow func(context.Context, Client) Result,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	requests := make(chan screenRequest)
	hostDone := make(chan Result, 1)
	result := make(chan Result, 1)
	host := newScreenHost(runCtx, cancel, requests, hostDone, forceAltScreen)
	client := screenClient{requests: requests}
	go func() {
		Result := workflow(runCtx, client)
		hostDone <- Result
		result <- Result
	}()

	_, programErr := tea.NewProgram(
		host,
		tea.WithContext(runCtx),
		tea.WithInput(input),
		tea.WithOutput(output),
	).Run()
	cancel()
	completed := <-result

	if programErr != nil &&
		!errors.Is(programErr, tea.ErrInterrupted) &&
		!errors.Is(programErr, context.Canceled) {
		return fmt.Errorf("run terminal UI: %w", programErr)
	}
	if host.userCanceled &&
		!host.cancelIsError &&
		errors.Is(completed.Err, context.Canceled) {
		return nil
	}
	return completed.Err
}
