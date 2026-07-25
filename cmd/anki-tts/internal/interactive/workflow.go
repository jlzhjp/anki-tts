package interactive

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/cmd/anki-tts/internal/terminal"
)

// Run executes the interactive note-at-a-time workflow.
func Run(
	ctx context.Context,
	client terminal.Client,
	app Application,
	options Options,
) error {
	services := app.ServiceNames()
	if len(services) == 0 {
		return errors.New("no TTS services are configured; add an [openrouter] table to config.toml")
	}
	if options.Service != "" && !slices.Contains(services, options.Service) {
		return fmt.Errorf("TTS service %q is not configured", options.Service)
	}

	workflow := &interactiveWorkflow{
		client: client, app: app, options: options, services: services,
	}
	return workflow.run(ctx)
}

func (w *interactiveWorkflow) run(ctx context.Context) error {
	_, err := w.runNotes(ctx)
	return err
}

func (w *interactiveWorkflow) runNotes(ctx context.Context) (navigation, error) {
	for {
		note, screen, err := chooseNote(
			ctx,
			w.client,
			w.app,
			noteOptions{
				Query:            w.options.Query,
				SourceField:      w.options.FromField,
				DestinationField: w.options.ToField,
			},
			w.screens.note,
			w.display(),
		)
		w.screens.note = screen
		if errors.Is(err, errBack) {
			return navigateBack, nil
		}
		if err != nil {
			return navigateBack, err
		}

		w.setNote(note)
		next, err := w.runNote(ctx)
		if err != nil {
			return navigateBack, err
		}
		if next == navigateBack {
			continue
		}
	}
}

func (w *interactiveWorkflow) runNote(ctx context.Context) (navigation, error) {
	if w.options.FromField != "" {
		w.setSourceField(w.options.FromField)
		return w.withSource(ctx)
	}

	for {
		field, screen, err := chooseSourceField(
			ctx,
			w.client,
			w.state.note,
			w.screens.source,
			w.display(),
		)
		w.screens.source = screen
		if errors.Is(err, errBack) {
			return navigateBack, nil
		}
		if err != nil {
			return navigateBack, err
		}

		w.setSourceField(field)
		next, err := w.withSource(ctx)
		if err != nil {
			return navigateBack, err
		}
		if next == navigateBack {
			continue
		}
		return next, nil
	}
}

func (w *interactiveWorkflow) withSource(ctx context.Context) (navigation, error) {
	if w.options.ToField != "" {
		w.setDestinationField(w.options.ToField)
		return w.withDestination(ctx)
	}

	for {
		field, screen, err := chooseDestinationField(
			ctx,
			w.client,
			w.state.note,
			w.screens.destination,
			w.display(),
		)
		w.screens.destination = screen
		if errors.Is(err, errBack) {
			return navigateBack, nil
		}
		if err != nil {
			return navigateBack, err
		}

		w.setDestinationField(field)
		next, err := w.withDestination(ctx)
		if err != nil {
			return navigateBack, err
		}
		if next == navigateBack {
			continue
		}
		return next, nil
	}
}

func (w *interactiveWorkflow) withDestination(ctx context.Context) (navigation, error) {
	field, ok := w.state.note.Fields[w.state.destinationField]
	if !ok {
		return navigateBack, fmt.Errorf(
			"note %d has no field %q",
			w.state.note.ID,
			w.state.destinationField,
		)
	}
	confirmOverwrite := strings.TrimSpace(field.Value) != "" && !w.options.Yes

	for {
		if confirmOverwrite {
			confirmed, screen, err := confirmDestinationOverwrite(
				ctx,
				w.client,
				w.screens.overwrite,
				w.display(),
			)
			w.screens.overwrite = screen
			if errors.Is(err, errBack) {
				return navigateBack, nil
			}
			if err != nil {
				return navigateBack, err
			}
			if !confirmed {
				return navigateBack, nil
			}
		}

		next, err := w.chooseServiceAndGenerate(ctx)
		if err != nil {
			return navigateBack, err
		}
		if next == navigateBack && confirmOverwrite {
			continue
		}
		return next, nil
	}
}

func (w *interactiveWorkflow) chooseServiceAndGenerate(
	ctx context.Context,
) (navigation, error) {
	if w.options.Service != "" {
		w.setService(w.options.Service)
		return w.generate(ctx)
	}

	selected, screen, err := chooseTTSService(
		ctx,
		w.client,
		w.services,
		w.screens.service,
		w.display(),
	)
	w.screens.service = screen
	if errors.Is(err, errBack) {
		return navigateBack, nil
	}
	if err != nil {
		return navigateBack, err
	}

	w.setService(selected)
	return w.generate(ctx)
}

func (w *interactiveWorkflow) generate(ctx context.Context) (navigation, error) {
	request := ankitts.GenerationRequest{
		Notes:            ankitts.NoteResults(w.state.note),
		SourceField:      w.state.sourceField,
		DestinationField: w.state.destinationField,
		Service:          w.state.service,
	}
	result, err := generateNoteAudio(
		ctx,
		w.client,
		w.app,
		request,
		w.display(),
	)
	if err != nil {
		return navigateBack, err
	}

	refreshNoteList(
		w.screens.note,
		saveStatus(result, w.state.destinationField),
		w.state.note.ID,
	)
	w.resetAfterGeneration()
	return navigateNext, nil
}
