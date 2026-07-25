package interactive

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/ankitts/cmd/anki-tts/internal/terminal"
)

type (
	client  = terminal.Client
	display = terminal.Display
	screen  = terminal.Screen
)

var errBack = terminal.ErrBack

func complete(value any) tea.Cmd {
	return terminal.Complete(value)
}

func fail(err error, retry tea.Cmd) tea.Cmd {
	return terminal.Fail(err, retry)
}

func prompt[T any](
	ctx context.Context,
	client client,
	screen screen,
	display display,
) (T, error) {
	return terminal.Prompt[T](ctx, client, screen, display)
}
