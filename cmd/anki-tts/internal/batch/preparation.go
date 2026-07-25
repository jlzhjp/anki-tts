package batch

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"jlzhjp.dev/anki-tts"
)

// preparationScreen keeps terminal feedback responsive while note details
// are loaded and validated before any generation side effects can begin.
type preparationScreen struct {
	prepare func() (ankitts.Plan, error)
}

func (s *preparationScreen) Init() tea.Cmd { return s.execute() }

func (s *preparationScreen) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return s, nil
}

func (*preparationScreen) View() tea.View {
	return tea.NewView(
		"Loading note details and validating the selection…\n\nq/ctrl+c to cancel\n",
	)
}

func (*preparationScreen) Filtering() bool    { return false }
func (*preparationScreen) BackDisabled() bool { return true }
func (*preparationScreen) Retry() tea.Cmd     { return nil }

func (s *preparationScreen) execute() tea.Cmd {
	return func() tea.Msg {
		plan, err := s.prepare()
		if err != nil {
			return failedMsg{Err: err, Retry: s.execute()}
		}
		return completedMsg{Value: plan}
	}
}

func prepareTerminal(
	ctx context.Context,
	client client,
	prepare func() (ankitts.Plan, error),
) (ankitts.Plan, error) {
	value, err := client.Prompt(
		ctx,
		&preparationScreen{prepare: prepare},
		display{},
	)
	if err != nil {
		return ankitts.Plan{}, err
	}
	plan, ok := value.(ankitts.Plan)
	if !ok {
		return ankitts.Plan{}, fmt.Errorf(
			"batch preparation returned %T, expected ankitts.Plan",
			value,
		)
	}
	return plan, nil
}
