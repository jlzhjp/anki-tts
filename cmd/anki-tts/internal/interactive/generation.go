package interactive

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/ankitts"
)

type generationApplication interface {
	HasAudioProcessors() bool
	Prepare(ankitts.GenerationRequest) (ankitts.Plan, error)
	Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error)
}

type generationFinishedMsg struct {
	result ankitts.GenerateResult
	err    error
}

// noteAudioGenerationScreen displays progress while generating one note's audio.
type noteAudioGenerationScreen struct {
	selectionScreen
	ctx     context.Context
	app     generationApplication
	request ankitts.GenerationRequest
}

func newNoteAudioGenerationScreen(
	ctx context.Context,
	app generationApplication,
	request ankitts.GenerationRequest,
) *noteAudioGenerationScreen {
	title := "Generating voice with " + request.Service
	if app.HasAudioProcessors() {
		title = "Generating and transforming audio with " + request.Service
	}
	screen := &noteAudioGenerationScreen{
		selectionScreen: newSelectionScreen(title, nil),
		ctx:             ctx,
		app:             app,
		request:         request,
	}
	screen.busy = true
	return screen
}

// generateNoteAudio runs generation while presenting retryable progress.
func generateNoteAudio(
	ctx context.Context,
	client client,
	app generationApplication,
	request ankitts.GenerationRequest,
	display display,
) (ankitts.GenerateResult, error) {
	screen := newNoteAudioGenerationScreen(ctx, app, request)
	return prompt[ankitts.GenerateResult](ctx, client, screen, display)
}

func (s *noteAudioGenerationScreen) Init() tea.Cmd {
	return tea.Batch(s.list.StartSpinner(), s.generate())
}

func (s *noteAudioGenerationScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(generationFinishedMsg); ok {
		s.list.StopSpinner()
		if msg.err != nil {
			return s, fail(msg.err, s.generate())
		}
		return s, complete(msg.result)
	}
	return s, s.update(message)
}

func (s *noteAudioGenerationScreen) BackDisabled() bool { return true }

func (s *noteAudioGenerationScreen) Retry() tea.Cmd {
	s.busy = true
	return s.list.StartSpinner()
}

func (s *noteAudioGenerationScreen) generate() tea.Cmd {
	return func() tea.Msg {
		plan, err := s.app.Prepare(s.request)
		if err != nil {
			return generationFinishedMsg{err: err}
		}
		batch, err := s.app.Execute(s.ctx, plan, ankitts.ExecuteOptions{})
		if err != nil {
			return generationFinishedMsg{err: err}
		}
		if len(batch.Items) != 1 {
			return generationFinishedMsg{
				err: fmt.Errorf("generation pipeline returned %d results, want 1", len(batch.Items)),
			}
		}
		item := batch.Items[0]
		return generationFinishedMsg{result: item.Result, err: item.Err}
	}
}
