package step

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts"
)

// BatchConfirmationScreen displays the notes included in a batch operation.
type BatchConfirmationScreen struct {
	notes          []ankitts.PlannedNote
	overwritesOnly bool
	width          int
	height         int
	sized          bool
	altScreen      bool
	offset         int
}

// ConfirmBatch presents or resumes a batch confirmation screen.
func ConfirmBatch(
	ctx context.Context,
	client Client,
	notes []ankitts.PlannedNote,
	overwritesOnly bool,
	previous *BatchConfirmationScreen,
	display Display,
) (bool, *BatchConfirmationScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = &BatchConfirmationScreen{
			notes:          notes,
			overwritesOnly: overwritesOnly,
			width:          80,
			height:         24,
		}
	}
	value, err := prompt[bool](ctx, client, previous, display)
	return value, previous, err
}

func (s *BatchConfirmationScreen) Init() tea.Cmd { return nil }

func (s *BatchConfirmationScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "up", "k":
			if s.altScreen && s.offset > 0 {
				s.offset--
			}
		case "down", "j":
			if s.altScreen {
				s.offset++
				s.clampOffset()
			}
		case "y", "enter":
			return s, complete(true)
		case "n":
			return s, complete(false)
		}
	}
	return s, nil
}

func (s *BatchConfirmationScreen) View() tea.View {
	title := fmt.Sprintf("Generate audio for %d selected note(s)?", len(s.notes))
	if s.overwritesOnly {
		title = red(fmt.Sprintf(
			"Replace %d non-empty destination field(s)?",
			s.overwriteCount(),
		))
	}

	rows := make([]string, 0, len(s.notes))
	for _, note := range s.notes {
		if s.overwritesOnly && !note.WillOverwrite {
			continue
		}
		status := "empty destination"
		if note.WillOverwrite {
			status = red("WILL OVERWRITE")
		}
		rows = append(rows, fmt.Sprintf(
			"  %d  %-20s  %s  [%s]",
			note.Note.ID,
			truncate(note.Note.ModelName, 20),
			compactPreview(note.SourceText, max(12, s.width-45)),
			status,
		))
	}
	rows = s.visibleRows(rows)

	help := "y/enter confirm • n/esc/q cancel"
	if s.altScreen {
		help = "↑/↓ scroll • " + help
	}
	view := tea.NewView(
		title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + help + "\n",
	)
	view.AltScreen = s.altScreen
	return view
}

func (s *BatchConfirmationScreen) SetSize(width, height int) {
	s.width, s.height = width, height
	if !s.sized {
		s.sized = true
		s.altScreen = s.rowCount()+5 > height
	}
	s.clampOffset()
}

func (*BatchConfirmationScreen) Filtering() bool { return false }

func (s *BatchConfirmationScreen) overwriteCount() int {
	count := 0
	for _, note := range s.notes {
		if note.WillOverwrite {
			count++
		}
	}
	return count
}

func (s *BatchConfirmationScreen) rowCount() int {
	if s.overwritesOnly {
		return s.overwriteCount()
	}
	return len(s.notes)
}

func (s *BatchConfirmationScreen) visibleRows(rows []string) []string {
	if !s.altScreen || len(rows) <= s.visibleRowCount() {
		return rows
	}
	start := min(s.offset, max(0, len(rows)-s.visibleRowCount()))
	return rows[start:min(len(rows), start+s.visibleRowCount())]
}

func (s *BatchConfirmationScreen) visibleRowCount() int {
	return max(1, s.height-5)
}

func (s *BatchConfirmationScreen) clampOffset() {
	limit := max(0, s.rowCount()-s.visibleRowCount())
	s.offset = min(max(s.offset, 0), limit)
}

func compactPreview(value string, limit int) string {
	return truncate(strings.Join(strings.Fields(value), " "), limit)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func red(value string) string {
	return "\x1b[1;31m" + value + "\x1b[0m"
}
