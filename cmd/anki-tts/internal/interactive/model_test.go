package interactive

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

func TestComposableApplicationNavigationAndContext(t *testing.T) {
	app := &fakeApplication{services: []string{"openrouter"}}
	m := newInteractive(context.Background(), app)
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
	service := "openrouter"
	app := &fakeApplication{
		services: []string{service},
		result:   ankitts.GenerateResult{Filename: "voice.mp3", Cost: &cost},
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
	service := "openrouter"
	app := &fakeApplication{services: []string{service}, errs: []error{errors.New("temporary failure"), nil}, result: ankitts.GenerateResult{Filename: "voice.mp3"}}
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
	m := newInteractive(context.Background(), &fakeApplication{})
	if m.failure == nil || !strings.Contains(m.View().Content, "no TTS services") {
		t.Fatalf("failure=%v view=%q", m.failure, m.View().Content)
	}
}

func TestQuitAndResizeAreCoordinatedByRoot(t *testing.T) {
	app := &fakeApplication{services: []string{"openrouter"}}
	m := newInteractive(context.Background(), app)
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

func readyAtService(t *testing.T, app *fakeApplication, service string) interactiveModel {
	t.Helper()
	m := newInteractive(context.Background(), app)
	m = update(t, m, decksLoadedMsg{decks: []string{"Japanese"}})
	m = pressEnter(t, m)
	m = update(t, m, notesLoadedMsg{notes: []anki.Note{testNote()}})
	m = pressEnter(t, m) // note
	m = pressEnter(t, m) // source
	field := m.active().(*fieldModel)
	field.list.Select(1)
	m = pressEnter(t, m) // destination: Audio
	if got := m.active().(*serviceModel).list.SelectedItem().(item).value.(string); got != service {
		t.Fatalf("service=%q", got)
	}
	return m
}

func testNote() anki.Note {
	return anki.Note{ID: 42, ModelName: "Basic", Fields: map[string]anki.Field{
		"Front": {Value: "Hello", Order: 0},
		"Audio": {Value: "", Order: 1},
	}}
}

func update(t *testing.T, model interactiveModel, msg tea.Msg) interactiveModel {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(interactiveModel)
	if !ok {
		t.Fatalf("updated model has type %T", updated)
	}
	return result
}

func updateWithCmd(t *testing.T, model interactiveModel, msg tea.Msg) (interactiveModel, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	result, ok := updated.(interactiveModel)
	if !ok {
		t.Fatalf("updated model has type %T", updated)
	}
	return result, cmd
}

func pressEnter(t *testing.T, model interactiveModel) interactiveModel {
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

func pressEnterRunning(t *testing.T, model interactiveModel) interactiveModel {
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

type fakeApplication struct {
	services      []string
	result        ankitts.GenerateResult
	errs          []error
	generateCalls int
	request       ankitts.GenerationRequest
	selector      ankitts.NoteSelector
	notes         []anki.Note
}

func (f *fakeApplication) ListDecks(context.Context) ([]string, error) { return nil, nil }
func (f *fakeApplication) SelectNotes(context.Context, ankitts.NoteSelector) ([]anki.Note, error) {
	return f.notes, nil
}
func (f *fakeApplication) ServiceNames() []string   { return f.services }
func (f *fakeApplication) HasAudioProcessors() bool { return false }
func (f *fakeApplication) Prepare(request ankitts.GenerationRequest) (ankitts.Plan, error) {
	f.request = request
	return ankitts.Plan{}, nil
}
func (f *fakeApplication) Execute(_ context.Context, _ ankitts.Plan, _ ankitts.ExecuteOptions) (ankitts.BatchResult, error) {
	call := f.generateCalls
	f.generateCalls++
	if call < len(f.errs) && f.errs[call] != nil {
		return ankitts.BatchResult{}, f.errs[call]
	}
	return ankitts.BatchResult{Items: []ankitts.ItemResult{{Result: f.result}}}, nil
}
