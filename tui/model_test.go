package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/tts"
)

func TestWorkflowGeneratesStoresAndUpdates(t *testing.T) {
	client := &fakeAnki{}
	service := &fakeTTS{voice: tts.Voice{Data: []byte("audio bytes"), Format: "mp3"}}
	services := tts.NewContainer()
	if err := services.Add("OpenRouter", service); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), client, services)
	m = update(t, m, decksMsg{decks: []string{"Japanese"}})
	m = pressEnter(t, m)
	if m.deck != "Japanese" || !m.busy {
		t.Fatalf("deck state = %q, busy=%v", m.deck, m.busy)
	}

	note := anki.Note{ID: 42, ModelName: "Basic", Fields: map[string]anki.Field{
		"Front": {Value: `<b>Hello</b>&nbsp;world`, Order: 0},
		"Audio": {Value: "existing", Order: 1},
	}}
	m = update(t, m, notesMsg{notes: []anki.Note{note}})
	m = pressEnter(t, m) // note
	m = pressEnter(t, m) // source: Front
	m.list.Select(1)
	m = pressEnter(t, m) // destination: Audio
	m.list.Select(1)
	m = pressEnter(t, m) // append
	m = pressEnter(t, m) // service

	msg := m.generateCmd(tts.NamedService{Name: "OpenRouter", Service: service})()
	saved, ok := msg.(savedMsg)
	if !ok || saved.err != nil {
		t.Fatalf("generate message = %#v", msg)
	}
	if service.input.Text != "Hello world" {
		t.Fatalf("TTS input = %q", service.input.Text)
	}
	if client.mediaFilename != "_anki-tts-42-"+audioHashPrefix+".mp3" {
		t.Fatalf("media filename = %q", client.mediaFilename)
	}
	wantField := "existing<br>[sound:" + client.mediaFilename + "]"
	if got := client.update.Fields["Audio"]; got != wantField {
		t.Fatalf("updated Audio = %q, want %q", got, wantField)
	}
}

func TestCancelReturnsToDestination(t *testing.T) {
	m := New(context.Background(), &fakeAnki{}, tts.NewContainer())
	m.note = anki.Note{Fields: map[string]anki.Field{"Front": {Value: "text"}}}
	m.screen = actionScreen
	m.busy = false
	m.setList("Destination field behavior", actionItems())
	m.list.Select(2)
	m = pressEnter(t, m)
	if m.screen != destinationScreen {
		t.Fatalf("screen = %v, want destination", m.screen)
	}
}

func TestNoServicesShowsError(t *testing.T) {
	m := New(context.Background(), &fakeAnki{}, tts.NewContainer())
	m.screen = actionScreen
	m.busy = false
	m.setList("Destination field behavior", actionItems())
	m = pressEnter(t, m)
	if m.screen != errorScreen || !strings.Contains(m.err.Error(), "no TTS services") {
		t.Fatalf("screen=%v error=%v", m.screen, m.err)
	}
}

const audioHashPrefix = "ef71589075cc"

func update(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model has type %T", updated)
	}
	return result
}

func pressEnter(t *testing.T, model Model) Model {
	t.Helper()
	return update(t, model, tea.KeyPressMsg{Code: tea.KeyEnter})
}

type fakeAnki struct {
	mediaFilename string
	update        anki.NoteUpdate
}

func (*fakeAnki) ListDecks(context.Context) ([]string, error) { return nil, nil }
func (*fakeAnki) ListNotes(context.Context, string) ([]anki.Note, error) {
	return nil, nil
}
func (f *fakeAnki) StoreMediaFile(_ context.Context, filename string, _ []byte) (string, error) {
	f.mediaFilename = filename
	return filename, nil
}
func (f *fakeAnki) UpdateNote(_ context.Context, update anki.NoteUpdate) error {
	f.update = update
	return nil
}

type fakeTTS struct {
	input tts.Input
	voice tts.Voice
}

func (f *fakeTTS) Generate(_ context.Context, input tts.Input) (tts.Voice, error) {
	f.input = input
	return f.voice, nil
}
