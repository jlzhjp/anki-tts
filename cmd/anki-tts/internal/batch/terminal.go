package batch

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/terminal"
)

type client = terminal.Client
type display = terminal.Display
type screen = terminal.Screen
type completedMsg = terminal.CompletedMsg
type failedMsg = terminal.FailedMsg

var errBack = terminal.ErrBack

func complete(value any) tea.Cmd {
	return terminal.Complete(value)
}

func prompt[T any](
	ctx context.Context,
	client client,
	screen screen,
	display display,
) (T, error) {
	return terminal.Prompt[T](ctx, client, screen, display)
}
