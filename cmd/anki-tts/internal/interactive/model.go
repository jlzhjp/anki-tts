package interactive

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

type screenKind uint8

const (
	deckScreen screenKind = iota
	noteScreen
	sourceScreen
	destinationScreen
	actionScreen
	serviceScreen
)

// interactiveModel is the Bubble Tea application coordinator.
type interactiveModel struct {
	ctx     context.Context
	app     Application
	screens []screenModel
	failure *errorModel
	width   int
	height  int
	status  string
	options Options

	deck             string
	note             anki.Note
	sourceField      string
	destinationField string
	service          string
}

// newInteractive creates an interactive model around an injected app.
func newInteractive(ctx context.Context, app Application) interactiveModel {
	return newInteractiveModel(ctx, app, Options{})
}

// newInteractiveModel creates a TUI model with CLI-provided constraints.
func newInteractiveModel(ctx context.Context, app Application, options Options) interactiveModel {
	if ctx == nil {
		ctx = context.Background()
	}
	m := interactiveModel{ctx: ctx, app: app, options: options, width: 80, height: 24}
	if app == nil {
		m.failure = newErrorModel(errors.New("interactive app is not configured"), nil)
		return m
	}
	if len(options.Selector.Decks) == 1 {
		m.deck = options.Selector.Decks[0]
		m.screens = []screenModel{newNoteModel(ctx, app, m.deck, options)}
	} else {
		m.screens = []screenModel{newDeckModel(ctx, app, options.Selector.Decks)}
	}
	m.resizeScreens()
	if len(app.ServiceNames()) == 0 {
		m.failure = newErrorModel(errors.New("no TTS services are configured; add an [openrouter] table to config.toml"), nil)
	}
	return m
}

// Init starts the active screen.
func (m interactiveModel) Init() tea.Cmd {
	if m.failure != nil {
		return nil
	}
	return m.active().Init()
}

// Update coordinates global input, screen transitions, and child updates.
func (m interactiveModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
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
		return m.push(newNoteModel(m.ctx, m.app, msg.deck, m.options))
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
			m.service = ""
			return m.pop()
		}
		m.clearAfter(actionScreen)
		return m.afterOverwriteConfirmed()
	case serviceSelectedMsg:
		m.service = msg.service
		request := m.generationSpec(msg.service)
		if serviceModel, ok := m.active().(*serviceModel); ok {
			spinner := serviceModel.startGeneration(msg.service, m.hasTransformerContext())
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
func (m interactiveModel) View() tea.View {
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

func (m interactiveModel) active() screenModel {
	if m.failure != nil {
		return m.failure
	}
	if len(m.screens) == 0 {
		return nil
	}
	return m.screens[len(m.screens)-1]
}

func (m interactiveModel) push(screen screenModel) (tea.Model, tea.Cmd) {
	m.screens = append(m.screens, screen)
	m.resizeScreens()
	return m, screen.Init()
}

func (m interactiveModel) pop() (tea.Model, tea.Cmd) {
	if len(m.screens) <= 1 {
		return m, tea.Quit
	}
	popped := m.screens[len(m.screens)-1].kind()
	m.screens = m.screens[:len(m.screens)-1]
	m.clearSelectionFrom(popped)
	m.resizeScreens()
	return m, nil
}

func (m *interactiveModel) clearSelectionFrom(kind screenKind) {
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
		m.service = ""
	}
}

func (m *interactiveModel) clearAfter(kind screenKind) {
	for len(m.screens) > 0 && m.screens[len(m.screens)-1].kind() > kind {
		m.screens = m.screens[:len(m.screens)-1]
	}
	switch kind {
	case deckScreen:
		m.note, m.sourceField, m.destinationField, m.service = anki.Note{}, "", "", ""
	case noteScreen:
		m.sourceField, m.destinationField, m.service = "", "", ""
	case sourceScreen:
		m.destinationField, m.service = "", ""
	case destinationScreen:
		m.service = ""
	case actionScreen:
		m.service = ""
	}
}

func (m *interactiveModel) resizeScreens() {
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

func (m interactiveModel) contextLine() string {
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
	if m.service != "" {
		parts = append(parts, "Service: "+m.service)
	}
	return strings.Join(parts, " · ")
}

func (m interactiveModel) generationSpec(service string) ankitts.GenerationRequest {
	return ankitts.GenerationRequest{
		Notes:            []anki.Note{m.note},
		SourceField:      m.sourceField,
		DestinationField: m.destinationField,
		Service:          service,
	}
}

func (m interactiveModel) afterSourceSelected() (tea.Model, tea.Cmd) {
	if m.options.ToField != "" {
		m.destinationField = m.options.ToField
		return m.afterDestinationSelected()
	}
	return m.push(newFieldModel(destinationScreen, "Select the destination field", fieldItems(m.note, false)))
}

func (m interactiveModel) afterDestinationSelected() (tea.Model, tea.Cmd) {
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

func (m interactiveModel) afterOverwriteConfirmed() (tea.Model, tea.Cmd) {
	services := m.app.ServiceNames()
	if m.options.Service != "" {
		for _, service := range services {
			if service == m.options.Service {
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

func (m interactiveModel) generateCmd(request ankitts.GenerationRequest) tea.Cmd {
	return func() tea.Msg {
		plan, err := m.app.Prepare(request)
		if err != nil {
			return generatedMsg{request: request, err: err}
		}
		batch, err := m.app.Execute(m.ctx, plan, ankitts.ExecuteOptions{})
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

func (m interactiveModel) returnToNotesAndRefresh() (tea.Model, tea.Cmd) {
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
	m.service = ""
	m.resizeScreens()
	return m, cmd
}

// The transformation choice is represented by the app implementation;
// the coordinator only needs a stable loading title.
func (m interactiveModel) hasTransformerContext() bool { return m.app.HasAudioProcessors() }

func saveStatus(result ankitts.GenerateResult, destination string) string {
	var costStatus string
	if result.Cost != nil {
		costStatus = fmt.Sprintf("Cost: $%.6f · ", *result.Cost)
	} else if result.CostErr != nil {
		costStatus = fmt.Sprintf("Cost unavailable: %v · ", result.CostErr)
	}
	return costStatus + fmt.Sprintf("Saved %s to %s", result.Filename, destination)
}
