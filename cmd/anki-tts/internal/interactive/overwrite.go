package interactive

import (
	"context"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// destinationOverwriteScreen asks whether existing destination content may be replaced.
type destinationOverwriteScreen struct{ selectionScreen }

func newDestinationOverwriteScreen() *destinationOverwriteScreen {
	return &destinationOverwriteScreen{
		selectionScreen: newSelectionScreen(
			"Destination is not empty — replace it?",
			overwriteListItems(),
		),
	}
}

// confirmDestinationOverwrite presents or resumes overwrite confirmation.
func confirmDestinationOverwrite(
	ctx context.Context,
	client client,
	previous *destinationOverwriteScreen,
	display display,
) (bool, *destinationOverwriteScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = newDestinationOverwriteScreen()
	}
	value, err := prompt[bool](ctx, client, previous, display)
	return value, previous, err
}

func (s *destinationOverwriteScreen) Init() tea.Cmd { return nil }

func (s *destinationOverwriteScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "enter" && !s.Filtering() {
		if selected, ok := s.selected(); ok {
			return s, complete(selected.value.(bool))
		}
	}
	return s, s.update(message)
}

func overwriteListItems() []list.Item {
	return []list.Item{
		listItem{title: "Replace", description: "Replace the non-empty destination field", value: true},
		listItem{title: "Cancel", description: "Return without generating audio", value: false},
	}
}
