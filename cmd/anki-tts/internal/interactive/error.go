package interactive

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type errorModel struct {
	err   error
	retry tea.Cmd
}

func newErrorModel(err error, retry tea.Cmd) *errorModel { return &errorModel{err: err, retry: retry} }
func (m *errorModel) Init() tea.Cmd                      { return nil }
func (m *errorModel) kind() screenKind                   { return deckScreen }
func (m *errorModel) setSize(int, int)                   {}
func (m *errorModel) filtering() bool                    { return false }
func (m *errorModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "enter":
			if m.retry != nil {
				return m, messageCmd(retryMsg{cmd: m.retry})
			}
		case "esc":
			return m, messageCmd(dismissErrorMsg{})
		}
	}
	return m, nil
}
func (m *errorModel) View() tea.View {
	help := "Esc: back  q: quit"
	if m.retry != nil {
		help = "Enter: retry  " + help
	}
	return tea.NewView(fmt.Sprintf("Error: %v\n\n%s\n", m.err, help))
}
