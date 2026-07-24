package step

import (
	"context"
	"sort"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type DeckSource interface {
	ListDecks(context.Context) ([]string, error)
}

type decksLoadedMsg struct {
	decks []string
	err   error
}

// DeckScreen displays the available Anki decks.
type DeckScreen struct {
	selectionScreen
	ctx     context.Context
	source  DeckSource
	allowed []string
}

func newDeckScreen(ctx context.Context, source DeckSource, allowed []string) *DeckScreen {
	screen := &DeckScreen{
		selectionScreen: newSelectionScreen("Anki TTS — loading decks", nil),
		ctx:             ctx,
		source:          source,
		allowed:         allowed,
	}
	screen.busy = true
	return screen
}

// ChooseDeck presents or resumes the deck-selection screen.
func ChooseDeck(
	ctx context.Context,
	client Client,
	source DeckSource,
	allowed []string,
	previous *DeckScreen,
	display Display,
) (string, *DeckScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = newDeckScreen(ctx, source, allowed)
	}
	value, err := prompt[string](ctx, client, previous, display)
	return value, previous, err
}

func (s *DeckScreen) Init() tea.Cmd {
	return tea.Batch(s.list.StartSpinner(), s.load())
}

func (s *DeckScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case decksLoadedMsg:
		s.list.StopSpinner()
		s.busy = false
		if msg.err != nil {
			return s, fail(msg.err, s.load())
		}
		return s, s.setItems("Select a deck", deckListItems(msg.decks), true)
	case tea.KeyPressMsg:
		if msg.String() == "enter" && !s.busy && !s.Filtering() {
			if selected, ok := s.selected(); ok {
				return s, complete(selected.value.(string))
			}
		}
	}
	return s, s.update(message)
}

func (s *DeckScreen) Retry() tea.Cmd {
	s.busy = true
	s.list.Title = "Loading decks"
	return s.list.StartSpinner()
}

func (s *DeckScreen) load() tea.Cmd {
	return func() tea.Msg {
		if len(s.allowed) > 0 {
			return decksLoadedMsg{decks: s.allowed}
		}
		decks, err := s.source.ListDecks(s.ctx)
		return decksLoadedMsg{decks: decks, err: err}
	}
}

func deckListItems(decks []string) []list.Item {
	sorted := append([]string(nil), decks...)
	sort.Strings(sorted)
	items := make([]list.Item, 0, len(sorted))
	for _, deck := range sorted {
		items = append(items, listItem{title: deck, value: deck})
	}
	return items
}
