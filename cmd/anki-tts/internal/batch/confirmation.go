package batch

import (
	"fmt"
	"strings"
)

func (m batchModel) confirmationView(overwritesOnly bool) string {
	title := fmt.Sprintf("Generate audio for %d selected note(s)?", len(m.notes))
	if overwritesOnly {
		title = red(fmt.Sprintf("Replace %d non-empty destination field(s)?", m.overwriteCount()))
	}
	rows := make([]string, 0, len(m.notes))
	for _, note := range m.notes {
		if overwritesOnly && !note.WillOverwrite {
			continue
		}
		preview := compactPreview(note.SourceText, max(12, m.width-45))
		status := "empty destination"
		if note.WillOverwrite {
			status = red("WILL OVERWRITE")
		}
		rows = append(rows, fmt.Sprintf("  %d  %-20s  %s  [%s]", note.Note.ID, truncate(note.Note.ModelName, 20), preview, status))
	}
	rows = m.visibleRows(rows)
	body := strings.Join(rows, "\n")
	help := "y/enter confirm • n/esc/q cancel"
	if m.altScreen {
		help = "↑/↓ scroll • " + help
	}
	return "Anki TTS\n\n" + title + "\n\n" + body + "\n\n" + help + "\n"
}

func (m batchModel) confirmationRows() int { return len(m.notes) + 7 }

func (m batchModel) overwriteCount() int {
	count := 0
	for _, note := range m.notes {
		if note.WillOverwrite {
			count++
		}
	}
	return count
}

func (m batchModel) visibleRows(rows []string) []string {
	if !m.altScreen || len(rows) <= m.visibleRowCount() {
		return rows
	}
	start := min(m.offset, max(0, len(rows)-m.visibleRowCount()))
	return rows[start:min(len(rows), start+m.visibleRowCount())]
}

func (m batchModel) visibleRowCount() int { return max(1, m.height-7) }
