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
	selection ankitts.NoteSelection,
) workflowResult {
	if !options.Yes {
		accepted, _, err := step.ConfirmBatch(
			ctx,
			client,
			selection.IDs,
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
	}

	plan, err := prepareTerminalBatch(ctx, client, func() (ankitts.Plan, error) {
		return prepareBatch(ctx, app, options, selection)
	})
	if err != nil {
		return workflowResult{err: err}
	}
	overwrites := overwriteNoteIDs(plan.Items())
	if !options.Yes && len(overwrites) > 0 {
		accepted, _, err := step.ConfirmBatch(
			ctx,
			client,
			overwrites,
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
