package interactive

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/interactive/step"
)

type workflowStage uint8

const (
	deckStage workflowStage = iota
	noteStage
	sourceStage
	destinationStage
	actionStage
	serviceStage
	generationStage
)

type workflowState struct {
	deck             string
	note             anki.Note
	sourceField      string
	destinationField string
	service          string
}

func runWorkflow(ctx context.Context, client step.Client, app Application, options Options) error {
	if app == nil {
		return errors.New("interactive app is not configured")
	}
	services := app.ServiceNames()
	if len(services) == 0 {
		return errors.New("no TTS services are configured; add an [openrouter] table to config.toml")
	}
	if options.Service != "" && !contains(services, options.Service) {
		return fmt.Errorf("TTS service %q is not configured", options.Service)
	}

	state := workflowState{}
	stage := deckStage
	var decks *step.DeckScreen
	var notes *step.NoteScreen
	var source *step.SourceFieldScreen
	var destination *step.DestinationFieldScreen
	var overwrite *step.DestinationOverwriteScreen
	var service *step.TTSServiceScreen

	if len(options.Selector.Decks) == 1 {
		state.deck = options.Selector.Decks[0]
		stage = noteStage
	}

	for {
		switch stage {
		case deckStage:
			deck, screen, err := step.ChooseDeck(
				ctx, client, app, options.Selector.Decks, decks, state.display(1),
			)
			decks = screen
			if errors.Is(err, step.ErrBack) {
				return nil
			}
			if err != nil {
				return err
			}
			if deck != state.deck {
				state = workflowState{deck: deck}
				notes, source, destination, overwrite, service = nil, nil, nil, nil, nil
			}
			stage = noteStage

		case noteStage:
			note, screen, err := step.ChooseNote(
				ctx,
				client,
				app,
				state.deck,
				step.NoteOptions{
					Selector:         options.Selector,
					SourceField:      options.FromField,
					DestinationField: options.ToField,
				},
				notes,
				state.display(2),
			)
			notes = screen
			if errors.Is(err, step.ErrBack) {
				if decks == nil {
					return nil
				}
				stage = deckStage
				continue
			}
			if err != nil {
				return err
			}
			if note.ID != state.note.ID {
				state.note = note
				state.sourceField, state.destinationField, state.service = "", "", ""
				source, destination, overwrite, service = nil, nil, nil, nil
			}
			if options.FromField != "" {
				state.sourceField = options.FromField
				stage = destinationStage
			} else {
				stage = sourceStage
			}

		case sourceStage:
			field, screen, err := step.ChooseSourceField(
				ctx, client, state.note, source, state.display(3),
			)
			source = screen
			if errors.Is(err, step.ErrBack) {
				stage = noteStage
				continue
			}
			if err != nil {
				return err
			}
			if field != state.sourceField {
				state.sourceField = field
				state.destinationField, state.service = "", ""
				destination, overwrite, service = nil, nil, nil
			}
			stage = destinationStage

		case destinationStage:
			if options.ToField != "" {
				state.destinationField = options.ToField
			} else {
				field, screen, err := step.ChooseDestinationField(
					ctx, client, state.note, destination, state.display(4),
				)
				destination = screen
				if errors.Is(err, step.ErrBack) {
					if options.FromField != "" {
						stage = noteStage
					} else {
						stage = sourceStage
					}
					continue
				}
				if err != nil {
					return err
				}
				if field != state.destinationField {
					state.destinationField, state.service = field, ""
					overwrite, service = nil, nil
				}
			}
			field, ok := state.note.Fields[state.destinationField]
			if !ok {
				return fmt.Errorf("note %d has no field %q", state.note.ID, state.destinationField)
			}
			if strings.TrimSpace(field.Value) != "" && !options.Yes {
				stage = actionStage
			} else {
				stage = serviceStage
			}

		case actionStage:
			confirmed, screen, err := step.ConfirmDestinationOverwrite(
				ctx, client, overwrite, state.display(5),
			)
			overwrite = screen
			if errors.Is(err, step.ErrBack) {
				stage = stageBeforeAction(options)
				continue
			}
			if err != nil {
				return err
			}
			if !confirmed {
				stage = stageBeforeAction(options)
				continue
			}
			stage = serviceStage

		case serviceStage:
			if options.Service != "" {
				state.service = options.Service
			} else {
				selected, screen, err := step.ChooseTTSService(
					ctx, client, services, service, state.display(6),
				)
				service = screen
				if errors.Is(err, step.ErrBack) {
					field := state.note.Fields[state.destinationField]
					if strings.TrimSpace(field.Value) != "" && !options.Yes {
						stage = actionStage
					} else if options.ToField == "" {
						stage = destinationStage
					} else if options.FromField == "" {
						stage = sourceStage
					} else {
						stage = noteStage
					}
					continue
				}
				if err != nil {
					return err
				}
				state.service = selected
			}
			stage = generationStage

		case generationStage:
			request := ankitts.GenerationRequest{
				Notes:            []anki.Note{state.note},
				SourceField:      state.sourceField,
				DestinationField: state.destinationField,
				Service:          state.service,
			}
			result, err := step.GenerateNoteAudio(ctx, client, app, request, state.display(6))
			if err != nil {
				return err
			}
			step.RefreshNoteList(notes, saveStatus(result, state.destinationField), state.note.ID)
			state.note = anki.Note{}
			state.sourceField, state.destinationField, state.service = "", "", ""
			source, destination, overwrite, service = nil, nil, nil, nil
			stage = noteStage
		}
	}
}

func (s workflowState) contextLine() string {
	parts := make([]string, 0, 5)
	if s.deck != "" {
		parts = append(parts, "Deck: "+s.deck)
	}
	if s.note.ID != 0 {
		parts = append(parts, fmt.Sprintf("Note: %d", s.note.ID))
	}
	if s.sourceField != "" {
		parts = append(parts, "Source: "+s.sourceField)
	}
	if s.destinationField != "" {
		parts = append(parts, "Destination: "+s.destinationField)
	}
	if s.service != "" {
		parts = append(parts, "Service: "+s.service)
	}
	return strings.Join(parts, " · ")
}

func (s workflowState) display(number int) step.Display {
	return step.Display{Step: number, Context: s.contextLine()}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func stageBeforeAction(options Options) workflowStage {
	if options.ToField == "" {
		return destinationStage
	}
	if options.FromField == "" {
		return sourceStage
	}
	return noteStage
}

func saveStatus(result ankitts.GenerateResult, destination string) string {
	var costStatus string
	if result.Cost != nil {
		costStatus = fmt.Sprintf("Cost: $%.6f · ", *result.Cost)
	} else if result.CostErr != nil {
		costStatus = fmt.Sprintf("Cost unavailable: %v · ", result.CostErr)
	}
	return costStatus + fmt.Sprintf("Saved %s to %s", result.Filename, destination)
}
