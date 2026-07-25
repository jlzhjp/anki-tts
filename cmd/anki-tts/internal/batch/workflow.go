package batch

import (
	"context"
	"errors"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/cmd/anki-tts/internal/terminal"
)

// Run executes the terminal batch workflow.
func Run(
	ctx context.Context,
	client terminal.Client,
	app Application,
	options Options,
	selection ankitts.NoteSelection,
) terminal.Result {
	if !options.Yes {
		accepted, _, err := confirm(
			ctx,
			client,
			selection.IDs,
			false,
			nil,
			display{},
		)
		if errors.Is(err, errBack) {
			return terminal.Result{}
		}
		if err != nil {
			return terminal.Result{Err: err}
		}
		if !accepted {
			return terminal.Result{}
		}
	}

	plan, err := prepareTerminal(ctx, client, func() (ankitts.Plan, error) {
		return prepare(ctx, app, options, selection)
	})
	if err != nil {
		return terminal.Result{Err: err}
	}
	overwrites := overwriteNoteIDs(plan.Items())
	if !options.Yes && len(overwrites) > 0 {
		accepted, _, err := confirm(
			ctx,
			client,
			overwrites,
			true,
			nil,
			display{},
		)
		if errors.Is(err, errBack) {
			return terminal.Result{}
		}
		if err != nil {
			return terminal.Result{Err: err}
		}
		if !accepted {
			return terminal.Result{}
		}
	}

	outcome, err := generate(
		ctx,
		client,
		app,
		plan,
		display{CancelIsError: true},
	)
	if err != nil {
		return terminal.Result{Err: err}
	}
	resultErr := resultError(outcome.result, outcome.err)
	return terminal.Result{
		Err:            resultErr,
		ErrorPresented: resultErr != nil,
	}
}
