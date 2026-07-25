package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/step"
)

type application interface {
	ListDecks(context.Context) ([]string, error)
	SelectNotes(context.Context, ankitts.NoteSelector) ([]anki.Note, error)
	ServiceNames() []string
	HasAudioProcessors() bool
	Prepare(ankitts.GenerationRequest) (ankitts.Plan, error)
	Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error)
}

type runOptions struct {
	Selector    ankitts.NoteSelector
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
	plan, ok, err := prepareBatch(ctx, app, options)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(output, "No notes matched the selectors.")
		return nil
	}
	if !isTerminal(input) || !isTerminal(output) {
		return runPlainBatch(ctx, app, options, plan, input, output)
	}
	return runTerminal(ctx, input, output, false, func(ctx context.Context, client step.Client) workflowResult {
		return runBatchWorkflow(ctx, client, app, options, plan)
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
