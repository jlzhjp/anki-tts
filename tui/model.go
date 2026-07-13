// Package tui implements the interactive anki-tts terminal application.
package tui

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/textutil"
	"jlzhjp.dev/anki-tts/tts"
)

// AnkiClient contains the Anki operations used by the TUI.
type AnkiClient interface {
	ListDecks(context.Context) ([]string, error)
	ListNotes(context.Context, string) ([]anki.Note, error)
	StoreMediaFile(context.Context, string, []byte) (string, error)
	UpdateNote(context.Context, anki.NoteUpdate) error
}

type screen uint8

const (
	deckScreen screen = iota
	noteScreen
	sourceScreen
	destinationScreen
	actionScreen
	serviceScreen
	errorScreen
)

const maxFinalAudioSize = 32 << 20 // 32 MiB

type destinationAction string

const (
	overrideAction destinationAction = "Override"
	appendAction   destinationAction = "Append"
	cancelAction   destinationAction = "Cancel"
)

// Model is the Bubble Tea application model.
type Model struct {
	ctx         context.Context
	anki        AnkiClient
	services    *tts.Container
	transformer tts.Transformer
	list        list.Model
	initialCmd  tea.Cmd
	screen      screen
	backScreen  screen
	width       int
	height      int
	busy        bool
	err         error
	retry       tea.Cmd
	status      string

	deck              string
	notes             []anki.Note
	note              anki.Note
	sourceField       string
	destinationField  string
	destinationAction destinationAction
}

// New creates a TUI model.
func New(ctx context.Context, client AnkiClient, services *tts.Container, transformer tts.Transformer) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	if services == nil {
		services = tts.NewContainer()
	}
	l := list.New(nil, list.NewDefaultDelegate(), 80, 24)
	l.Title = "Anki TTS — loading decks"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	spinnerCmd := l.StartSpinner()
	model := Model{ctx: ctx, anki: client, services: services, transformer: transformer, list: l, initialCmd: spinnerCmd, screen: deckScreen, width: 80, height: 24, busy: true}
	if len(services.Services()) == 0 {
		model.list.StopSpinner()
		model.screen = errorScreen
		model.backScreen = deckScreen
		model.busy = false
		model.err = errors.New("no TTS services are configured; add an [openrouter] table to config.toml")
	}
	return model
}

// Init loads decks from AnkiConnect.
func (m Model) Init() tea.Cmd {
	if m.screen == errorScreen {
		return nil
	}
	return tea.Batch(m.initialCmd, m.loadDecksCmd())
}

// Update handles Bubble Tea messages.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, msg.Height)
		return m, nil
	case decksMsg:
		m.list.StopSpinner()
		if msg.err != nil {
			return m.fail(msg.err, deckScreen, m.loadDecksCmd()), nil
		}
		m.busy, m.err, m.screen = false, nil, deckScreen
		m.setList("Select a deck", deckItems(msg.decks))
		return m, nil
	case notesMsg:
		m.list.StopSpinner()
		if msg.err != nil {
			return m.fail(msg.err, noteScreen, m.loadNotesCmd()), nil
		}
		m.busy, m.err, m.screen, m.notes = false, nil, noteScreen, msg.notes
		m.setList("Select a note — "+m.deck, noteItems(msg.notes))
		return m, nil
	case savedMsg:
		m.list.StopSpinner()
		if msg.err != nil {
			return m.fail(msg.err, serviceScreen, m.generateCmd(msg.service)), nil
		}
		m.status = fmt.Sprintf("Saved %s to %s", msg.filename, m.destinationField)
		m.busy = true
		spinnerCmd := m.list.StartSpinner()
		return m, tea.Batch(spinnerCmd, m.loadNotesCmd())
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" || (msg.String() == "q" && m.list.FilterState() != list.Filtering) {
			return m, tea.Quit
		}
		if m.screen == errorScreen {
			switch msg.String() {
			case "enter":
				if m.retry == nil {
					return m, nil
				}
				m.busy, m.err, m.screen = true, nil, m.backScreen
				spinnerCmd := m.list.StartSpinner()
				return m, tea.Batch(spinnerCmd, m.retry)
			case "esc":
				m.err, m.screen = nil, m.backScreen
				return m, nil
			}
		}
		if !m.busy && m.list.FilterState() != list.Filtering {
			switch msg.String() {
			case "esc":
				return m.goBack()
			case "enter":
				return m.selectCurrent()
			}
		}
	}

	if m.screen != errorScreen {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(message)
		return m, cmd
	}
	return m, nil
}

// View renders the current application state.
func (m Model) View() tea.View {
	if m.screen == errorScreen {
		help := "Esc: back  q: quit"
		if m.retry != nil {
			help = "Enter: retry  " + help
		}
		return tea.NewView(fmt.Sprintf("Anki TTS\n\nError: %v\n\n%s\n", m.err, help))
	}
	if m.busy {
		return tea.NewView(m.list.View())
	}
	content := m.list.View()
	if m.status != "" {
		content = m.status + "\n\n" + content
	}
	return tea.NewView(content)
}

func (m Model) selectCurrent() (tea.Model, tea.Cmd) {
	item, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, nil
	}
	switch m.screen {
	case deckScreen:
		m.deck, m.status, m.busy = item.value.(string), "", true
		spinnerCmd := m.list.StartSpinner()
		m.list.Title = "Loading notes — " + m.deck
		return m, tea.Batch(spinnerCmd, m.loadNotesCmd())
	case noteScreen:
		m.note = item.value.(anki.Note)
		fields := fieldItems(m.note, true)
		if len(fields) == 0 {
			return m.fail(errors.New("this note has no non-empty source fields"), noteScreen, nil), nil
		}
		m.screen = sourceScreen
		m.setList("Select the source field", fields)
	case sourceScreen:
		m.sourceField = item.value.(string)
		m.screen = destinationScreen
		m.setList("Select the destination field", fieldItems(m.note, false))
	case destinationScreen:
		m.destinationField = item.value.(string)
		m.screen = actionScreen
		m.setList("Destination field behavior", actionItems())
	case actionScreen:
		m.destinationAction = item.value.(destinationAction)
		if m.destinationAction == cancelAction {
			m.screen = destinationScreen
			m.setList("Select the destination field", fieldItems(m.note, false))
			return m, nil
		}
		services := serviceItems(m.services.Services())
		if len(services) == 0 {
			return m.fail(errors.New("no TTS services are configured"), actionScreen, nil), nil
		}
		m.screen = serviceScreen
		m.setList("Select a TTS service", services)
	case serviceScreen:
		service := item.value.(tts.NamedService)
		m.busy = true
		if m.transformer == nil {
			m.list.Title = "Generating voice with " + service.Name
		} else {
			m.list.Title = "Generating and transforming audio with " + service.Name
		}
		spinnerCmd := m.list.StartSpinner()
		return m, tea.Batch(spinnerCmd, m.generateCmd(service))
	}
	return m, nil
}

func (m Model) goBack() (tea.Model, tea.Cmd) {
	switch m.screen {
	case noteScreen:
		m.screen, m.busy = deckScreen, true
		m.list.Title = "Loading decks"
		spinnerCmd := m.list.StartSpinner()
		return m, tea.Batch(spinnerCmd, m.loadDecksCmd())
	case sourceScreen:
		m.screen = noteScreen
		m.setList("Select a note — "+m.deck, noteItems(m.notes))
	case destinationScreen:
		m.screen = sourceScreen
		m.setList("Select the source field", fieldItems(m.note, true))
	case actionScreen:
		m.screen = destinationScreen
		m.setList("Select the destination field", fieldItems(m.note, false))
	case serviceScreen:
		m.screen = actionScreen
		m.setList("Destination field behavior", actionItems())
	default:
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) setList(title string, items []list.Item) {
	m.list.Title = title
	m.list.SetItems(items)
	m.list.ResetSelected()
}

func (m Model) fail(err error, back screen, retry tea.Cmd) Model {
	m.list.StopSpinner()
	m.busy, m.err, m.backScreen, m.retry, m.screen = false, err, back, retry, errorScreen
	return m
}

type decksMsg struct {
	decks []string
	err   error
}

type notesMsg struct {
	notes []anki.Note
	err   error
}

type savedMsg struct {
	service  tts.NamedService
	filename string
	err      error
}

func (m Model) loadDecksCmd() tea.Cmd {
	return func() tea.Msg {
		decks, err := m.anki.ListDecks(m.ctx)
		return decksMsg{decks: decks, err: err}
	}
}

func (m Model) loadNotesCmd() tea.Cmd {
	return func() tea.Msg {
		notes, err := m.anki.ListNotes(m.ctx, m.deck)
		return notesMsg{notes: notes, err: err}
	}
}

func (m Model) generateCmd(service tts.NamedService) tea.Cmd {
	return func() tea.Msg {
		text, err := textutil.FromHTML(m.note.Fields[m.sourceField].Value)
		if err != nil {
			return savedMsg{service: service, err: fmt.Errorf("prepare source field: %w", err)}
		}
		if strings.TrimSpace(text) == "" {
			return savedMsg{service: service, err: errors.New("source field contains no speakable text")}
		}
		voice, err := service.Service.Generate(m.ctx, tts.Input{Text: text})
		if err != nil {
			return savedMsg{service: service, err: err}
		}
		audio := voice.Audio
		if audio.Data == nil {
			return savedMsg{service: service, err: errors.New("TTS service returned no audio data")}
		}
		if m.transformer != nil {
			input := audio
			audio, err = m.transformer.Transform(m.ctx, input)
			if err != nil {
				_ = input.Data.Close()
				return savedMsg{service: service, err: err}
			}
		}
		if audio.Data == nil {
			return savedMsg{service: service, err: errors.New("audio pipeline returned no data stream")}
		}
		defer audio.Data.Close()
		format := safeFormat(audio.Format)
		if format == "" {
			return savedMsg{service: service, err: fmt.Errorf("audio pipeline returned invalid format %q", audio.Format)}
		}
		data, err := io.ReadAll(io.LimitReader(audio.Data, maxFinalAudioSize+1))
		if err != nil {
			return savedMsg{service: service, err: fmt.Errorf("read final audio: %w", err)}
		}
		if len(data) == 0 {
			return savedMsg{service: service, err: errors.New("audio pipeline returned empty data")}
		}
		if len(data) > maxFinalAudioSize {
			return savedMsg{service: service, err: fmt.Errorf("final audio exceeds %d bytes", maxFinalAudioSize)}
		}
		hash := sha256.Sum256(data)
		filename := fmt.Sprintf("anki-tts-%d-%x.%s", m.note.ID, hash[:6], format)
		storedFilename, err := m.anki.StoreMediaFile(m.ctx, filename, data)
		if err != nil {
			return savedMsg{service: service, err: err}
		}
		tag := "[sound:" + storedFilename + "]"
		value := tag
		if m.destinationAction == appendAction && m.note.Fields[m.destinationField].Value != "" {
			value = m.note.Fields[m.destinationField].Value + "<br>" + tag
		}
		err = m.anki.UpdateNote(m.ctx, anki.NoteUpdate{ID: m.note.ID, Fields: map[string]string{m.destinationField: value}})
		if err != nil {
			err = fmt.Errorf("media %q was stored but the note update failed: %w", storedFilename, err)
		}
		return savedMsg{service: service, filename: storedFilename, err: err}
	}
}

type item struct {
	title       string
	description string
	value       any
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.description }
func (i item) FilterValue() string { return i.title + " " + i.description }

func deckItems(decks []string) []list.Item {
	sort.Strings(decks)
	items := make([]list.Item, 0, len(decks))
	for _, deck := range decks {
		items = append(items, item{title: deck, value: deck})
	}
	return items
}

func noteItems(notes []anki.Note) []list.Item {
	items := make([]list.Item, 0, len(notes))
	for _, note := range notes {
		title := firstFieldValue(note)
		if title == "" {
			title = "(empty note)"
		}
		items = append(items, item{title: title, description: fmt.Sprintf("%s · note %d", note.ModelName, note.ID), value: note})
	}
	return items
}

func fieldItems(note anki.Note, nonEmpty bool) []list.Item {
	type namedField struct {
		name  string
		field anki.Field
	}
	fields := make([]namedField, 0, len(note.Fields))
	for name, field := range note.Fields {
		if nonEmpty && strings.TrimSpace(field.Value) == "" {
			continue
		}
		fields = append(fields, namedField{name: name, field: field})
	}
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].field.Order == fields[j].field.Order {
			return fields[i].name < fields[j].name
		}
		return fields[i].field.Order < fields[j].field.Order
	})
	items := make([]list.Item, 0, len(fields))
	for _, field := range fields {
		preview, _ := textutil.FromHTML(field.field.Value)
		items = append(items, item{title: field.name, description: preview, value: field.name})
	}
	return items
}

func actionItems() []list.Item {
	return []list.Item{
		item{title: string(overrideAction), description: "Replace the destination field", value: overrideAction},
		item{title: string(appendAction), description: "Keep existing content and append audio", value: appendAction},
		item{title: string(cancelAction), description: "Return without generating audio", value: cancelAction},
	}
}

func serviceItems(services []tts.NamedService) []list.Item {
	items := make([]list.Item, 0, len(services))
	for _, service := range services {
		items = append(items, item{title: service.Name, description: "Generate voice", value: service})
	}
	return items
}

func firstFieldValue(note anki.Note) string {
	fields := fieldItems(note, true)
	if len(fields) == 0 {
		return ""
	}
	value := fields[0].(item).description
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func safeFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return ""
	}
	for _, r := range format {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ""
		}
	}
	return format
}
