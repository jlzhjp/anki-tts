package interactive

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type fieldModel struct{ selectionModel }

func newFieldModel(kind screenKind, title string, items []list.Item) *fieldModel {
	return &fieldModel{selectionModel: newSelectionModel(kind, title, items)}
}
func (m *fieldModel) Init() tea.Cmd { return nil }
func (m *fieldModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "enter" && !m.filtering() {
		if selected, ok := m.selected(); ok {
			if m.kindValue == sourceScreen {
				return m, messageCmd(sourceSelectedMsg{field: selected.value.(string)})
			}
			return m, messageCmd(destinationSelectedMsg{field: selected.value.(string)})
		}
	}
	return m, m.update(message)
}
