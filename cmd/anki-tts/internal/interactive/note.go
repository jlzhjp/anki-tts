package interactive

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

type noteModel struct {
	selectionModel
	ctx             context.Context
	app             Application
	deck            string
	status          string
	preferredNoteID int64
	options         Options
}

func newNoteModel(ctx context.Context, app Application, deck string, options Options) *noteModel {
	m := &noteModel{selectionModel: newSelectionModel(noteScreen, "Loading notes — "+deck, nil), ctx: ctx, app: app, deck: deck, options: options}
	m.busy = true
	return m
}
func (m *noteModel) Init() tea.Cmd { return tea.Batch(m.list.StartSpinner(), m.loadCmd()) }
func (m *noteModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case notesLoadedMsg:
		m.list.StopSpinner()
		m.busy = false
		if msg.err != nil {
			return m, failCmd(msg.err, m.loadCmd())
		}
		setItemsCmd := m.setItems("Select a note — "+m.deck, noteItems(msg.notes, m.options), m.preferredNoteID == 0)
		if m.preferredNoteID != 0 {
			for index, note := range msg.notes {
				if note.ID == m.preferredNoteID {
					m.list.Select(index)
					break
				}
			}
		}
		m.preferredNoteID = 0
		if m.status != "" {
			statusCmd := m.list.NewStatusMessage(m.status)
			m.status = ""
			return m, tea.Batch(setItemsCmd, statusCmd)
		}
		return m, setItemsCmd
	case tea.KeyPressMsg:
		if msg.String() == "enter" && !m.busy && !m.filtering() {
			if selected, ok := m.selected(); ok {
				candidate := selected.value.(noteCandidate)
				if candidate.invalid != "" {
					return m, m.list.NewStatusMessage(candidate.invalid)
				}
				return m, messageCmd(noteSelectedMsg{note: candidate.note})
			}
		}
	}
	return m, m.update(message)
}
func (m *noteModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		selector := m.options.Selector
		selector.Decks = []string{m.deck}
		notes, err := m.app.SelectNotes(m.ctx, selector)
		return notesLoadedMsg{notes: notes, err: err}
	}
}
func (m *noteModel) refresh(status string, noteID int64) tea.Cmd {
	m.status, m.preferredNoteID, m.busy = status, noteID, true
	m.list.Title = "Loading notes — " + m.deck
	return tea.Batch(m.list.StartSpinner(), m.loadCmd())
}
func (m *noteModel) retrying() tea.Cmd {
	m.busy = true
	m.list.Title = "Loading notes — " + m.deck
	return m.list.StartSpinner()
}
