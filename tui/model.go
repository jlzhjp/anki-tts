// Package tui implements the interactive anki-tts terminal application.
package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/textutil"
	"jlzhjp.dev/anki-tts/tts"
	"jlzhjp.dev/anki-tts/workflow"
)

// Workflow contains the application operations used by the TUI.
type Workflow interface {
	ListDecks(context.Context) ([]string, error)
	SelectNotes(context.Context, workflow.NoteSelector) ([]anki.Note, error)
	Services() []tts.NamedService
	TransformsAudio() bool
	Plan(workflow.GenerationSpec) (workflow.Plan, error)
	Execute(context.Context, workflow.Plan, workflow.PipelineOptions) (workflow.BatchResult, error)
}

// Options constrains the interactive workflow and preselects generation values.
type Options struct {
	Selector             workflow.NoteSelector
	FromField            string
	ToField              string
	Service              string
	Yes                  bool
	SynthesisConcurrency int
	AudioConcurrency     int
}

type screenKind uint8

const (
	deckScreen screenKind = iota
	noteScreen
	sourceScreen
	destinationScreen
	actionScreen
	serviceScreen
)

// Model is the Bubble Tea application coordinator.
type Model struct {
	ctx      context.Context
	workflow Workflow
	screens  []screenModel
	failure  *errorModel
	width    int
	height   int
	status   string
	options  Options

	deck             string
	note             anki.Note
	sourceField      string
	destinationField string
	service          tts.NamedService
}

// New creates a TUI model around an injected workflow.
func New(ctx context.Context, appWorkflow Workflow) Model {
	return NewWithOptions(ctx, appWorkflow, Options{})
}

// NewWithOptions creates a TUI model with CLI-provided constraints.
func NewWithOptions(ctx context.Context, appWorkflow Workflow, options Options) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := Model{ctx: ctx, workflow: appWorkflow, options: options, width: 80, height: 24}
	if appWorkflow == nil {
		m.failure = newErrorModel(errors.New("TUI workflow is not configured"), nil)
		return m
	}
	if len(options.Selector.Decks) == 1 {
		m.deck = options.Selector.Decks[0]
		m.screens = []screenModel{newNoteModel(ctx, appWorkflow, m.deck, options)}
	} else {
		m.screens = []screenModel{newDeckModel(ctx, appWorkflow, options.Selector.Decks)}
	}
	m.resizeScreens()
	if len(appWorkflow.Services()) == 0 {
		m.failure = newErrorModel(errors.New("no TTS services are configured; add an [openrouter] table to config.toml"), nil)
	}
	return m
}

// Init starts the active screen.
func (m Model) Init() tea.Cmd {
	if m.failure != nil {
		return nil
	}
	return m.active().Init()
}

// Update coordinates global input, screen transitions, and child updates.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeScreens()
		return m, nil
	case retryMsg:
		m.failure = nil
		var spinner tea.Cmd
		if retryable, ok := m.active().(retryableScreen); ok {
			spinner = retryable.retrying()
		}
		return m, tea.Batch(spinner, msg.cmd)
	case dismissErrorMsg:
		m.failure = nil
		if len(m.screens) == 1 {
			return m, m.active().Init()
		}
		return m, nil
	case screenFailedMsg:
		m.failure = newErrorModel(msg.err, msg.retry)
		m.resizeScreens()
		return m, nil
	case deckSelectedMsg:
		m.deck = msg.deck
		m.clearAfter(deckScreen)
		return m.push(newNoteModel(m.ctx, m.workflow, msg.deck, m.options))
	case noteSelectedMsg:
		m.note = msg.note
		m.clearAfter(noteScreen)
		if m.options.FromField != "" {
			m.sourceField = m.options.FromField
			return m.afterSourceSelected()
		}
		fields := fieldItems(msg.note, true)
		if len(fields) == 0 {
			m.failure = newErrorModel(errors.New("this note has no non-empty source fields"), nil)
			return m, nil
		}
		return m.push(newFieldModel(sourceScreen, "Select the source field", fields))
	case sourceSelectedMsg:
		m.sourceField = msg.field
		m.clearAfter(sourceScreen)
		return m.afterSourceSelected()
	case destinationSelectedMsg:
		m.destinationField = msg.field
		m.clearAfter(destinationScreen)
		return m.afterDestinationSelected()
	case actionSelectedMsg:
		if !msg.confirmed {
			m.service = tts.NamedService{}
			return m.pop()
		}
		m.clearAfter(actionScreen)
		return m.afterOverwriteConfirmed()
	case serviceSelectedMsg:
		m.service = msg.service
		request := m.generationSpec(msg.service)
		if serviceModel, ok := m.active().(*serviceModel); ok {
			spinner := serviceModel.startGeneration(msg.service.Name, m.hasTransformerContext())
			return m, tea.Batch(spinner, m.generateCmd(request))
		}
		return m, nil
	case generatedMsg:
		if serviceModel, ok := m.active().(*serviceModel); ok {
			serviceModel.stopGeneration()
		}
		if msg.err != nil {
			m.failure = newErrorModel(msg.err, m.generateCmd(msg.request))
			m.resizeScreens()
			return m, nil
		}
		m.status = saveStatus(msg.result, m.destinationField)
		return m.returnToNotesAndRefresh()
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		active := m.active()
		if active == nil {
			return m, nil
		}
		if !active.filtering() {
			switch msg.String() {
			case "q":
				return m, tea.Quit
			case "esc":
				if m.failure == nil {
					return m.pop()
				}
			}
		}
	}

	active := m.active()
	if active == nil {
		return m, nil
	}
	updated, cmd := active.Update(message)
	if m.failure != nil {
		m.failure = updated.(*errorModel)
	} else {
		m.screens[len(m.screens)-1] = updated.(screenModel)
	}
	return m, cmd
}

// View renders application context above the active child screen.
func (m Model) View() tea.View {
	active := m.active()
	if active == nil {
		return tea.NewView("Anki TTS\n")
	}
	header := "Anki TTS"
	if m.failure == nil {
		header += fmt.Sprintf(" · Step %d/6", int(active.kind())+1)
	}
	if context := m.contextLine(); context != "" {
		header += "\n" + context
	}
	return tea.NewView(header + "\n\n" + active.View().Content)
}

func (m Model) active() screenModel {
	if m.failure != nil {
		return m.failure
	}
	if len(m.screens) == 0 {
		return nil
	}
	return m.screens[len(m.screens)-1]
}

func (m Model) push(screen screenModel) (tea.Model, tea.Cmd) {
	m.screens = append(m.screens, screen)
	m.resizeScreens()
	return m, screen.Init()
}

func (m Model) pop() (tea.Model, tea.Cmd) {
	if len(m.screens) <= 1 {
		return m, tea.Quit
	}
	popped := m.screens[len(m.screens)-1].kind()
	m.screens = m.screens[:len(m.screens)-1]
	m.clearSelectionFrom(popped)
	m.resizeScreens()
	return m, nil
}

func (m *Model) clearSelectionFrom(kind screenKind) {
	switch kind {
	case noteScreen:
		m.note = anki.Note{}
		fallthrough
	case sourceScreen:
		m.sourceField = ""
		fallthrough
	case destinationScreen:
		m.destinationField = ""
		fallthrough
	case actionScreen, serviceScreen:
		m.service = tts.NamedService{}
	}
}

func (m *Model) clearAfter(kind screenKind) {
	for len(m.screens) > 0 && m.screens[len(m.screens)-1].kind() > kind {
		m.screens = m.screens[:len(m.screens)-1]
	}
	switch kind {
	case deckScreen:
		m.note, m.sourceField, m.destinationField, m.service = anki.Note{}, "", "", tts.NamedService{}
	case noteScreen:
		m.sourceField, m.destinationField, m.service = "", "", tts.NamedService{}
	case sourceScreen:
		m.destinationField, m.service = "", tts.NamedService{}
	case destinationScreen:
		m.service = tts.NamedService{}
	case actionScreen:
		m.service = tts.NamedService{}
	}
}

func (m *Model) resizeScreens() {
	headerHeight := 3
	if m.contextLine() != "" {
		headerHeight++
	}
	childHeight := max(1, m.height-headerHeight)
	for _, screen := range m.screens {
		screen.setSize(m.width, childHeight)
	}
	if m.failure != nil {
		m.failure.setSize(m.width, childHeight)
	}
}

func (m Model) contextLine() string {
	parts := make([]string, 0, 6)
	if m.deck != "" {
		parts = append(parts, "Deck: "+m.deck)
	}
	if m.note.ID != 0 {
		parts = append(parts, fmt.Sprintf("Note: %d", m.note.ID))
	}
	if m.sourceField != "" {
		parts = append(parts, "Source: "+m.sourceField)
	}
	if m.destinationField != "" {
		parts = append(parts, "Destination: "+m.destinationField)
	}
	if m.service.Name != "" {
		parts = append(parts, "Service: "+m.service.Name)
	}
	return strings.Join(parts, " · ")
}

func (m Model) generationSpec(service tts.NamedService) workflow.GenerationSpec {
	return workflow.GenerationSpec{
		Notes:            []anki.Note{m.note},
		SourceField:      m.sourceField,
		DestinationField: m.destinationField,
		Service:          service,
	}
}

func (m Model) afterSourceSelected() (tea.Model, tea.Cmd) {
	if m.options.ToField != "" {
		m.destinationField = m.options.ToField
		return m.afterDestinationSelected()
	}
	return m.push(newFieldModel(destinationScreen, "Select the destination field", fieldItems(m.note, false)))
}

func (m Model) afterDestinationSelected() (tea.Model, tea.Cmd) {
	field, ok := m.note.Fields[m.destinationField]
	if !ok {
		m.failure = newErrorModel(fmt.Errorf("note %d has no field %q", m.note.ID, m.destinationField), nil)
		return m, nil
	}
	if strings.TrimSpace(field.Value) != "" && !m.options.Yes {
		return m.push(newActionModel())
	}
	return m.afterOverwriteConfirmed()
}

func (m Model) afterOverwriteConfirmed() (tea.Model, tea.Cmd) {
	services := m.workflow.Services()
	if m.options.Service != "" {
		for _, service := range services {
			if service.Name == m.options.Service {
				m.service = service
				return m, m.generateCmd(m.generationSpec(service))
			}
		}
		m.failure = newErrorModel(fmt.Errorf("TTS service %q is not configured", m.options.Service), nil)
		return m, nil
	}
	if len(services) == 0 {
		m.failure = newErrorModel(errors.New("no TTS services are configured"), nil)
		return m, nil
	}
	return m.push(newServiceModel(services))
}

func (m Model) generateCmd(request workflow.GenerationSpec) tea.Cmd {
	return func() tea.Msg {
		plan, err := m.workflow.Plan(request)
		if err != nil {
			return generatedMsg{request: request, err: err}
		}
		synthesisConcurrency := m.options.SynthesisConcurrency
		if synthesisConcurrency <= 0 {
			synthesisConcurrency = workflow.DefaultSynthesisConcurrency
		}
		audioConcurrency := m.options.AudioConcurrency
		if audioConcurrency <= 0 {
			audioConcurrency = workflow.DefaultAudioConcurrency
		}
		batch, err := m.workflow.Execute(m.ctx, plan, workflow.PipelineOptions{
			SynthesisConcurrency: synthesisConcurrency,
			AudioConcurrency:     audioConcurrency,
		})
		if err != nil {
			return generatedMsg{request: request, err: err}
		}
		if len(batch.Items) != 1 {
			return generatedMsg{request: request, err: fmt.Errorf("generation pipeline returned %d results, want 1", len(batch.Items))}
		}
		item := batch.Items[0]
		return generatedMsg{request: request, result: item.Result, err: item.Err}
	}
}

func (m Model) returnToNotesAndRefresh() (tea.Model, tea.Cmd) {
	noteIndex := -1
	for index, screen := range m.screens {
		if screen.kind() == noteScreen {
			noteIndex = index
			break
		}
	}
	if noteIndex < 0 {
		return m, nil
	}
	m.screens = m.screens[:noteIndex+1]
	notes := m.screens[noteIndex].(*noteModel)
	cmd := notes.refresh(m.status, m.note.ID)
	m.status = ""
	m.note = anki.Note{}
	m.sourceField = ""
	m.destinationField = ""
	m.service = tts.NamedService{}
	m.resizeScreens()
	return m, cmd
}

// The transformation choice is represented by the workflow implementation;
// the coordinator only needs a stable loading title.
func (m Model) hasTransformerContext() bool { return m.workflow.TransformsAudio() }

func saveStatus(result workflow.GenerateResult, destination string) string {
	var costStatus string
	if result.Cost != nil {
		costStatus = fmt.Sprintf("Cost: $%.6f · ", *result.Cost)
	} else if result.CostErr != nil {
		costStatus = fmt.Sprintf("Cost unavailable: %v · ", result.CostErr)
	}
	return costStatus + fmt.Sprintf("Saved %s to %s", result.Filename, destination)
}

type screenModel interface {
	tea.Model
	kind() screenKind
	setSize(int, int)
	filtering() bool
}

type retryableScreen interface {
	retrying() tea.Cmd
}

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

type deckModel struct {
	selectionModel
	ctx      context.Context
	workflow Workflow
	decks    []string
}

func newDeckModel(ctx context.Context, appWorkflow Workflow, decks []string) *deckModel {
	m := &deckModel{selectionModel: newSelectionModel(deckScreen, "Anki TTS — loading decks", nil), ctx: ctx, workflow: appWorkflow, decks: decks}
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
		decks, err := m.workflow.ListDecks(m.ctx)
		return decksLoadedMsg{decks: decks, err: err}
	}
}

func (m *deckModel) retrying() tea.Cmd {
	m.busy = true
	m.list.Title = "Loading decks"
	return m.list.StartSpinner()
}

type noteModel struct {
	selectionModel
	ctx             context.Context
	workflow        Workflow
	deck            string
	status          string
	preferredNoteID int64
	options         Options
}

func newNoteModel(ctx context.Context, appWorkflow Workflow, deck string, options Options) *noteModel {
	m := &noteModel{selectionModel: newSelectionModel(noteScreen, "Loading notes — "+deck, nil), ctx: ctx, workflow: appWorkflow, deck: deck, options: options}
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
		notes, err := m.workflow.SelectNotes(m.ctx, selector)
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

type fieldModel struct{ selectionModel }

func newFieldModel(kind screenKind, title string, items []list.Item) *fieldModel {
	return &fieldModel{selectionModel: newSelectionModel(kind, title, items)}
}

func (m *fieldModel) Init() tea.Cmd { return nil }
func (m *fieldModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "enter" && !m.filtering() {
		if selected, ok := m.selected(); ok {
			if m.kindValue == sourceScreen {
				return m, messageCmd(sourceSelectedMsg{field: selected.value.(string)})
			}
			return m, messageCmd(destinationSelectedMsg{field: selected.value.(string)})
		}
	}
	return m, m.update(message)
}

type actionModel struct{ selectionModel }

func newActionModel() *actionModel {
	return &actionModel{selectionModel: newSelectionModel(actionScreen, "Destination is not empty — replace it?", actionItems())}
}

func (m *actionModel) Init() tea.Cmd { return nil }
func (m *actionModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "enter" && !m.filtering() {
		if selected, ok := m.selected(); ok {
			return m, messageCmd(actionSelectedMsg{confirmed: selected.value.(bool)})
		}
	}
	return m, m.update(message)
}

type serviceModel struct{ selectionModel }

func newServiceModel(services []tts.NamedService) *serviceModel {
	return &serviceModel{selectionModel: newSelectionModel(serviceScreen, "Select a TTS service", serviceItems(services))}
}

func (m *serviceModel) Init() tea.Cmd { return nil }
func (m *serviceModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(tea.KeyPressMsg); ok && msg.String() == "enter" && !m.busy && !m.filtering() {
		if selected, ok := m.selected(); ok {
			return m, messageCmd(serviceSelectedMsg{service: selected.value.(tts.NamedService)})
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

type noteCandidate struct {
	note    anki.Note
	invalid string
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
	sorted := append([]string(nil), decks...)
	sort.Strings(sorted)
	items := make([]list.Item, 0, len(sorted))
	for _, deck := range sorted {
		items = append(items, item{title: deck, value: deck})
	}
	return items
}

func noteItems(notes []anki.Note, options Options) []list.Item {
	items := make([]list.Item, 0, len(notes))
	for _, note := range notes {
		title := firstFieldValue(note)
		if title == "" {
			title = "(empty note)"
		}
		candidate := noteCandidate{note: note}
		if options.FromField != "" {
			field, ok := note.Fields[options.FromField]
			if !ok {
				candidate.invalid = fmt.Sprintf("note %d is missing source field %q", note.ID, options.FromField)
			} else {
				text, _ := textutil.FromHTML(field.Value)
				if strings.TrimSpace(text) == "" {
					candidate.invalid = fmt.Sprintf("note %d has an empty source field %q", note.ID, options.FromField)
				}
			}
		}
		if candidate.invalid == "" && options.ToField != "" {
			if _, ok := note.Fields[options.ToField]; !ok {
				candidate.invalid = fmt.Sprintf("note %d is missing destination field %q", note.ID, options.ToField)
			}
		}
		description := fmt.Sprintf("%s · note %d", note.ModelName, note.ID)
		if candidate.invalid != "" {
			description += " · DISABLED: " + candidate.invalid
		}
		items = append(items, item{title: title, description: description, value: candidate})
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
		item{title: "Replace", description: "Replace the non-empty destination field", value: true},
		item{title: "Cancel", description: "Return without generating audio", value: false},
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
	return strings.ReplaceAll(value, "\n", " ")
}

type decksLoadedMsg struct {
	decks []string
	err   error
}
type notesLoadedMsg struct {
	notes []anki.Note
	err   error
}
type deckSelectedMsg struct{ deck string }
type noteSelectedMsg struct{ note anki.Note }
type sourceSelectedMsg struct{ field string }
type destinationSelectedMsg struct{ field string }
type actionSelectedMsg struct{ confirmed bool }
type serviceSelectedMsg struct{ service tts.NamedService }
type generatedMsg struct {
	request workflow.GenerationSpec
	result  workflow.GenerateResult
	err     error
}
type screenFailedMsg struct {
	err   error
	retry tea.Cmd
}
type retryMsg struct{ cmd tea.Cmd }
type dismissErrorMsg struct{}

func messageCmd(message tea.Msg) tea.Cmd { return func() tea.Msg { return message } }
func failCmd(err error, retry tea.Cmd) tea.Cmd {
	return messageCmd(screenFailedMsg{err: err, retry: retry})
}
