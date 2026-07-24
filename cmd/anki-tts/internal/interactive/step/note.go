package step

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/internal/textutil"
)

type NoteSource interface {
	SelectNotes(context.Context, ankitts.NoteSelector) ([]anki.Note, error)
}

type NoteOptions struct {
	Selector         ankitts.NoteSelector
	SourceField      string
	DestinationField string
}

type notesLoadedMsg struct {
	notes []anki.Note
	err   error
}

type noteCandidate struct {
	note    anki.Note
	invalid string
}

// NoteScreen displays notes from the selected deck.
type NoteScreen struct {
	selectionScreen
	ctx             context.Context
	source          NoteSource
	deck            string
	status          string
	preferredNoteID int64
	reload          bool
	options         NoteOptions
}

func newNoteScreen(ctx context.Context, source NoteSource, deck string, options NoteOptions) *NoteScreen {
	screen := &NoteScreen{
		selectionScreen: newSelectionScreen("Loading notes — "+deck, nil),
		ctx:             ctx,
		source:          source,
		deck:            deck,
		options:         options,
	}
	screen.busy = true
	return screen
}

// ChooseNote presents or resumes note selection for one deck.
func ChooseNote(
	ctx context.Context,
	client Client,
	source NoteSource,
	deck string,
	options NoteOptions,
	previous *NoteScreen,
	display Display,
) (anki.Note, *NoteScreen, error) {
	display.Resume = previous != nil && !previous.reload
	if previous == nil {
		previous = newNoteScreen(ctx, source, deck, options)
	}
	previous.reload = false
	value, err := prompt[anki.Note](ctx, client, previous, display)
	return value, previous, err
}

// RefreshNoteList prepares an existing note screen to reload after generation.
func RefreshNoteList(screen *NoteScreen, status string, preferredNoteID int64) {
	screen.status = status
	screen.preferredNoteID = preferredNoteID
	screen.busy = true
	screen.reload = true
	screen.list.Title = "Loading notes — " + screen.deck
}

func (s *NoteScreen) Init() tea.Cmd {
	return tea.Batch(s.list.StartSpinner(), s.load())
}

func (s *NoteScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case notesLoadedMsg:
		s.list.StopSpinner()
		s.busy = false
		if msg.err != nil {
			return s, fail(msg.err, s.load())
		}
		setItems := s.setItems("Select a note — "+s.deck, noteListItems(msg.notes, s.options), s.preferredNoteID == 0)
		if s.preferredNoteID != 0 {
			for index, note := range msg.notes {
				if note.ID == s.preferredNoteID {
					s.list.Select(index)
					break
				}
			}
		}
		s.preferredNoteID = 0
		if s.status != "" {
			status := s.list.NewStatusMessage(s.status)
			s.status = ""
			return s, tea.Batch(setItems, status)
		}
		return s, setItems
	case tea.KeyPressMsg:
		if msg.String() == "enter" && !s.busy && !s.Filtering() {
			if selected, ok := s.selected(); ok {
				candidate := selected.value.(noteCandidate)
				if candidate.invalid != "" {
					return s, s.list.NewStatusMessage(candidate.invalid)
				}
				return s, complete(candidate.note)
			}
		}
	}
	return s, s.update(message)
}

func (s *NoteScreen) Retry() tea.Cmd {
	s.busy = true
	s.list.Title = "Loading notes — " + s.deck
	return s.list.StartSpinner()
}

func (s *NoteScreen) load() tea.Cmd {
	return func() tea.Msg {
		selector := s.options.Selector
		selector.Decks = []string{s.deck}
		notes, err := s.source.SelectNotes(s.ctx, selector)
		return notesLoadedMsg{notes: notes, err: err}
	}
}

func noteListItems(notes []anki.Note, options NoteOptions) []list.Item {
	items := make([]list.Item, 0, len(notes))
	for _, note := range notes {
		title := firstFieldValue(note)
		if title == "" {
			title = "(empty note)"
		}
		candidate := noteCandidate{note: note}
		if options.SourceField != "" {
			field, ok := note.Fields[options.SourceField]
			if !ok {
				candidate.invalid = fmt.Sprintf("note %d is missing source field %q", note.ID, options.SourceField)
			} else {
				text, _ := textutil.FromHTML(field.Value)
				if strings.TrimSpace(text) == "" {
					candidate.invalid = fmt.Sprintf("note %d has an empty source field %q", note.ID, options.SourceField)
				}
			}
		}
		if candidate.invalid == "" && options.DestinationField != "" {
			if _, ok := note.Fields[options.DestinationField]; !ok {
				candidate.invalid = fmt.Sprintf("note %d is missing destination field %q", note.ID, options.DestinationField)
			}
		}
		description := fmt.Sprintf("%s · note %d", note.ModelName, note.ID)
		if candidate.invalid != "" {
			description += " · DISABLED: " + candidate.invalid
		}
		items = append(items, listItem{title: title, description: description, value: candidate})
	}
	return items
}

func firstFieldValue(note anki.Note) string {
	fields := fieldListItems(note, true)
	if len(fields) == 0 {
		return ""
	}
	value := fields[0].(listItem).description
	return strings.ReplaceAll(value, "\n", " ")
}
