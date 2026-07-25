package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/pipeline"
)

func TestBatchConfirmsOverwriteAndProcessesEveryNote(t *testing.T) {
	client := &batchAnki{notes: []anki.Note{
		{ID: 2, ModelName: "Basic", Fields: map[string]anki.Field{"Front": {Value: "two"}, "Audio": {Value: "old"}}},
		{ID: 1, ModelName: "Basic", Fields: map[string]anki.Field{"Front": {Value: "one"}, "Audio": {Value: ""}}},
	}}
	services := ankitts.NewServiceContainer()
	if err := services.Add("Test", batchTTS{}); err != nil {
		t.Fatal(err)
	}
	app, err := ankitts.New(client, services, nil, pipeline.Config{
		"Test": pipeline.DefaultStageConfig(2),
		"anki": pipeline.DefaultStageConfig(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runApplication(context.Background(), app, runOptions{
		FromField: "Front", ToField: "Audio", Service: "Test",
	}, strings.NewReader("yes\ny\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if updates := client.updateCount(); updates != 2 {
		t.Fatalf("updates=%d", updates)
	}
	got := output.String()
	for _, want := range []string{"Notes with non-empty destination fields", "Replace 1 non-empty", "2 succeeded, 0 failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q: %s", want, got)
		}
	}
}

func TestBatchPreflightRejectsAllNotesBeforeGeneration(t *testing.T) {
	client := &batchAnki{notes: []anki.Note{
		{
			ID: 1,
			Fields: map[string]anki.Field{
				"Front": {Value: "valid"},
				"Audio": {},
			},
		},
		{
			ID: 2,
			Fields: map[string]anki.Field{
				"Front": {Value: "missing destination"},
			},
		},
	}}
	services := ankitts.NewServiceContainer()
	if err := services.Add("Test", batchTTS{}); err != nil {
		t.Fatal(err)
	}
	app, err := ankitts.New(client, services, nil, pipeline.Config{
		"Test": pipeline.DefaultStageConfig(2),
		"anki": pipeline.DefaultStageConfig(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = runApplication(
		context.Background(),
		app,
		runOptions{FromField: "Front", ToField: "Audio", Service: "Test"},
		strings.NewReader("y\n"),
		io.Discard,
	)
	if err == nil || !strings.Contains(err.Error(), `note 2: missing destination field "Audio"`) {
		t.Fatalf("error=%v", err)
	}
	if updates := client.updateCount(); updates != 0 {
		t.Fatalf("updates=%d", updates)
	}
}

func TestCompletionGenerationDoesNotLoadRuntimeConfiguration(t *testing.T) {
	var output bytes.Buffer
	cmd := newRootCommand(strings.NewReader(""), &output, io.Discard)
	cmd.SetArgs([]string{"completion", "bash"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "__start_anki-tts") {
		t.Fatal("generated completion script is missing Cobra entrypoint")
	}
}

func TestConcurrencyFlagsWereRemoved(t *testing.T) {
	cmd := newRootCommand(strings.NewReader(""), io.Discard, io.Discard)
	for _, name := range []string{"synthesis-concurrency", "audio-concurrency"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Fatalf("flag --%s is still registered", name)
		}
	}
}

func TestNativeFilterReplacesSelectorFlags(t *testing.T) {
	cmd := newRootCommand(strings.NewReader(""), io.Discard, io.Discard)
	if cmd.Flags().Lookup("filter") == nil {
		t.Fatal("--filter is not registered")
	}
	for _, name := range []string{"deck", "note-template", "field-match"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Fatalf("legacy flag --%s is still registered", name)
		}
	}
}

type batchAnki struct {
	mu             sync.Mutex
	notes          []anki.Note
	updates        []anki.NoteUpdate
	notesInfoCalls int
}

func (b *batchAnki) FindNoteIDs(context.Context, string) ([]int64, error) {
	ids := make([]int64, len(b.notes))
	for index, note := range b.notes {
		ids[index] = note.ID
	}
	return ids, nil
}

func (b *batchAnki) NotesInfo(_ context.Context, ids []int64) ([]anki.Note, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notesInfoCalls++
	byID := make(map[int64]anki.Note, len(b.notes))
	for _, note := range b.notes {
		byID[note.ID] = note
	}
	notes := make([]anki.Note, 0, len(ids))
	for _, id := range ids {
		notes = append(notes, byID[id])
	}
	return notes, nil
}

func (*batchAnki) StoreMediaFile(_ context.Context, filename string, _ []byte) (string, error) {
	return filename, nil
}

func (b *batchAnki) UpdateNote(_ context.Context, update anki.NoteUpdate) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updates = append(b.updates, update)
	return nil
}

func (b *batchAnki) updateCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.updates)
}

type batchTTS struct{}

func (batchTTS) Generate(context.Context, ankitts.Input) (ankitts.Voice, error) {
	return &batchVoice{ReadCloser: io.NopCloser(strings.NewReader("audio"))}, nil
}

type batchVoice struct{ io.ReadCloser }

func (*batchVoice) Format() string                            { return "mp3" }
func (*batchVoice) MediaType() string                         { return "audio/mpeg" }
func (*batchVoice) LoadCost(context.Context) (float64, error) { return 0, nil }
