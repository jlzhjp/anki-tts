package interactive

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type deckModel struct {
	selectionModel
	ctx   context.Context
	app   Application
	decks []string
}

func newDeckModel(ctx context.Context, app Application, decks []string) *deckModel {
	m := &deckModel{selectionModel: newSelectionModel(deckScreen, "Anki TTS — loading decks", nil), ctx: ctx, app: app, decks: decks}
	m.busy = true
	return m
}
func (m *deckModel) Init() tea.Cmd { return tea.Batch(m.list.StartSpinner(), m.loadCmd()) }
func (m *deckModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case decksLoadedMsg:
		m.list.StopSpinner()
		m.busy = false
		if msg.err != nil {
			return m, failCmd(msg.err, m.loadCmd())
		}
		return m, m.setItems("Select a deck", deckItems(msg.decks), true)
	case tea.KeyPressMsg:
		if msg.String() == "enter" && !m.busy && !m.filtering() {
			if selected, ok := m.selected(); ok {
				return m, messageCmd(deckSelectedMsg{deck: selected.value.(string)})
			}
		}
	}
	return m, m.update(message)
}
func (m *deckModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		if len(m.decks) > 0 {
			return decksLoadedMsg{decks: m.decks}
		}
		decks, err := m.app.ListDecks(m.ctx)
		return decksLoadedMsg{decks: decks, err: err}
	}
}
func (m *deckModel) retrying() tea.Cmd {
	m.busy = true
	m.list.Title = "Loading decks"
	return m.list.StartSpinner()
}
