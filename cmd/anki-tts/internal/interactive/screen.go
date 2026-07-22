package interactive

import (
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type screenModel interface {
	tea.Model
	kind() screenKind
	setSize(int, int)
	filtering() bool
}

type retryableScreen interface{ retrying() tea.Cmd }

type selectionModel struct {
	kindValue screenKind
	list      list.Model
	busy      bool
}

func newSelectionModel(kind screenKind, title string, items []list.Item) selectionModel {
	l := list.New(items, list.NewDefaultDelegate(), 80, 20)
	l.Title = title
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	l.StatusMessageLifetime = 10 * time.Second
	return selectionModel{kindValue: kind, list: l}
}

func (m *selectionModel) kind() screenKind { return m.kindValue }
func (m *selectionModel) setSize(w, h int) { m.list.SetSize(w, h) }
func (m *selectionModel) filtering() bool  { return m.list.FilterState() == list.Filtering }
func (m *selectionModel) selected() (item, bool) {
	value, ok := m.list.SelectedItem().(item)
	return value, ok
}
func (m *selectionModel) View() tea.View { return tea.NewView(m.list.View()) }
func (m *selectionModel) update(message tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(message)
	return cmd
}
func (m *selectionModel) setItems(title string, items []list.Item, reset bool) tea.Cmd {
	m.list.Title = title
	cmd := m.list.SetItems(items)
	if reset {
		m.list.ResetSelected()
	}
	return cmd
}
