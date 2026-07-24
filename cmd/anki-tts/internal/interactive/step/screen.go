package step

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// ErrBack reports that the user requested the previous workflow step.
var ErrBack = errors.New("back")

// Display describes how the host presents a screen.
type Display struct {
	Step    int
	Context string
	Resume  bool
}

// Screen is the model contract required by the interactive host.
type Screen interface {
	tea.Model
	SetSize(int, int)
	Filtering() bool
}

// Client presents screens on behalf of sequential workflow steps.
type Client interface {
	Prompt(context.Context, Screen, Display) (any, error)
}

// Retryable is implemented by screens that can restart failed work.
type Retryable interface{ Retry() tea.Cmd }

// BackDisabled is implemented by screens that cannot be left with Esc.
type BackDisabled interface{ BackDisabled() bool }

// CompletedMsg reports a screen's accepted result to the host.
type CompletedMsg struct{ Value any }

// FailedMsg asks the host to display its shared error overlay.
type FailedMsg struct {
	Err   error
	Retry tea.Cmd
}

func complete(value any) tea.Cmd {
	return func() tea.Msg { return CompletedMsg{Value: value} }
}

func fail(err error, retry tea.Cmd) tea.Cmd {
	return func() tea.Msg { return FailedMsg{Err: err, Retry: retry} }
}

func prompt[T any](ctx context.Context, client Client, screen Screen, display Display) (T, error) {
	var zero T
	value, err := client.Prompt(ctx, screen, display)
	if err != nil {
		return zero, err
	}
	result, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("screen returned %T, expected %T", value, zero)
	}
	return result, nil
}
