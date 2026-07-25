package ankitts

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"

	"jlzhjp.dev/ankitts/anki"
)

const defaultNoteBatchSize = 100

// NoteQuery selects note IDs using Anki's native search syntax.
type NoteQuery struct {
	Filter string
	Limit  int
}

// NoteSelection is a deterministic snapshot of matching note IDs.
type NoteSelection struct {
	IDs []int64
}

// NoteLoadOptions controls how note details are retrieved from AnkiConnect.
type NoteLoadOptions struct {
	BatchSize int
}

// NoteResult is one ordered result from lazy note hydration.
type NoteResult struct {
	Note anki.Note
	Err  error
}

// SearchNotes finds, deduplicates, sorts, and limits matching note IDs.
func (a *Application) SearchNotes(ctx context.Context, query NoteQuery) (NoteSelection, error) {
	if query.Limit < 0 {
		return NoteSelection{}, errors.New("note limit must not be negative")
	}
	ids, err := a.anki.FindNoteIDs(ctx, query.Filter)
	if err != nil {
		return NoteSelection{}, err
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if query.Limit > 0 && len(ids) > query.Limit {
		ids = ids[:query.Limit]
	}
	return NoteSelection{IDs: slices.Clone(ids)}, nil
}

// Notes lazily retrieves complete note information in bounded batches.
func (a *Application) Notes(
	ctx context.Context,
	selection NoteSelection,
	options NoteLoadOptions,
) iter.Seq[NoteResult] {
	ids := slices.Clone(selection.IDs)
	batchSize := options.BatchSize
	if batchSize == 0 {
		batchSize = defaultNoteBatchSize
	}

	return func(yield func(NoteResult) bool) {
		if batchSize < 0 {
			yield(NoteResult{Err: errors.New("note batch size must not be negative")})
			return
		}
		for batchIDs := range slices.Chunk(ids, batchSize) {
			notes, err := a.anki.NotesInfo(ctx, batchIDs)
			if err != nil {
				yield(NoteResult{Err: err})
				return
			}

			byID := make(map[int64]anki.Note, len(notes))
			for _, note := range notes {
				byID[note.ID] = note
			}
			for _, id := range batchIDs {
				note, ok := byID[id]
				if !ok {
					yield(NoteResult{
						Err: fmt.Errorf("get note information: note %d was not returned", id),
					})
					return
				}
				if !yield(NoteResult{Note: note}) {
					return
				}
			}
		}
	}
}

// NoteResults returns an iterator over an in-memory note snapshot.
func NoteResults(notes ...anki.Note) iter.Seq[NoteResult] {
	notes = slices.Clone(notes)
	return func(yield func(NoteResult) bool) {
		for _, note := range notes {
			if !yield(NoteResult{Note: note}) {
				return
			}
		}
	}
}
