package ankitts

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/internal/textutil"
)

// FieldMatcher selects notes whose named field matches Pattern after HTML is
// converted to plain text.
type FieldMatcher struct {
	Field   string
	Pattern *regexp.Regexp
}

// NoteSelector describes the intersection of CLI note selectors.
type NoteSelector struct {
	Decks         []string
	NoteTemplates []string
	FieldMatchers []FieldMatcher
	Limit         int
}

// SelectNotes returns matching notes, deduplicated and ordered by note ID.
func (s *Application) SelectNotes(ctx context.Context, selector NoteSelector) ([]anki.Note, error) {
	decks := selector.Decks
	if len(decks) == 0 {
		decks = []string{""}
	}

	byID := make(map[int64]anki.Note)
	for _, deck := range decks {
		notes, err := s.anki.ListNotes(ctx, deck)
		if err != nil {
			return nil, err
		}
		for _, note := range notes {
			if matchesNote(note, selector) {
				byID[note.ID] = note
			}
		}
	}

	notes := make([]anki.Note, 0, len(byID))
	for _, note := range byID {
		notes = append(notes, note)
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
	if selector.Limit > 0 && len(notes) > selector.Limit {
		notes = notes[:selector.Limit]
	}
	return notes, nil
}

func matchesNote(note anki.Note, selector NoteSelector) bool {
	if len(selector.NoteTemplates) > 0 && !contains(selector.NoteTemplates, note.ModelName) {
		return false
	}
	for _, matcher := range selector.FieldMatchers {
		field, ok := note.Fields[matcher.Field]
		if !ok || matcher.Pattern == nil {
			return false
		}
		value, err := textutil.FromHTML(field.Value)
		if err != nil || !matcher.Pattern.MatchString(value) {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// ParseFieldMatcher parses FIELD=REGEX syntax.
func ParseFieldMatcher(value string) (FieldMatcher, error) {
	field, pattern, ok := strings.Cut(value, "=")
	field = strings.TrimSpace(field)
	if !ok || field == "" {
		return FieldMatcher{}, fmt.Errorf("field matcher %q must use FIELD=REGEX syntax", value)
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return FieldMatcher{}, fmt.Errorf("field matcher %q: %w", value, err)
	}
	return FieldMatcher{Field: field, Pattern: compiled}, nil
}
