// Package batch runs batch-mode note selection, confirmation, and generation.
package batch

import (
	"context"
	"io"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

// Application is the business capability required by batch mode.
type Application interface {
	SelectNotes(context.Context, ankitts.NoteSelector) ([]anki.Note, error)
	Prepare(ankitts.GenerationRequest) (ankitts.Plan, error)
	Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error)
}

// Options configures one batch-mode run.
type Options struct {
	Selector  ankitts.NoteSelector
	FromField string
	ToField   string
	Service   string
	Yes       bool
}

// Run selects, confirms, and processes notes using terminal-appropriate UI.
func Run(ctx context.Context, app Application, options Options, input io.Reader, output io.Writer) error {
	return run(ctx, app, options, input, output)
}
