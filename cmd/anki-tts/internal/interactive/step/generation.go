package step

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts"
)

type GenerationApplication interface {
	HasAudioProcessors() bool
	Prepare(ankitts.GenerationRequest) (ankitts.Plan, error)
	Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error)
}

type generationFinishedMsg struct {
	result ankitts.GenerateResult
	err    error
}

// NoteAudioGenerationScreen displays progress while generating one note's audio.
type NoteAudioGenerationScreen struct {
	selectionScreen
	ctx     context.Context
	app     GenerationApplication
	request ankitts.GenerationRequest
}

func newNoteAudioGenerationScreen(
	ctx context.Context,
	app GenerationApplication,
	request ankitts.GenerationRequest,
) *NoteAudioGenerationScreen {
	title := "Generating voice with " + request.Service
	if app.HasAudioProcessors() {
		title = "Generating and transforming audio with " + request.Service
	}
	screen := &NoteAudioGenerationScreen{
		selectionScreen: newSelectionScreen(title, nil),
		ctx:             ctx,
		app:             app,
		request:         request,
	}
	screen.busy = true
	return screen
}

// GenerateNoteAudio runs generation while presenting retryable progress.
func GenerateNoteAudio(
	ctx context.Context,
	client Client,
	app GenerationApplication,
	request ankitts.GenerationRequest,
	display Display,
) (ankitts.GenerateResult, error) {
	screen := newNoteAudioGenerationScreen(ctx, app, request)
	return prompt[ankitts.GenerateResult](ctx, client, screen, display)
}

func (s *NoteAudioGenerationScreen) Init() tea.Cmd {
	return tea.Batch(s.list.StartSpinner(), s.generate())
}

func (s *NoteAudioGenerationScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := message.(generationFinishedMsg); ok {
		s.list.StopSpinner()
		if msg.err != nil {
			return s, fail(msg.err, s.generate())
		}
		return s, complete(msg.result)
	}
	return s, s.update(message)
}

func (s *NoteAudioGenerationScreen) BackDisabled() bool { return true }

func (s *NoteAudioGenerationScreen) Retry() tea.Cmd {
	s.busy = true
	return s.list.StartSpinner()
}

func (s *NoteAudioGenerationScreen) generate() tea.Cmd {
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
