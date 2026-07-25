package interactive

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/internal/textutil"
)

const (
	interactiveNoteWindowSize        = 50
	interactiveNotePrefetchThreshold = 10
)

type noteSource interface {
	SearchNotes(context.Context, ankitts.NoteQuery) (ankitts.NoteSelection, error)
	Notes(context.Context, ankitts.NoteSelection, ankitts.NoteLoadOptions) iter.Seq[ankitts.NoteResult]
}

type noteOptions struct {
	SourceField      string
	DestinationField string
	Query            ankitts.NoteQuery
}

type noteStreamStartedMsg struct {
	next   func() (ankitts.NoteResult, bool)
	stop   func()
	cancel context.CancelFunc
	err    error
}

type notesLoadedMsg struct {
	results []ankitts.NoteResult
	done    bool
}

type noteRefreshedMsg struct {
	err  error
	note anki.Note
}

type noteCandidate struct {
	invalid string
	note    anki.Note
}

// noteScreen progressively displays notes matching an Anki search.
type noteScreen struct {
	ctx          context.Context
	source       noteSource
	stopIterator func()
	cancelStream context.CancelFunc
	nextResult   func() (ankitts.NoteResult, bool)
	selectionScreen
	options         noteOptions
	status          string
	notes           []anki.Note
	preferredNoteID int64
	reload          bool
	loading         bool
	exhausted       bool
}

func newNoteScreen(ctx context.Context, source noteSource, options noteOptions) *noteScreen {
	screen := &noteScreen{
		selectionScreen: newSelectionScreen("Loading notes", nil),
		ctx:             ctx,
		source:          source,
		options:         options,
	}
	screen.busy = true
	return screen
}

// chooseNote presents or resumes progressive note selection.
func chooseNote(
	ctx context.Context,
	client client,
	source noteSource,
	options noteOptions,
	previous *noteScreen,
	display display,
) (anki.Note, *noteScreen, error) {
	display.Resume = previous != nil && !previous.reload
	if previous == nil {
		previous = newNoteScreen(ctx, source, options)
	}
	previous.reload = false
	value, err := prompt[anki.Note](ctx, client, previous, display)
	return value, previous, err
}

// refreshNoteList reloads one generated note without restarting the search.
func refreshNoteList(screen *noteScreen, status string, preferredNoteID int64) {
	screen.status = status
	screen.preferredNoteID = preferredNoteID
	screen.busy = true
	screen.reload = true
	screen.list.Title = "Refreshing note"
}

func (s *noteScreen) Init() tea.Cmd {
	if s.preferredNoteID != 0 && len(s.notes) > 0 {
		return tea.Batch(s.list.StartSpinner(), s.refresh())
	}
	return tea.Batch(s.list.StartSpinner(), s.start())
}

func (s *noteScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case noteStreamStartedMsg:
		if msg.err != nil {
			s.busy = false
			s.list.StopSpinner()
			return s, fail(msg.err, s.start())
		}
		s.nextResult = msg.next
		s.stopIterator = msg.stop
		s.cancelStream = msg.cancel
		s.loading = true
		command := s.loadWindow()
		return s, command

	case notesLoadedMsg:
		s.loading = false
		for _, result := range msg.results {
			if result.Err != nil {
				s.busy = false
				s.list.StopSpinner()
				s.stopStream()
				return s, fail(result.Err, s.start())
			}
			s.notes = append(s.notes, result.Note)
		}
		s.exhausted = msg.done
		s.busy = false
		s.list.StopSpinner()
		title := fmt.Sprintf("Select a note · %d loaded", len(s.notes))
		if s.exhausted {
			title = fmt.Sprintf("Select a note · %d total", len(s.notes))
		}
		if len(s.notes) == 0 && s.exhausted {
			title = "No notes matched the filter"
		}
		cmd := s.setItems(title, noteListItems(s.notes, s.options), len(s.notes) == len(msg.results))
		s.selectPreferred()
		return s, cmd

	case noteRefreshedMsg:
		s.busy = false
		s.list.StopSpinner()
		if msg.err != nil {
			return s, fail(msg.err, s.refresh())
		}
		for index := range s.notes {
			if s.notes[index].ID == msg.note.ID {
				s.notes[index] = msg.note
				break
			}
		}
		cmd := s.setItems(
			fmt.Sprintf("Select a note · %d loaded", len(s.notes)),
			noteListItems(s.notes, s.options),
			false,
		)
		s.selectPreferred()
		if s.status != "" {
			status := s.list.NewStatusMessage(s.status)
			s.status = ""
			return s, tea.Batch(cmd, status)
		}
		return s, cmd

	case tea.KeyPressMsg:
		if msg.String() == "enter" && !s.busy && !s.Filtering() {
			if selected, ok := s.selected(); ok {
				candidate, ok := selected.value.(noteCandidate)
				if !ok {
					return s, fail(fmt.Errorf("note selection has unexpected type %T", selected.value), nil)
				}
				if candidate.invalid != "" {
					return s, s.list.NewStatusMessage(candidate.invalid)
				}
				return s, complete(candidate.note)
			}
		}
	}

	cmd := s.update(message)
	if !s.busy && !s.loading && !s.exhausted &&
		len(s.notes)-1-s.list.Index() <= interactiveNotePrefetchThreshold {
		s.busy = true
		s.loading = true
		return s, tea.Batch(cmd, s.list.StartSpinner(), s.loadWindow())
	}
	return s, cmd
}

func (s *noteScreen) Retry() tea.Cmd {
	s.busy = true
	s.list.Title = "Loading notes"
	return s.list.StartSpinner()
}

func (s *noteScreen) start() tea.Cmd {
	s.stopStream()
	s.notes = nil
	s.nextResult = nil
	s.exhausted = false
	s.loading = false
	return func() tea.Msg {
		selection, err := s.source.SearchNotes(s.ctx, s.options.Query)
		if err != nil {
			return noteStreamStartedMsg{err: err}
		}
		// #nosec G118 -- cancel is transferred through noteStreamStartedMsg and
		// called by stopStream when the stream is replaced or the screen exits.
		streamCtx, cancel := context.WithCancel(s.ctx)
		sequence := s.source.Notes(
			streamCtx,
			selection,
			ankitts.NoteLoadOptions{BatchSize: interactiveNoteWindowSize},
		)
		next, stop := iter.Pull(sequence)
		return noteStreamStartedMsg{
			next: next, stop: stop, cancel: cancel,
		}
	}
}

func (s *noteScreen) loadWindow() tea.Cmd {
	return func() tea.Msg {
		results := make([]ankitts.NoteResult, 0, interactiveNoteWindowSize)
		for range interactiveNoteWindowSize {
			result, ok := s.nextResult()
			if !ok {
				return notesLoadedMsg{results: results, done: true}
			}
			results = append(results, result)
			if result.Err != nil {
				return notesLoadedMsg{results: results, done: true}
			}
		}
		return notesLoadedMsg{results: results}
	}
}

func (s *noteScreen) refresh() tea.Cmd {
	id := s.preferredNoteID
	return func() tea.Msg {
		for result := range s.source.Notes(
			s.ctx,
			ankitts.NoteSelection{IDs: []int64{id}},
			ankitts.NoteLoadOptions{BatchSize: 1},
		) {
			return noteRefreshedMsg{note: result.Note, err: result.Err}
		}
		return noteRefreshedMsg{err: fmt.Errorf("refresh note %d: no result", id)}
	}
}

func (s *noteScreen) selectPreferred() {
	if s.preferredNoteID == 0 {
		return
	}
	for index, note := range s.notes {
		if note.ID == s.preferredNoteID {
			s.list.Select(index)
			break
		}
	}
	s.preferredNoteID = 0
}

func (s *noteScreen) stopStream() {
	if s.cancelStream != nil {
		s.cancelStream()
		s.cancelStream = nil
	}
	if s.stopIterator != nil {
		s.stopIterator()
		s.stopIterator = nil
	}
	s.nextResult = nil
}

func noteListItems(notes []anki.Note, options noteOptions) []list.Item {
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
	field, ok := fields[0].(listItem)
	if !ok {
		return ""
	}
	value := field.description
	return strings.ReplaceAll(value, "\n", " ")
}
