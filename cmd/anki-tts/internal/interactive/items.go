package interactive

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/internal/textutil"
)

type noteCandidate struct {
	note    anki.Note
	invalid string
}

type item struct {
	title       string
	description string
	value       any
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.description }
func (i item) FilterValue() string { return i.title + " " + i.description }

func deckItems(decks []string) []list.Item {
	sorted := append([]string(nil), decks...)
	sort.Strings(sorted)
	items := make([]list.Item, 0, len(sorted))
	for _, deck := range sorted {
		items = append(items, item{title: deck, value: deck})
	}
	return items
}

func noteItems(notes []anki.Note, options Options) []list.Item {
	items := make([]list.Item, 0, len(notes))
	for _, note := range notes {
		title := firstFieldValue(note)
		if title == "" {
			title = "(empty note)"
		}
		candidate := noteCandidate{note: note}
		if options.FromField != "" {
			field, ok := note.Fields[options.FromField]
			if !ok {
				candidate.invalid = fmt.Sprintf("note %d is missing source field %q", note.ID, options.FromField)
			} else {
				text, _ := textutil.FromHTML(field.Value)
				if strings.TrimSpace(text) == "" {
					candidate.invalid = fmt.Sprintf("note %d has an empty source field %q", note.ID, options.FromField)
				}
			}
		}
		if candidate.invalid == "" && options.ToField != "" {
			if _, ok := note.Fields[options.ToField]; !ok {
				candidate.invalid = fmt.Sprintf("note %d is missing destination field %q", note.ID, options.ToField)
			}
		}
		description := fmt.Sprintf("%s · note %d", note.ModelName, note.ID)
		if candidate.invalid != "" {
			description += " · DISABLED: " + candidate.invalid
		}
		items = append(items, item{title: title, description: description, value: candidate})
	}
	return items
}

func fieldItems(note anki.Note, nonEmpty bool) []list.Item {
	type namedField struct {
		name  string
		field anki.Field
	}
	fields := make([]namedField, 0, len(note.Fields))
	for name, field := range note.Fields {
		if nonEmpty && strings.TrimSpace(field.Value) == "" {
			continue
		}
		fields = append(fields, namedField{name: name, field: field})
	}
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].field.Order == fields[j].field.Order {
			return fields[i].name < fields[j].name
		}
		return fields[i].field.Order < fields[j].field.Order
	})
	items := make([]list.Item, 0, len(fields))
	for _, field := range fields {
		preview, _ := textutil.FromHTML(field.field.Value)
		items = append(items, item{title: field.name, description: preview, value: field.name})
	}
	return items
}

func actionItems() []list.Item {
	return []list.Item{
		item{title: "Replace", description: "Replace the non-empty destination field", value: true},
		item{title: "Cancel", description: "Return without generating audio", value: false},
	}
}

func serviceItems(services []string) []list.Item {
	items := make([]list.Item, 0, len(services))
	for _, service := range services {
		items = append(items, item{title: service, description: "Generate voice", value: service})
	}
	return items
}

func firstFieldValue(note anki.Note) string {
	fields := fieldItems(note, true)
	if len(fields) == 0 {
		return ""
	}
	value := fields[0].(item).description
	return strings.ReplaceAll(value, "\n", " ")
}
