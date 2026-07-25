package step

import (
	"context"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// DestinationOverwriteScreen asks whether existing destination content may be replaced.
type DestinationOverwriteScreen struct{ selectionScreen }

func newDestinationOverwriteScreen() *DestinationOverwriteScreen {
	return &DestinationOverwriteScreen{
		selectionScreen: newSelectionScreen(
			"Destination is not empty — replace it?",
			overwriteListItems(),
		),
	}
}

// ConfirmDestinationOverwrite presents or resumes overwrite confirmation.
func ConfirmDestinationOverwrite(
	ctx context.Context,
	client Client,
	previous *DestinationOverwriteScreen,
	display Display,
) (bool, *DestinationOverwriteScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = newDestinationOverwriteScreen()
	}
	value, err := prompt[bool](ctx, client, previous, display)
	return value, previous, err
}

func (s *DestinationOverwriteScreen) Init() tea.Cmd { return nil }

func (s *DestinationOverwriteScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
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
