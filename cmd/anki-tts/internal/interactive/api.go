// Package interactive provides the interactive Anki TTS terminal UI.
package interactive

import (
	"context"
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"

	ankitts "jlzhjp.dev/anki-tts"
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
	model := newInteractiveModel(ctx, app, options)
	_, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return fmt.Errorf("run interactive UI: %w", err)
	}
	return nil
}
