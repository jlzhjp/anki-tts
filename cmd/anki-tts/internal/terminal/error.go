// Package terminal provides shared terminal UI infrastructure.
package terminal

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// RetryMsg asks the host to dismiss an error and rerun its command.
type RetryMsg struct{ Cmd tea.Cmd }

// DismissErrorMsg asks the host to dismiss its current error overlay.
type DismissErrorMsg struct{}

// ErrorScreen renders failures shared by every workflow step.
type ErrorScreen struct {
	err   error
	retry tea.Cmd
}

// NewErrorScreen constructs an error overlay with an optional retry command.
func NewErrorScreen(err error, retry tea.Cmd) *ErrorScreen {
	return &ErrorScreen{err: err, retry: retry}
}

// Init implements tea.Model.
func (s *ErrorScreen) Init() tea.Cmd { return nil }

// SetSize updates the screen size. ErrorScreen does not require dimensions.
func (s *ErrorScreen) SetSize(int, int) {}

// Filtering reports whether the screen is accepting filtered text input.
func (s *ErrorScreen) Filtering() bool { return false }

// Update handles retry and dismissal keys.
func (s *ErrorScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "enter":
			if s.retry != nil {
				return s, func() tea.Msg { return RetryMsg{Cmd: s.retry} }
			}
		case "esc":
			return s, func() tea.Msg { return DismissErrorMsg{} }
		}
	}
	return s, nil
}

// View renders the error and its available actions.
func (s *ErrorScreen) View() tea.View {
	help := "Esc: back  q: quit"
	if s.retry != nil {
		help = "Enter: retry  " + help
	}
	return tea.NewView(fmt.Sprintf("Error: %v\n\n%s\n", s.err, help))
}
