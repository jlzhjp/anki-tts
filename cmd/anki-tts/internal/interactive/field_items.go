package interactive

import (
	"cmp"
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"

	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/internal/textutil"
)

func fieldListItems(note *anki.Note, nonEmpty bool) []list.Item {
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
	slices.SortFunc(fields, func(a, b namedField) int {
		return cmp.Or(
			cmp.Compare(a.field.Order, b.field.Order),
			cmp.Compare(a.name, b.name),
		)
	})
	items := make([]list.Item, 0, len(fields))
	for _, field := range fields {
		preview, _ := textutil.FromHTML(field.field.Value)
		items = append(items, listItem{title: field.name, description: preview, value: field.name})
	}
	return items
}
