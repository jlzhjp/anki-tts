package interactive

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/internal/interactive/step"
)

func TestScreenHostInstallsCompletesAndResizesScreen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan screenRequest)
	host := newScreenHost(ctx, cancel, requests, make(chan error))
	screen := &fakeScreen{}
	reply := make(chan screenOutcome, 1)

	updated, _ := host.Update(screenRequestedMsg{request: screenRequest{
		screen: screen,
		reply:  reply,
		display: step.Display{
			Step:    3,
			Context: "Deck: Japanese",
		},
	}})
	host = updated.(*screenHost)
	if host.active != screen {
		t.Fatal("screen was not installed")
	}

	host.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if screen.width != 100 || screen.height >= 30 {
		t.Fatalf("screen size=%dx%d", screen.width, screen.height)
	}

	host.Update(step.CompletedMsg{Value: "Front"})
	outcome := <-reply
	if outcome.value != "Front" || outcome.back || host.active != nil {
		t.Fatalf("outcome=%+v active=%T", outcome, host.active)
	}
}

func TestScreenHostReturnsBackToClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := newScreenHost(ctx, cancel, make(chan screenRequest), make(chan error))
	screen := &fakeScreen{}
	reply := make(chan screenOutcome, 1)
	host.Update(screenRequestedMsg{request: screenRequest{screen: screen, reply: reply}})

	host.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if outcome := <-reply; !outcome.back {
		t.Fatalf("outcome=%+v", outcome)
	}
}

func TestScreenClientPromptReturnsValueAndBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan screenRequest)
	client := screenClient{requests: requests}

	values := make(chan any, 1)
	errs := make(chan error, 1)
	go func() {
		value, err := client.Prompt(ctx, &fakeScreen{}, step.Display{Step: 1})
		values <- value
		errs <- err
	}()
	request := <-requests
	request.reply <- screenOutcome{value: "selected"}
	if value := <-values; value != "selected" {
		t.Fatalf("value=%v", value)
	}
	if err := <-errs; err != nil {
		t.Fatalf("error=%v", err)
	}

	go func() {
		_, err := client.Prompt(ctx, &fakeScreen{}, step.Display{Step: 2})
		errs <- err
	}()
	request = <-requests
	request.reply <- screenOutcome{back: true}
	if err := <-errs; !errors.Is(err, step.ErrBack) {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkflowSkipsConfiguredSelectionSteps(t *testing.T) {
	ctx := context.Background()
	app := &fakeApplication{services: []string{"openrouter"}}
	client := &scriptedClient{}
	client.prompt = func(screen step.Screen, _ step.Display) (any, error) {
		client.screens = append(client.screens, screen)
		switch screen.(type) {
		case *step.NoteScreen:
			if len(client.screens) == 1 {
				return testNote(), nil
			}
			return nil, context.Canceled
		case *step.NoteAudioGenerationScreen:
			return ankitts.GenerateResult{Filename: "voice.mp3"}, nil
		default:
			return nil, errors.New("unexpected screen")
		}
	}

	err := runWorkflow(ctx, client, app, Options{
		Selector:  ankitts.NoteSelector{Decks: []string{"Japanese"}},
		FromField: "Front",
		ToField:   "Audio",
		Service:   "openrouter",
		Yes:       true,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("workflow error=%v", err)
	}
	if len(client.screens) != 3 {
		t.Fatalf("screens=%d, want note, generation, refreshed note", len(client.screens))
	}
	if client.screens[0] != client.screens[2] {
		t.Fatal("note screen was not preserved for refresh")
	}
	if _, ok := client.screens[1].(*step.NoteAudioGenerationScreen); !ok {
		t.Fatalf("generation screen=%T", client.screens[1])
	}
}

func TestWorkflowRejectsConfiguredUnknownService(t *testing.T) {
	err := runWorkflow(
		context.Background(),
		&scriptedClient{},
		&fakeApplication{services: []string{"openrouter"}},
		Options{Service: "missing"},
	)
	if err == nil || !strings.Contains(err.Error(), `TTS service "missing" is not configured`) {
		t.Fatalf("error=%v", err)
	}
}

type fakeScreen struct {
	width  int
	height int
}

func (s *fakeScreen) Init() tea.Cmd                       { return nil }
func (s *fakeScreen) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }
func (s *fakeScreen) View() tea.View                      { return tea.NewView("screen") }
func (s *fakeScreen) SetSize(width, height int)           { s.width, s.height = width, height }
func (s *fakeScreen) Filtering() bool                     { return false }

type scriptedClient struct {
	prompt  func(step.Screen, step.Display) (any, error)
	screens []step.Screen
}

func (c *scriptedClient) Prompt(_ context.Context, screen step.Screen, display step.Display) (any, error) {
	return c.prompt(screen, display)
}

func testNote() anki.Note {
	return anki.Note{ID: 42, ModelName: "Basic", Fields: map[string]anki.Field{
		"Front": {Value: "Hello", Order: 0},
		"Audio": {Value: "", Order: 1},
	}}
}

type fakeApplication struct {
	services []string
}

func (f *fakeApplication) ListDecks(context.Context) ([]string, error) { return nil, nil }
func (f *fakeApplication) SelectNotes(context.Context, ankitts.NoteSelector) ([]anki.Note, error) {
	return nil, nil
}
func (f *fakeApplication) ServiceNames() []string   { return f.services }
func (f *fakeApplication) HasAudioProcessors() bool { return false }
func (f *fakeApplication) Prepare(ankitts.GenerationRequest) (ankitts.Plan, error) {
	return ankitts.Plan{}, nil
}
func (f *fakeApplication) Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error) {
	return ankitts.BatchResult{}, nil
}
