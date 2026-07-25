package batch

import (
	"context"
	"io"
	"strings"
	"sync"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

type scriptedClient struct {
	prompt func(screen, display) (any, error)
}

func (c *scriptedClient) Prompt(
	_ context.Context,
	screen screen,
	display display,
) (any, error) {
	return c.prompt(screen, display)
}

type fakeClient struct {
	value    any
	err      error
	displays []display
}

func (c *fakeClient) Prompt(
	_ context.Context,
	_ screen,
	display display,
) (any, error) {
	c.displays = append(c.displays, display)
	return c.value, c.err
}

type fakeAnkiClient struct {
	mu             sync.Mutex
	notes          []anki.Note
	notesInfoCalls int
}

func (b *fakeAnkiClient) FindNoteIDs(context.Context, string) ([]int64, error) {
	ids := make([]int64, len(b.notes))
	for index, note := range b.notes {
		ids[index] = note.ID
	}
	return ids, nil
}

func (b *fakeAnkiClient) NotesInfo(_ context.Context, ids []int64) ([]anki.Note, error) {
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

func (*fakeAnkiClient) StoreMediaFile(
	_ context.Context,
	filename string,
	_ []byte,
) (string, error) {
	return filename, nil
}

func (*fakeAnkiClient) UpdateNote(context.Context, anki.NoteUpdate) error {
	return nil
}

func (b *fakeAnkiClient) notesInfoCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notesInfoCalls
}

type fakeTTS struct{}

func (fakeTTS) Generate(context.Context, ankitts.Input) (ankitts.Voice, error) {
	return &fakeVoice{ReadCloser: io.NopCloser(strings.NewReader("audio"))}, nil
}

type fakeVoice struct{ io.ReadCloser }

func (*fakeVoice) Format() string                            { return "mp3" }
func (*fakeVoice) MediaType() string                         { return "audio/mpeg" }
func (*fakeVoice) LoadCost(context.Context) (float64, error) { return 0, nil }
