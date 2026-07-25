package terminal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestScreenHostPreservesChildViewAndRemovesStepCounter(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	host := newScreenHost(
		ctx,
		cancel,
		make(chan screenRequest),
		make(chan Result),
		false,
	)
	screen := &fakeScreen{altScreen: true}
	reply := make(chan screenOutcome, 1)

	host.Update(screenRequestedMsg{request: screenRequest{
		screen: screen,
		reply:  reply,
		display: Display{
			Context: "Deck: Japanese",
		},
	}})
	host.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	view := host.View()
	if !view.AltScreen {
		t.Fatal("child alternate-screen preference was discarded")
	}
	if strings.Contains(view.Content, "Step ") {
		t.Fatalf("view still contains a step counter: %q", view.Content)
	}
	if !strings.Contains(view.Content, "Deck: Japanese\n\nscreen") {
		t.Fatalf("view is missing context or child content: %q", view.Content)
	}
	if screen.width != 100 || screen.height >= 30 {
		t.Fatalf("screen size=%dx%d", screen.width, screen.height)
	}
}

func TestScreenHostCanForceAlternateScreen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	host := newScreenHost(
		ctx,
		cancel,
		make(chan screenRequest),
		make(chan Result),
		true,
	)
	host.active = &fakeScreen{}

	if !host.View().AltScreen {
		t.Fatal("host did not force the alternate screen")
	}
}

func TestScreenHostCompletesWithoutDiscardingFinalView(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	host := newScreenHost(
		ctx,
		cancel,
		make(chan screenRequest),
		make(chan Result),
		false,
	)
	screen := &fakeScreen{}
	reply := make(chan screenOutcome, 1)
	host.active = screen
	host.reply = reply

	host.complete(screenOutcome{value: "done"})

	if outcome := <-reply; outcome.value != "done" {
		t.Fatalf("outcome=%+v", outcome)
	}
	if host.active != screen {
		t.Fatal("completed screen was discarded before its final render")
	}
}

func TestPresentedWorkflowErrorSkipsErrorOverlay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	host := newScreenHost(
		ctx,
		cancel,
		make(chan screenRequest),
		make(chan Result),
		false,
	)
	want := errors.New("batch failed")

	updated, cmd := host.Update(workflowFinishedMsg{
		result: Result{Err: want, ErrorPresented: true},
	})
	host, ok := updated.(*screenHost)
	if !ok {
		t.Fatalf("updated model=%T", updated)
	}

	if cmd == nil || host.failure != nil {
		t.Fatalf("cmd=%v failure=%v", cmd, host.failure)
	}
}

func TestScreenHostPreservesBatchExecutionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	host := newScreenHost(
		ctx,
		cancel,
		make(chan screenRequest),
		make(chan Result),
		false,
	)
	host.active = &backDisabledScreen{}
	host.activeCancelIsError = true

	host.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})

	if !host.userCanceled || !host.cancelIsError {
		t.Fatalf(
			"userCanceled=%v cancelIsError=%v",
			host.userCanceled,
			host.cancelIsError,
		)
	}
}

func TestRunTerminalReturnsPresentedWorkflowError(t *testing.T) {
	want := errors.New("batch failed")
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var output bytes.Buffer

	err := Run(
		ctx,
		strings.NewReader(""),
		&output,
		false,
		func(context.Context, Client) Result {
			return Result{Err: want, ErrorPresented: true}
		},
	)

	if !errors.Is(err, want) {
		t.Fatalf("error=%v, want %v", err, want)
	}
}

type fakeScreen struct {
	width     int
	height    int
	altScreen bool
}

func (s *fakeScreen) Init() tea.Cmd { return nil }
func (s *fakeScreen) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return s, nil
}

func (s *fakeScreen) View() tea.View {
	view := tea.NewView("screen")
	view.AltScreen = s.altScreen
	return view
}

func (s *fakeScreen) SetSize(width, height int) {
	s.width, s.height = width, height
}
func (*fakeScreen) Filtering() bool { return false }

type backDisabledScreen struct{ fakeScreen }

func (*backDisabledScreen) BackDisabled() bool { return true }
