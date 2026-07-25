package step

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// BatchConfirmationScreen displays the notes included in a batch operation.
type BatchConfirmationScreen struct {
	noteIDs   []int64
	overwrite bool
	height    int
	sized     bool
	altScreen bool
	offset    int
}

// ConfirmBatch presents or resumes a batch confirmation screen.
func ConfirmBatch(
	ctx context.Context,
	client Client,
	noteIDs []int64,
	overwrite bool,
	previous *BatchConfirmationScreen,
	display Display,
) (bool, *BatchConfirmationScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = &BatchConfirmationScreen{
			noteIDs:   append([]int64(nil), noteIDs...),
			overwrite: overwrite,
			height:    24,
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
	title := fmt.Sprintf("Generate audio for %d selected note(s)?", len(s.noteIDs))
	if s.overwrite {
		title = red(fmt.Sprintf(
			"Replace %d non-empty destination field(s)?",
			len(s.noteIDs),
		))
	}

	rows := make([]string, 0, len(s.noteIDs))
	for _, noteID := range s.noteIDs {
		rows = append(rows, fmt.Sprintf("  %d", noteID))
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

func (s *BatchConfirmationScreen) SetSize(_ int, height int) {
	s.height = height
	if !s.sized {
		s.sized = true
		s.altScreen = s.rowCount()+5 > height
	}
	s.clampOffset()
}

func (*BatchConfirmationScreen) Filtering() bool { return false }

func (s *BatchConfirmationScreen) rowCount() int {
	return len(s.noteIDs)
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

func red(value string) string {
	return "\x1b[1;31m" + value + "\x1b[0m"
}
