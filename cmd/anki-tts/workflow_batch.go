package main

import (
	"context"
	"errors"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/step"
)

func runBatchWorkflow(
	ctx context.Context,
	client step.Client,
	app application,
	options runOptions,
	plan ankitts.Plan,
) workflowResult {
	notes := plan.Items()
	if !options.Yes {
		accepted, _, err := step.ConfirmBatch(
			ctx,
			client,
			notes,
			false,
			nil,
			step.Display{},
		)
		if errors.Is(err, step.ErrBack) {
			return workflowResult{}
		}
		if err != nil {
			return workflowResult{err: err}
		}
		if !accepted {
			return workflowResult{}
		}

		if hasOverwrites(notes) {
			accepted, _, err = step.ConfirmBatch(
				ctx,
				client,
				notes,
				true,
				nil,
				step.Display{},
			)
			if errors.Is(err, step.ErrBack) {
				return workflowResult{}
			}
			if err != nil {
				return workflowResult{err: err}
			}
			if !accepted {
				return workflowResult{}
			}
		}
	}

	outcome, err := step.GenerateBatch(
		ctx,
		client,
		app,
		plan,
		step.Display{CancelIsError: true},
	)
	if err != nil {
		return workflowResult{err: err}
	}
	resultErr := batchResultError(outcome.Result, outcome.Err)
	return workflowResult{
		err:            resultErr,
		errorPresented: resultErr != nil,
	}
}

func hasOverwrites(notes []ankitts.PlannedNote) bool {
	for _, note := range notes {
		if note.WillOverwrite {
			return true
		}
	}
	return false
}
