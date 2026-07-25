package interactive

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// ttsServiceScreen displays configured text-to-speech services.
type ttsServiceScreen struct{ selectionScreen }

func newTTSServiceScreen(services []string) *ttsServiceScreen {
	return &ttsServiceScreen{
		selectionScreen: newSelectionScreen("Select a TTS service", serviceListItems(services)),
	}
}

// chooseTTSService presents or resumes text-to-speech service selection.
func chooseTTSService(
	ctx context.Context,
	client client,
	services []string,
	previous *ttsServiceScreen,
	display display,
) (string, *ttsServiceScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = newTTSServiceScreen(services)
	}
	value, err := prompt[string](ctx, client, previous, display)
	return value, previous, err
}

func (s *ttsServiceScreen) Init() tea.Cmd { return nil }

func (s *ttsServiceScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "enter" && !s.busy && !s.Filtering() {
		if selected, ok := s.selected(); ok {
			value, ok := selected.value.(string)
			if !ok {
				return s, fail(fmt.Errorf("service selection has unexpected type %T", selected.value), nil)
			}
			return s, complete(value)
		}
	}
	command := s.update(message)
	return s, command
}

func serviceListItems(services []string) []list.Item {
	items := make([]list.Item, 0, len(services))
	for _, service := range services {
		items = append(items, listItem{title: service, description: "Generate voice", value: service})
	}
	return items
}
