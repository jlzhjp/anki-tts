package main

import (
	"context"
	"fmt"
	"io"
	"iter"
	"strings"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/step"
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
		return runTerminal(ctx, input, output, true, func(ctx context.Context, client step.Client) workflowResult {
			return workflowResult{
				err: runInteractiveWorkflow(ctx, client, app, options),
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
	if !isTerminal(input) || !isTerminal(output) {
		return runPlainBatch(ctx, app, options, selection, input, output)
	}
	return runTerminal(ctx, input, output, false, func(ctx context.Context, client step.Client) workflowResult {
		return runBatchWorkflow(ctx, client, app, options, selection)
	})
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
