package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/tts"
	"jlzhjp.dev/anki-tts/workflow"
)

func TestComposableWorkflowNavigationAndContext(t *testing.T) {
	app := &fakeWorkflow{services: []tts.NamedService{{Name: "OpenRouter", Service: fakeService{}}}}
	m := New(context.Background(), app)
	m = update(t, m, decksLoadedMsg{decks: []string{"Japanese", "English"}})
	deck := m.active().(*deckModel)
	deck.list.Select(1)
	m = pressEnter(t, m)
	if len(m.screens) != 2 || m.deck != "Japanese" {
		t.Fatalf("screens=%d deck=%q", len(m.screens), m.deck)
	}

	note := testNote()
	m = update(t, m, notesLoadedMsg{notes: []anki.Note{note}})
	m = pressEnter(t, m)
	m = pressEnter(t, m) // source: Front
	if view := m.View().Content; !strings.Contains(view, "Deck: Japanese") || !strings.Contains(view, "Source: Front") {
		t.Fatalf("view missing context: %q", view)
	}

	source := m.screens[2].(*fieldModel)
	source.list.Select(1)
	m, _ = updateWithCmd(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.active() != source || source.list.Index() != 1 {
		t.Fatalf("source screen state was not preserved")
	}
}

func TestSuccessfulGenerationRefreshesNotesAndShowsStatus(t *testing.T) {
	cost := 0.00125
	service := tts.NamedService{Name: "OpenRouter", Service: fakeService{}}
	app := &fakeWorkflow{
		services: []tts.NamedService{service},
		result:   workflow.GenerateResult{Filename: "voice.mp3", Cost: &cost},
	}
	m := readyAtService(t, app, service)
	m = pressEnterRunning(t, m)
	if len(m.screens) != 2 {
		t.Fatalf("screens=%d, want note screen", len(m.screens))
	}
	notes := m.active().(*noteModel)
	if !notes.busy {
		t.Fatal("note refresh did not start")
	}
	m = update(t, m, notesLoadedMsg{notes: []anki.Note{testNote()}})
	if view := m.View().Content; !strings.Contains(view, "Cost: $0.001250") || !strings.Contains(view, "Saved voice.mp3 to Audio") {
		t.Fatalf("view=%q", view)
	}
	if app.request.SourceField != "Front" || app.request.DestinationField != "Audio" {
		t.Fatalf("request=%+v", app.request)
	}
}

func TestGenerationErrorCanRetry(t *testing.T) {
	service := tts.NamedService{Name: "OpenRouter", Service: fakeService{}}
	app := &fakeWorkflow{services: []tts.NamedService{service}, errs: []error{errors.New("temporary failure"), nil}, result: workflow.GenerateResult{Filename: "voice.mp3"}}
	m := readyAtService(t, app, service)
	m = pressEnterRunning(t, m)
	if m.failure == nil || m.failure.retry == nil {
		t.Fatalf("failure=%+v", m.failure)
	}
	m = pressEnterRunning(t, m)
	if m.failure != nil || len(m.screens) != 2 || app.generateCalls != 2 {
		t.Fatalf("failure=%v screens=%d calls=%d", m.failure, len(m.screens), app.generateCalls)
	}
}

func TestNoServicesShowsInitialError(t *testing.T) {
	m := New(context.Background(), &fakeWorkflow{})
	if m.failure == nil || !strings.Contains(m.View().Content, "no TTS services") {
		t.Fatalf("failure=%v view=%q", m.failure, m.View().Content)
	}
}

func TestQuitAndResizeAreCoordinatedByRoot(t *testing.T) {
	app := &fakeWorkflow{services: []tts.NamedService{{Name: "OpenRouter", Service: fakeService{}}}}
	m := New(context.Background(), app)
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	deck := m.active().(*deckModel)
	if deck.list.Width() != 100 || deck.list.Height() >= 30 {
		t.Fatalf("list size=%dx%d", deck.list.Width(), deck.list.Height())
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if cmd == nil {
		t.Fatal("q did not produce a quit command")
	}
}

func readyAtService(t *testing.T, app *fakeWorkflow, service tts.NamedService) Model {
	t.Helper()
	m := New(context.Background(), app)
	m = update(t, m, decksLoadedMsg{decks: []string{"Japanese"}})
	m = pressEnter(t, m)
	m = update(t, m, notesLoadedMsg{notes: []anki.Note{testNote()}})
	m = pressEnter(t, m) // note
	m = pressEnter(t, m) // source
	field := m.active().(*fieldModel)
	field.list.Select(1)
	m = pressEnter(t, m) // destination: Audio
	if got := m.active().(*serviceModel).list.SelectedItem().(item).value.(tts.NamedService); got.Name != service.Name {
		t.Fatalf("service=%q", got.Name)
	}
	return m
}

func testNote() anki.Note {
	return anki.Note{ID: 42, ModelName: "Basic", Fields: map[string]anki.Field{
		"Front": {Value: "Hello", Order: 0},
		"Audio": {Value: "", Order: 1},
	}}
}

func update(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model has type %T", updated)
	}
	return result
}

func updateWithCmd(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model has type %T", updated)
	}
	return result, cmd
}

func pressEnter(t *testing.T, model Model) Model {
	t.Helper()
	model, cmd := updateWithCmd(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		return model
	}
	message := cmd()
	if _, batched := message.(tea.BatchMsg); batched {
		return model
	}
	return update(t, model, message)
}

func pressEnterRunning(t *testing.T, model Model) Model {
	t.Helper()
	model, cmd := updateWithCmd(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not produce a command")
	}
	message := cmd()
	model, cmd = updateWithCmd(t, model, message)
	if cmd == nil {
		return model
	}
	message, ok := generatedFromCommand(cmd)
	if !ok {
		t.Fatal("command did not produce a generation result")
	}
	return update(t, model, message)
}

func generatedFromCommand(cmd tea.Cmd) (tea.Msg, bool) {
	message := cmd()
	if _, ok := message.(generatedMsg); ok {
		return message, true
	}
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return nil, false
	}
	for _, child := range batch {
		if child == nil {
			continue
		}
		if message, ok := generatedFromCommand(child); ok {
			return message, true
		}
	}
	return nil, false
}

type fakeWorkflow struct {
	services      []tts.NamedService
	result        workflow.GenerateResult
	errs          []error
	generateCalls int
	request       workflow.GenerationSpec
	selector      workflow.NoteSelector
	notes         []anki.Note
}

func (f *fakeWorkflow) ListDecks(context.Context) ([]string, error) { return nil, nil }
func (f *fakeWorkflow) SelectNotes(context.Context, workflow.NoteSelector) ([]anki.Note, error) {
	return f.notes, nil
}
func (f *fakeWorkflow) Services() []tts.NamedService { return f.services }
func (f *fakeWorkflow) TransformsAudio() bool        { return false }
func (f *fakeWorkflow) Plan(request workflow.GenerationSpec) (workflow.Plan, error) {
	f.request = request
	return workflow.Plan{}, nil
}
func (f *fakeWorkflow) Execute(_ context.Context, _ workflow.Plan, _ workflow.PipelineOptions) (workflow.BatchResult, error) {
	call := f.generateCalls
	f.generateCalls++
	if call < len(f.errs) && f.errs[call] != nil {
		return workflow.BatchResult{}, f.errs[call]
	}
	return workflow.BatchResult{Items: []workflow.ItemResult{{Result: f.result}}}, nil
}

type fakeService struct{}

func (fakeService) Generate(context.Context, tts.Input) (tts.Voice, error) { return nil, nil }
