package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/batch"
	"jlzhjp.dev/anki-tts/pipeline"
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
	err = batch.Run(context.Background(), app, batch.Options{
		FromField: "Front", ToField: "Audio", Service: "Test",
	}, strings.NewReader("yes\ny\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	if len(client.updates) != 2 {
		t.Fatalf("updates=%d", len(client.updates))
	}
	got := output.String()
	for _, want := range []string{"WILL OVERWRITE", "Replace 1 non-empty", "2 succeeded, 0 failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q: %s", want, got)
		}
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

type batchAnki struct {
	notes   []anki.Note
	updates []anki.NoteUpdate
}

func (*batchAnki) ListDecks(context.Context) ([]string, error)         { return nil, nil }
func (*batchAnki) ListNoteTemplates(context.Context) ([]string, error) { return nil, nil }
func (*batchAnki) ListTemplateFields(context.Context, string) ([]string, error) {
	return nil, nil
}
func (b *batchAnki) ListNotes(context.Context, string) ([]anki.Note, error) { return b.notes, nil }
func (*batchAnki) StoreMediaFile(_ context.Context, filename string, _ []byte) (string, error) {
	return filename, nil
}
func (b *batchAnki) UpdateNote(_ context.Context, update anki.NoteUpdate) error {
	b.updates = append(b.updates, update)
	return nil
}

type batchTTS struct{}

func (batchTTS) Generate(context.Context, ankitts.Input) (ankitts.Voice, error) {
	return &batchVoice{ReadCloser: io.NopCloser(strings.NewReader("audio"))}, nil
}

type batchVoice struct{ io.ReadCloser }

func (*batchVoice) Format() string                            { return "mp3" }
func (*batchVoice) MediaType() string                         { return "audio/mpeg" }
func (*batchVoice) LoadCost(context.Context) (float64, error) { return 0, nil }
