package ankitts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/pipeline"
)

func TestSearchNotesPassesNativeFilterAndLimitsDeterministically(t *testing.T) {
	client := &selectionAnki{ids: []int64{30, 5, 30, 20, 10}}
	app := newSelectionApplication(t, client)
	selection, err := app.SearchNotes(t.Context(), NoteQuery{
		Filter: `deck:Japanese note:Basic`,
		Limit:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.filter != `deck:Japanese note:Basic` {
		t.Fatalf("filter=%q", client.filter)
	}
	if !slices.Equal(selection.IDs, []int64{5, 10, 20}) {
		t.Fatalf("IDs=%v", selection.IDs)
	}
}

func TestSearchNotesRejectsNegativeLimit(t *testing.T) {
	app := newSelectionApplication(t, &selectionAnki{})
	if _, err := app.SearchNotes(t.Context(), NoteQuery{Limit: -1}); err == nil {
		t.Fatal("negative limit was accepted")
	}
}

func TestNotesLoadsLazilyInStableBatches(t *testing.T) {
	client := &selectionAnki{notes: map[int64]anki.Note{
		1: {ID: 1}, 2: {ID: 2}, 3: {ID: 3}, 4: {ID: 4}, 5: {ID: 5},
	}}
	app := newSelectionApplication(t, client)
	sequence := app.Notes(
		t.Context(),
		NoteSelection{IDs: []int64{1, 2, 3, 4, 5}},
		NoteLoadOptions{BatchSize: 2},
	)
	if len(client.batches) != 0 {
		t.Fatal("notesInfo was called before iteration")
	}

	var got []int64
	for result := range sequence {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		got = append(got, result.Note.ID)
		if len(got) == 3 {
			break
		}
	}
	if !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("IDs=%v", got)
	}
	if !slices.EqualFunc(client.batches, [][]int64{{1, 2}, {3, 4}}, slices.Equal) {
		t.Fatalf("batches=%v", client.batches)
	}
}

func TestNotesYieldsOneTerminalError(t *testing.T) {
	want := errors.New("collection unavailable")
	client := &selectionAnki{infoErr: want}
	app := newSelectionApplication(t, client)
	var results []NoteResult
	for result := range app.Notes(
		t.Context(),
		NoteSelection{IDs: []int64{1, 2}},
		NoteLoadOptions{BatchSize: 1},
	) {
		results = append(results, result)
	}
	if len(results) != 1 || !errors.Is(results[0].Err, want) {
		t.Fatalf("results=%v", results)
	}
}

func TestNotesRejectsNegativeBatchSizeWithoutCallingAnki(t *testing.T) {
	client := &selectionAnki{}
	app := newSelectionApplication(t, client)
	var results []NoteResult
	for result := range app.Notes(
		t.Context(),
		NoteSelection{IDs: []int64{1}},
		NoteLoadOptions{BatchSize: -1},
	) {
		results = append(results, result)
	}
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results=%v", results)
	}
	if len(client.batches) != 0 {
		t.Fatalf("notesInfo batches=%v", client.batches)
	}
}

func newSelectionApplication(t *testing.T, client *selectionAnki) *Application {
	t.Helper()
	app, err := New(client, nil, nil, pipeline.Config{
		"anki": pipeline.DefaultStageConfig(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

type selectionAnki struct {
	ids     []int64
	notes   map[int64]anki.Note
	filter  string
	batches [][]int64
	infoErr error
}

func (s *selectionAnki) FindNoteIDs(_ context.Context, filter string) ([]int64, error) {
	s.filter = filter
	return slices.Clone(s.ids), nil
}

func (s *selectionAnki) NotesInfo(_ context.Context, ids []int64) ([]anki.Note, error) {
	s.batches = append(s.batches, slices.Clone(ids))
	if s.infoErr != nil {
		return nil, s.infoErr
	}
	notes := make([]anki.Note, 0, len(ids))
	for _, id := range ids {
		note, ok := s.notes[id]
		if !ok {
			return nil, fmt.Errorf("unknown note %d", id)
		}
		notes = append(notes, note)
	}
	return notes, nil
}

func (*selectionAnki) StoreMediaFile(context.Context, string, []byte) (string, error) {
	return "", nil
}
func (*selectionAnki) UpdateNote(context.Context, anki.NoteUpdate) error { return nil }
