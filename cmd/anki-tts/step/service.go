package step

import (
	"context"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// TTSServiceScreen displays configured text-to-speech services.
type TTSServiceScreen struct{ selectionScreen }

func newTTSServiceScreen(services []string) *TTSServiceScreen {
	return &TTSServiceScreen{
		selectionScreen: newSelectionScreen("Select a TTS service", serviceListItems(services)),
	}
}

// ChooseTTSService presents or resumes text-to-speech service selection.
func ChooseTTSService(
	ctx context.Context,
	client Client,
	services []string,
	previous *TTSServiceScreen,
	display Display,
) (string, *TTSServiceScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = newTTSServiceScreen(services)
	}
	value, err := prompt[string](ctx, client, previous, display)
	return value, previous, err
}

func (s *TTSServiceScreen) Init() tea.Cmd { return nil }

func (s *TTSServiceScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "enter" && !s.busy && !s.Filtering() {
		if selected, ok := s.selected(); ok {
			return s, complete(selected.value.(string))
		}
	}
	return s, s.update(message)
}

func serviceListItems(services []string) []list.Item {
	items := make([]list.Item, 0, len(services))
	for _, service := range services {
		items = append(items, listItem{title: service, description: "Generate voice", value: service})
	}
	return items
}
