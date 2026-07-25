package step

import (
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type listItem struct {
	title       string
	description string
	value       any
}

func (i listItem) Title() string       { return i.title }
func (i listItem) Description() string { return i.description }
func (i listItem) FilterValue() string { return i.title + " " + i.description }

type selectionScreen struct {
	list list.Model
	busy bool
}

func newSelectionScreen(title string, items []list.Item) selectionScreen {
	model := list.New(items, list.NewDefaultDelegate(), 80, 20)
	model.Title = title
	model.SetShowStatusBar(true)
	model.SetFilteringEnabled(true)
	model.DisableQuitKeybindings()
	model.StatusMessageLifetime = 10 * time.Second
	return selectionScreen{list: model}
}

func (s *selectionScreen) SetSize(w, h int) { s.list.SetSize(w, h) }
func (s *selectionScreen) Filtering() bool  { return s.list.FilterState() == list.Filtering }
func (s *selectionScreen) View() tea.View   { return tea.NewView(s.list.View()) }
func (s *selectionScreen) selected() (listItem, bool) {
	item, ok := s.list.SelectedItem().(listItem)
	return item, ok
}

func (s *selectionScreen) update(message tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(message)
	return cmd
}

func (s *selectionScreen) setItems(title string, items []list.Item, reset bool) tea.Cmd {
	s.list.Title = title
	cmd := s.list.SetItems(items)
	if reset {
		s.list.ResetSelected()
	}
	return cmd
}
