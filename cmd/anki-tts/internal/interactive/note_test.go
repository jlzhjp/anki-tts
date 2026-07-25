package interactive

import (
	"context"
	"iter"
	"slices"
	"testing"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

func TestNoteScreenLoadsOneConsumerWindowAtATime(t *testing.T) {
	source := &windownoteSource{notes: testWindowNotes(120)}
	screen := newNoteScreen(
		context.Background(),
		source,
		noteOptions{Query: ankitts.NoteQuery{Filter: "tag:tts"}},
	)

	started := screen.start()().(noteStreamStartedMsg)
	if source.filter != "tag:tts" {
		t.Fatalf("filter=%q", source.filter)
	}
	_, load := screen.Update(started)
	loaded := load().(notesLoadedMsg)
	_, _ = screen.Update(loaded)

	if len(screen.notes) != interactiveNoteWindowSize {
		t.Fatalf("loaded=%d, want %d", len(screen.notes), interactiveNoteWindowSize)
	}
	if source.batchSize != interactiveNoteWindowSize {
		t.Fatalf("batch size=%d", source.batchSize)
	}
	if source.batches != 1 {
		t.Fatalf("batches=%d, want 1 before the next window is requested", source.batches)
	}

	loaded = screen.loadWindow()().(notesLoadedMsg)
	_, _ = screen.Update(loaded)
	if len(screen.notes) != 2*interactiveNoteWindowSize {
		t.Fatalf("loaded=%d, want %d", len(screen.notes), 2*interactiveNoteWindowSize)
	}
	if source.batches != 2 {
		t.Fatalf("batches=%d, want 2", source.batches)
	}
	screen.stopStream()
}

func TestNoteScreenRefreshesOnlySelectedNote(t *testing.T) {
	source := &windownoteSource{notes: []anki.Note{{
		ID: 1,
		Fields: map[string]anki.Field{
			"Front": {Value: "before"},
		},
	}}}
	screen := newNoteScreen(context.Background(), source, noteOptions{})
	screen.notes = append([]anki.Note(nil), source.notes...)
	source.notes[0] = anki.Note{
		ID: 1,
		Fields: map[string]anki.Field{
			"Front": {Value: "after"},
		},
	}
	refreshNoteList(screen, "saved", 1)

	message := screen.refresh()().(noteRefreshedMsg)
	_, _ = screen.Update(message)
	if got := screen.notes[0].Fields["Front"].Value; got != "after" {
		t.Fatalf("Front=%q", got)
	}
	if source.lastSelection.IDs[0] != 1 || source.batchSize != 1 {
		t.Fatalf("selection=%v batchSize=%d", source.lastSelection.IDs, source.batchSize)
	}
}

type windownoteSource struct {
	notes         []anki.Note
	filter        string
	batchSize     int
	batches       int
	lastSelection ankitts.NoteSelection
}

func (s *windownoteSource) SearchNotes(
	_ context.Context,
	query ankitts.NoteQuery,
) (ankitts.NoteSelection, error) {
	s.filter = query.Filter
	ids := make([]int64, len(s.notes))
	for index, note := range s.notes {
		ids[index] = note.ID
	}
	return ankitts.NoteSelection{IDs: ids}, nil
}

func (s *windownoteSource) Notes(
	_ context.Context,
	selection ankitts.NoteSelection,
	options ankitts.NoteLoadOptions,
) iter.Seq[ankitts.NoteResult] {
	s.batchSize = options.BatchSize
	s.lastSelection = selection
	byID := make(map[int64]anki.Note, len(s.notes))
	for _, note := range s.notes {
		byID[note.ID] = note
	}
	return func(yield func(ankitts.NoteResult) bool) {
		for ids := range slices.Chunk(selection.IDs, options.BatchSize) {
			s.batches++
			for _, id := range ids {
				if !yield(ankitts.NoteResult{Note: byID[id]}) {
					return
				}
			}
		}
	}
}

func testWindowNotes(count int) []anki.Note {
	notes := make([]anki.Note, count)
	for index := range notes {
		notes[index] = anki.Note{
			ID:        int64(index + 1),
			ModelName: "Basic",
			Fields: map[string]anki.Field{
				"Front": {Value: "note"},
			},
		}
	}
	return notes
}
