package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/tts"
)

func TestWorkflowGeneratesStoresAndUpdates(t *testing.T) {
	client := &fakeAnki{}
	service := &fakeTTS{voice: voice("audio bytes", "mp3")}
	services := tts.NewContainer()
	if err := services.Add("OpenRouter", service); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), client, services, nil)
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
	m := New(context.Background(), &fakeAnki{}, tts.NewContainer(), nil)
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
	m := New(context.Background(), &fakeAnki{}, tts.NewContainer(), nil)
	m.screen = actionScreen
	m.busy = false
	m.setList("Destination field behavior", actionItems())
	m = pressEnter(t, m)
	if m.screen != errorScreen || !strings.Contains(m.err.Error(), "no TTS services") {
		t.Fatalf("screen=%v error=%v", m.screen, m.err)
	}
}

func TestTransformationDeterminesUploadedMedia(t *testing.T) {
	client := &fakeAnki{}
	service := &fakeTTS{data: "provider audio", format: "wav"}
	transformer := &fakeTransformer{output: "transformed audio", format: "mp3"}
	m, namedService := readyToGenerateModel(t, client, service, transformer)

	message := m.generateCmd(namedService)().(savedMsg)
	if message.err != nil {
		t.Fatal(message.err)
	}
	wantHash := sha256.Sum256([]byte("transformed audio"))
	wantFilename := fmt.Sprintf("_anki-tts-42-%x.mp3", wantHash[:6])
	if client.mediaFilename != wantFilename {
		t.Fatalf("filename = %q, want %q", client.mediaFilename, wantFilename)
	}
	if string(client.mediaData) != "transformed audio" {
		t.Fatalf("uploaded data = %q", client.mediaData)
	}
}

func TestTransformationFailurePreventsAnkiChanges(t *testing.T) {
	client := &fakeAnki{}
	service := &fakeTTS{data: "provider audio", format: "wav"}
	transformErr := errors.New("FFmpeg failed")
	transformer := &fakeTransformer{errs: []error{transformErr}}
	m, namedService := readyToGenerateModel(t, client, service, transformer)

	message := m.generateCmd(namedService)().(savedMsg)
	if !errors.Is(message.err, transformErr) {
		t.Fatalf("error = %v", message.err)
	}
	if client.storeCalls != 0 || client.updateCalls != 0 {
		t.Fatalf("Anki calls: store=%d update=%d", client.storeCalls, client.updateCalls)
	}
}

func TestTransformationStreamFailurePreventsAnkiChanges(t *testing.T) {
	client := &fakeAnki{}
	service := &fakeTTS{data: "provider audio", format: "wav"}
	streamErr := errors.New("FFmpeg execution failed")
	transformer := &fakeTransformer{streamErr: streamErr, format: "mp3"}
	m, namedService := readyToGenerateModel(t, client, service, transformer)

	message := m.generateCmd(namedService)().(savedMsg)
	if !errors.Is(message.err, streamErr) {
		t.Fatalf("error = %v", message.err)
	}
	if client.storeCalls != 0 || client.updateCalls != 0 {
		t.Fatalf("Anki calls: store=%d update=%d", client.storeCalls, client.updateCalls)
	}
}

func TestRetryRepeatsTransformation(t *testing.T) {
	client := &fakeAnki{}
	service := &fakeTTS{data: "provider audio", format: "wav"}
	transformer := &fakeTransformer{
		errs:   []error{errors.New("temporary FFmpeg failure"), nil},
		output: "retry output",
		format: "mp3",
	}
	m, namedService := readyToGenerateModel(t, client, service, transformer)

	failedMessage := m.generateCmd(namedService)().(savedMsg)
	m = update(t, m, failedMessage)
	if m.retry == nil || m.screen != errorScreen {
		t.Fatalf("retry=%v screen=%v", m.retry, m.screen)
	}
	retriedMessage := m.retry().(savedMsg)
	if retriedMessage.err != nil {
		t.Fatal(retriedMessage.err)
	}
	if transformer.calls != 2 || service.calls != 2 {
		t.Fatalf("calls: transform=%d generate=%d", transformer.calls, service.calls)
	}
	if client.storeCalls != 1 || client.updateCalls != 1 {
		t.Fatalf("Anki calls: store=%d update=%d", client.storeCalls, client.updateCalls)
	}
}

func readyToGenerateModel(t *testing.T, client *fakeAnki, service *fakeTTS, transformer tts.Transformer) (Model, tts.NamedService) {
	t.Helper()
	services := tts.NewContainer()
	if err := services.Add("OpenRouter", service); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), client, services, transformer)
	m.note = anki.Note{ID: 42, Fields: map[string]anki.Field{
		"Front": {Value: "hello"},
		"Audio": {},
	}}
	m.sourceField = "Front"
	m.destinationField = "Audio"
	m.destinationAction = overrideAction
	return m, tts.NamedService{Name: "OpenRouter", Service: service}
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
	mediaData     []byte
	update        anki.NoteUpdate
	storeCalls    int
	updateCalls   int
}

func (*fakeAnki) ListDecks(context.Context) ([]string, error) { return nil, nil }
func (*fakeAnki) ListNotes(context.Context, string) ([]anki.Note, error) {
	return nil, nil
}
func (f *fakeAnki) StoreMediaFile(_ context.Context, filename string, data []byte) (string, error) {
	f.storeCalls++
	f.mediaFilename = filename
	f.mediaData = append([]byte(nil), data...)
	return f.mediaFilename, nil
}
func (f *fakeAnki) UpdateNote(_ context.Context, update anki.NoteUpdate) error {
	f.updateCalls++
	f.update = update
	return nil
}

type fakeTTS struct {
	input  tts.Input
	voice  tts.Voice
	data   string
	format string
	calls  int
}

func (f *fakeTTS) Generate(_ context.Context, input tts.Input) (tts.Voice, error) {
	f.calls++
	f.input = input
	if f.data != "" {
		return voice(f.data, f.format), nil
	}
	return f.voice, nil
}

type fakeTransformer struct {
	errs      []error
	output    string
	format    string
	calls     int
	streamErr error
}

func (f *fakeTransformer) Transform(_ context.Context, input tts.AudioStream) (tts.AudioStream, error) {
	_, _ = io.ReadAll(input.Data)
	_ = input.Data.Close()
	call := f.calls
	f.calls++
	if call < len(f.errs) && f.errs[call] != nil {
		return tts.AudioStream{}, f.errs[call]
	}
	if f.streamErr != nil {
		input.Data = io.NopCloser(errorReader{err: f.streamErr})
	} else {
		input.Data = io.NopCloser(bytes.NewBufferString(f.output))
	}
	input.Format = f.format
	return input, nil
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

func voice(data, format string) tts.Voice {
	return tts.Voice{Audio: tts.AudioStream{Data: io.NopCloser(bytes.NewBufferString(data)), Format: format}}
}
