package interactive

import tea "charm.land/bubbletea/v2"

type serviceModel struct{ selectionModel }

func newServiceModel(services []string) *serviceModel {
	return &serviceModel{selectionModel: newSelectionModel(serviceScreen, "Select a TTS service", serviceItems(services))}
}
func (m *serviceModel) Init() tea.Cmd { return nil }
func (m *serviceModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "enter" && !m.busy && !m.filtering() {
		if selected, ok := m.selected(); ok {
			return m, messageCmd(serviceSelectedMsg{service: selected.value.(string)})
		}
	}
	return m, m.update(message)
}
func (m *serviceModel) startGeneration(name string, transforming bool) tea.Cmd {
	m.busy = true
	m.list.Title = "Generating voice with " + name
	if transforming {
		m.list.Title = "Generating and transforming audio with " + name
	}
	return m.list.StartSpinner()
}
func (m *serviceModel) stopGeneration() { m.busy = false; m.list.StopSpinner() }
func (m *serviceModel) retrying() tea.Cmd {
	m.busy = true
	return m.list.StartSpinner()
}
