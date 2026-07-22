package batch

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func (m batchModel) progressView() string {
	active := make([]int, 0, len(m.progress))
	failedNotes := make([]int, 0)
	succeeded, failed := 0, 0
	for index, state := range m.progress {
		if state.done {
			succeeded++
		} else if state.err != nil && !state.working {
			failed++
			failedNotes = append(failedNotes, index)
		} else if state.working {
			active = append(active, index)
		}
	}
	sort.Ints(active)
	sort.Ints(failedNotes)
	var builder strings.Builder
	pending := len(m.notes) - succeeded - failed - len(active)
	fmt.Fprintf(&builder, "Anki TTS · Processing %d notes\n\nActive %d · Pending %d · Completed %d/%d · Failed %d\n", len(m.notes), len(active), pending, succeeded, len(m.notes), failed)
	for _, index := range active {
		state := m.progress[index]
		fmt.Fprintf(&builder, "\n  note %d · %s", m.notes[index].Note.ID, state.operation)
		if !state.retryAt.IsZero() {
			remaining := max(time.Until(state.retryAt), 0)
			fmt.Fprintf(&builder, " · retry %d/%d in %s", state.attempt, state.maxAttempts, remaining.Round(100*time.Millisecond))
		}
		if state.err != nil {
			fmt.Fprintf(&builder, "\n    %s", red(state.err.Error()))
		}
	}
	if len(failedNotes) > 0 {
		builder.WriteString("\n\nErrors:")
		for _, index := range failedNotes {
			state := m.progress[index]
			fmt.Fprintf(&builder, "\n  note %d · %s: %s", m.notes[index].Note.ID, state.operation, red(state.err.Error()))
		}
	}
	builder.WriteString("\n\nq/ctrl+c to cancel\n")
	return builder.String()
}
