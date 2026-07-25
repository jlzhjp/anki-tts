package main

import (
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/batch"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/interactive"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/terminal"
)

type application interface {
	SearchNotes(context.Context, ankitts.NoteQuery) (ankitts.NoteSelection, error)
	Notes(context.Context, ankitts.NoteSelection, ankitts.NoteLoadOptions) iter.Seq[ankitts.NoteResult]
	ServiceNames() []string
	HasAudioProcessors() bool
	Prepare(ankitts.GenerationRequest) (ankitts.Plan, error)
	Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error)
}

type runOptions struct {
	Query       ankitts.NoteQuery
	FromField   string
	ToField     string
	Service     string
	Yes         bool
	Interactive bool
}

func runApplication(
	ctx context.Context,
	app application,
	options runOptions,
	input io.Reader,
	output io.Writer,
) error {
	if app == nil {
		return fmt.Errorf("application is not configured")
	}
	if options.Interactive {
		return terminal.Run(ctx, input, output, true, func(ctx context.Context, client terminal.Client) terminal.Result {
			return terminal.Result{
				Err: interactive.Run(ctx, client, app, interactive.Options{
					Query:     options.Query,
					FromField: options.FromField,
					ToField:   options.ToField,
					Service:   options.Service,
					Yes:       options.Yes,
				}),
			}
		})
	}
	if err := validateBatchOptions(options); err != nil {
		return err
	}
	selection, err := app.SearchNotes(ctx, options.Query)
	if err != nil {
		return err
	}
	if len(selection.IDs) == 0 {
		fmt.Fprintln(output, "No notes matched the filter.")
		return nil
	}
	batchOptions := batch.Options{
		FromField: options.FromField,
		ToField:   options.ToField,
		Service:   options.Service,
		Yes:       options.Yes,
	}
	if !isTerminal(input) || !isTerminal(output) {
		return batch.RunPlain(ctx, app, batchOptions, selection, input, output)
	}
	return terminal.Run(ctx, input, output, false, func(ctx context.Context, client terminal.Client) terminal.Result {
		return batch.Run(ctx, client, app, batchOptions, selection)
	})
}

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

func validateBatchOptions(options runOptions) error {
	required := []struct{ name, value string }{
		{"--from-field", options.FromField},
		{"--to-field", options.ToField},
		{"--service", options.Service},
	}
	for _, flag := range required {
		if strings.TrimSpace(flag.value) == "" {
			return fmt.Errorf("%s is required in batch mode", flag.name)
		}
	}
	return nil
}
