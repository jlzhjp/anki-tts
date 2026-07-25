package main

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/step"
)

// batchPreparationScreen keeps terminal feedback responsive while note details
// are loaded and validated before any generation side effects can begin.
type batchPreparationScreen struct {
	prepare func() (ankitts.Plan, error)
}

func (s *batchPreparationScreen) Init() tea.Cmd { return s.execute() }

func (s *batchPreparationScreen) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return s, nil
}

func (*batchPreparationScreen) View() tea.View {
	return tea.NewView(
		"Loading note details and validating the selection…\n\nq/ctrl+c to cancel\n",
	)
}

func (*batchPreparationScreen) Filtering() bool    { return false }
func (*batchPreparationScreen) BackDisabled() bool { return true }
func (*batchPreparationScreen) Retry() tea.Cmd     { return nil }

func (s *batchPreparationScreen) execute() tea.Cmd {
	return func() tea.Msg {
		plan, err := s.prepare()
		if err != nil {
			return step.FailedMsg{Err: err, Retry: s.execute()}
		}
		return step.CompletedMsg{Value: plan}
	}
}

func prepareTerminalBatch(
	ctx context.Context,
	client step.Client,
	prepare func() (ankitts.Plan, error),
) (ankitts.Plan, error) {
	value, err := client.Prompt(
		ctx,
		&batchPreparationScreen{prepare: prepare},
		step.Display{},
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
