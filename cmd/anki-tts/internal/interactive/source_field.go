package interactive

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
)

// sourceFieldScreen displays non-empty fields that can provide speech text.
type sourceFieldScreen struct{ selectionScreen }

func newSourceFieldScreen(note anki.Note) (*sourceFieldScreen, error) {
	fields := fieldListItems(note, true)
	if len(fields) == 0 {
		return nil, errors.New("this note has no non-empty source fields")
	}
	return &sourceFieldScreen{
		selectionScreen: newSelectionScreen("Select the source field", fields),
	}, nil
}

// chooseSourceField presents or resumes source-field selection.
func chooseSourceField(
	ctx context.Context,
	client client,
	note anki.Note,
	previous *sourceFieldScreen,
	display display,
) (string, *sourceFieldScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		var err error
		previous, err = newSourceFieldScreen(note)
		if err != nil {
			return "", nil, err
		}
	}
	value, err := prompt[string](ctx, client, previous, display)
	return value, previous, err
}

func (s *sourceFieldScreen) Init() tea.Cmd { return nil }

func (s *sourceFieldScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok && key.String() == "enter" && !s.Filtering() {
		if selected, ok := s.selected(); ok {
			return s, complete(selected.value.(string))
		}
	}
	return s, s.update(message)
}
