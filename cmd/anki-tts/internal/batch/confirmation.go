package batch

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// confirmationScreen displays the notes included in a batch operation.
type confirmationScreen struct {
	noteIDs   []int64
	overwrite bool
	height    int
	sized     bool
	altScreen bool
	offset    int
}

// confirm presents or resumes a batch confirmation screen.
func confirm(
	ctx context.Context,
	client client,
	noteIDs []int64,
	overwrite bool,
	previous *confirmationScreen,
	display display,
) (bool, *confirmationScreen, error) {
	display.Resume = previous != nil
	if previous == nil {
		previous = &confirmationScreen{
			noteIDs:   append([]int64(nil), noteIDs...),
			overwrite: overwrite,
			height:    24,
		}
	}
	value, err := prompt[bool](ctx, client, previous, display)
	return value, previous, err
}

func (s *confirmationScreen) Init() tea.Cmd { return nil }

func (s *confirmationScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
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

func (s *confirmationScreen) View() tea.View {
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

func (s *confirmationScreen) SetSize(_ int, height int) {
	s.height = height
	if !s.sized {
		s.sized = true
		s.altScreen = s.rowCount()+5 > height
	}
	s.clampOffset()
}

func (*confirmationScreen) Filtering() bool { return false }

func (s *confirmationScreen) rowCount() int {
	return len(s.noteIDs)
}

func (s *confirmationScreen) visibleRows(rows []string) []string {
	if !s.altScreen || len(rows) <= s.visibleRowCount() {
		return rows
	}
	start := min(s.offset, max(0, len(rows)-s.visibleRowCount()))
	return rows[start:min(len(rows), start+s.visibleRowCount())]
}

func (s *confirmationScreen) visibleRowCount() int {
	return max(1, s.height-5)
}

func (s *confirmationScreen) clampOffset() {
	limit := max(0, s.rowCount()-s.visibleRowCount())
	s.offset = min(max(s.offset, 0), limit)
}
