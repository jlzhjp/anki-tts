package step

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
)

// DestinationFieldScreen displays fields that can receive generated audio.
type DestinationFieldScreen struct{ selectionScreen }

func newDestinationFieldScreen(note anki.Note) *DestinationFieldScreen {
	return &DestinationFieldScreen{
		selectionScreen: newSelectionScreen(
			"Select the destination field",
			fieldListItems(note, false),
		),
	}
}

// ChooseDestinationField presents or resumes destination-field selection.
func ChooseDestinationField(
	ctx context.Context,
	client Client,
	note anki.Note,
	previous *DestinationFieldScreen,
	display Display,
) (string, *DestinationFieldScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = newDestinationFieldScreen(note)
	}
	value, err := prompt[string](ctx, client, previous, display)
	return value, previous, err
}

func (s *DestinationFieldScreen) Init() tea.Cmd { return nil }

func (s *DestinationFieldScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "enter" && !s.Filtering() {
		if selected, ok := s.selected(); ok {
			return s, complete(selected.value.(string))
		}
	}
	return s, s.update(message)
}
