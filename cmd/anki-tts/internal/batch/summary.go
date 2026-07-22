package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ankitts "jlzhjp.dev/anki-tts"
)

func (m batchModel) summaryView() string {
	succeeded := 0
	var failures []ankitts.ItemResult
	for _, item := range m.result.Items {
		if item.Err == nil {
			succeeded++
		} else {
			failures = append(failures, item)
		}
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Anki TTS\n\nSummary: %d succeeded, %d failed.\n", succeeded, len(failures))
	for _, item := range failures {
		fmt.Fprintf(&builder, "  note %d: %s\n", item.NoteID, red(item.Err.Error()))
	}
	if m.executionErr != nil && !errors.Is(m.executionErr, context.Canceled) {
		fmt.Fprintf(&builder, "\n%s\n", red(m.executionErr.Error()))
	}
	return builder.String()
}
