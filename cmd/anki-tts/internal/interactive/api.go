// Package interactive provides the interactive Anki TTS terminal UI.
package interactive

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

// Application is the business capability required by interactive mode.
type Application interface {
	ListDecks(context.Context) ([]string, error)
	SelectNotes(context.Context, ankitts.NoteSelector) ([]anki.Note, error)
	ServiceNames() []string
	HasAudioProcessors() bool
	Prepare(ankitts.GenerationRequest) (ankitts.Plan, error)
	Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error)
}

// Options constrains the interactive app and preselects generation values.
type Options struct {
	Selector  ankitts.NoteSelector
	FromField string
	ToField   string
	Service   string
	Yes       bool
}

// Run starts the interactive terminal UI.
func Run(ctx context.Context, app Application, options Options, input io.Reader, output io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	requests := make(chan screenRequest)
	done := make(chan error, 1)
	host := newScreenHost(ctx, cancel, requests, done)
	client := screenClient{requests: requests}
	go func() {
		done <- runWorkflow(ctx, client, app, options)
	}()

	_, err := tea.NewProgram(host, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return fmt.Errorf("run interactive UI: %w", err)
	}
	return nil
}
