package ankitts

import (
	"context"
	"reflect"
	"testing"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/pipeline"
)

func TestSelectNotesCombinesSelectorsAndLimitsDeterministically(t *testing.T) {
	client := &selectionAnki{notes: map[string][]anki.Note{
		"A": {
			{ID: 30, ModelName: "Basic", Fields: map[string]anki.Field{"Front": {Value: "<b>cat</b>"}}},
			{ID: 10, ModelName: "Cloze", Fields: map[string]anki.Field{"Front": {Value: "cat"}}},
		},
		"B": {
			{ID: 30, ModelName: "Basic", Fields: map[string]anki.Field{"Front": {Value: "<b>cat</b>"}}},
			{ID: 5, ModelName: "Basic", Fields: map[string]anki.Field{"Front": {Value: "cat"}}},
			{ID: 20, ModelName: "Basic", Fields: map[string]anki.Field{"Front": {Value: "dog"}}},
		},
	}}
	matcher, err := ParseFieldMatcher(`Front=^cat$`)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(client, nil, nil, pipeline.Config{
		"anki": pipeline.DefaultStageConfig(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	notes, err := service.SelectNotes(context.Background(), NoteSelector{
		Decks:         []string{"A", "B"},
		NoteTemplates: []string{"Basic"},
		FieldMatchers: []FieldMatcher{matcher},
		Limit:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := noteIDs(notes); !reflect.DeepEqual(got, []int64{5}) {
		t.Fatalf("note IDs = %v", got)
	}
}

func TestParseFieldMatcher(t *testing.T) {
	matcher, err := ParseFieldMatcher(`Expression=^ねこ$`)
	if err != nil || matcher.Field != "Expression" || !matcher.Pattern.MatchString("ねこ") {
		t.Fatalf("matcher=%+v error=%v", matcher, err)
	}
	for _, value := range []string{"missing-separator", "=empty-field", "Front=["} {
		if _, err := ParseFieldMatcher(value); err == nil {
			t.Fatalf("ParseFieldMatcher(%q) succeeded", value)
		}
	}
}

func noteIDs(notes []anki.Note) []int64 {
	ids := make([]int64, len(notes))
	for index, note := range notes {
		ids[index] = note.ID
	}
	return ids
}

type selectionAnki struct{ notes map[string][]anki.Note }

func (*selectionAnki) ListDecks(context.Context) ([]string, error)         { return nil, nil }
func (*selectionAnki) ListNoteTemplates(context.Context) ([]string, error) { return nil, nil }
func (*selectionAnki) ListTemplateFields(context.Context, string) ([]string, error) {
	return nil, nil
}
func (s *selectionAnki) ListNotes(_ context.Context, deck string) ([]anki.Note, error) {
	return s.notes[deck], nil
}
func (*selectionAnki) StoreMediaFile(context.Context, string, []byte) (string, error) {
	return "", nil
}
func (*selectionAnki) UpdateNote(context.Context, anki.NoteUpdate) error { return nil }
