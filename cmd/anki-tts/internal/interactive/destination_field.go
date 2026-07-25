package interactive

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
)

// destinationFieldScreen displays fields that can receive generated audio.
type destinationFieldScreen struct{ selectionScreen }

func newDestinationFieldScreen(note anki.Note) *destinationFieldScreen {
	return &destinationFieldScreen{
		selectionScreen: newSelectionScreen(
			"Select the destination field",
			fieldListItems(note, false),
		),
	}
}

// chooseDestinationField presents or resumes destination-field selection.
func chooseDestinationField(
	ctx context.Context,
	client client,
	note anki.Note,
	previous *destinationFieldScreen,
	display display,
) (string, *destinationFieldScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = newDestinationFieldScreen(note)
	}
	value, err := prompt[string](ctx, client, previous, display)
	return value, previous, err
}

func (s *destinationFieldScreen) Init() tea.Cmd { return nil }

func (s *destinationFieldScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "enter" && !s.Filtering() {
		if selected, ok := s.selected(); ok {
			return s, complete(selected.value.(string))
		}
	}
	return s, s.update(message)
}
