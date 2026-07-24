package step

import (
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/internal/textutil"
)

func fieldListItems(note anki.Note, nonEmpty bool) []list.Item {
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
		items = append(items, listItem{title: field.name, description: preview, value: field.name})
	}
	return items
}
