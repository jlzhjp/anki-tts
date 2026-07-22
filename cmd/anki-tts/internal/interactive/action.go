package interactive

import tea "charm.land/bubbletea/v2"

type actionModel struct{ selectionModel }

func newActionModel() *actionModel {
	return &actionModel{selectionModel: newSelectionModel(actionScreen, "Destination is not empty — replace it?", actionItems())}
}
func (m *actionModel) Init() tea.Cmd { return nil }
func (m *actionModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "enter" && !m.filtering() {
		if selected, ok := m.selected(); ok {
			return m, messageCmd(actionSelectedMsg{confirmed: selected.value.(bool)})
		}
	}
	return m, m.update(message)
}
