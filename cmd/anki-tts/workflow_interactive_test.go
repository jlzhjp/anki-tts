package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/step"
)

func TestInteractiveWorkflowSkipsConfiguredStagesAndLoopsNotes(t *testing.T) {
	app := &workflowApplication{services: []string{"openrouter"}}
	client := &scriptedClient{}
	client.prompt = func(screen step.Screen, _ step.Display) (any, error) {
		client.screens = append(client.screens, screen)
		switch screen.(type) {
		case *step.NoteScreen:
			if len(client.screens) == 1 {
				return workflowTestNote(), nil
			}
			return nil, context.Canceled
		case *step.NoteAudioGenerationScreen:
			return ankitts.GenerateResult{Filename: "voice.mp3"}, nil
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := runInteractiveWorkflow(
		context.Background(),
		client,
		app,
		runOptions{
			Selector:  ankitts.NoteSelector{Decks: []string{"Japanese"}},
			FromField: "Front",
			ToField:   "Audio",
			Service:   "openrouter",
			Yes:       true,
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("workflow error=%v", err)
	}
	if len(client.screens) != 3 {
		t.Fatalf("screens=%d, want note, generation, refreshed note", len(client.screens))
	}
	if client.screens[0] != client.screens[2] {
		t.Fatal("note screen was not preserved for refresh")
	}
}

func TestInteractiveWorkflowBackUnwindsVisibleStages(t *testing.T) {
	app := &workflowApplication{services: []string{"openrouter"}}
	client := &scriptedClient{}
	var sequence []string
	client.prompt = func(screen step.Screen, _ step.Display) (any, error) {
		switch screen.(type) {
		case *step.NoteScreen:
			sequence = append(sequence, "note")
			if len(sequence) == 1 {
				return workflowTestNote(), nil
			}
			return nil, context.Canceled
		case *step.SourceFieldScreen:
			sequence = append(sequence, "source")
			if len(sequence) == 2 {
				return "Front", nil
			}
			return nil, step.ErrBack
		case *step.DestinationFieldScreen:
			sequence = append(sequence, "destination")
			if len(sequence) == 3 {
				return "Audio", nil
			}
			return nil, step.ErrBack
		case *step.TTSServiceScreen:
			sequence = append(sequence, "service")
			return nil, step.ErrBack
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := runInteractiveWorkflow(
		context.Background(),
		client,
		app,
		runOptions{
			Selector: ankitts.NoteSelector{Decks: []string{"Japanese"}},
			Yes:      true,
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("workflow error=%v", err)
	}
	want := []string{
		"note",
		"source",
		"destination",
		"service",
		"destination",
		"source",
		"note",
	}
	if fmt.Sprint(sequence) != fmt.Sprint(want) {
		t.Fatalf("sequence=%v, want %v", sequence, want)
	}
}

func TestInteractiveWorkflowBackSkipsConfiguredStages(t *testing.T) {
	app := &workflowApplication{services: []string{"openrouter"}}
	client := &scriptedClient{}
	var sequence []string
	client.prompt = func(screen step.Screen, _ step.Display) (any, error) {
		switch screen.(type) {
		case *step.NoteScreen:
			sequence = append(sequence, "note")
			if len(sequence) == 1 {
				return workflowTestNote(), nil
			}
			return nil, context.Canceled
		case *step.SourceFieldScreen:
			sequence = append(sequence, "source")
			if len(sequence) == 2 {
				return "Front", nil
			}
			return nil, step.ErrBack
		case *step.TTSServiceScreen:
			sequence = append(sequence, "service")
			return nil, step.ErrBack
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := runInteractiveWorkflow(
		context.Background(),
		client,
		app,
		runOptions{
			Selector: ankitts.NoteSelector{Decks: []string{"Japanese"}},
			ToField:  "Audio",
			Yes:      true,
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("workflow error=%v", err)
	}
	want := []string{"note", "source", "service", "source", "note"}
	if fmt.Sprint(sequence) != fmt.Sprint(want) {
		t.Fatalf("sequence=%v, want %v", sequence, want)
	}
}

func TestInteractiveWorkflowUsesIterativeNoteCycle(t *testing.T) {
	const cycles = 2000
	app := &workflowApplication{services: []string{"openrouter"}}
	client := &scriptedClient{}
	notes := 0
	client.prompt = func(screen step.Screen, _ step.Display) (any, error) {
		switch screen.(type) {
		case *step.NoteScreen:
			if notes == cycles {
				return nil, context.Canceled
			}
			notes++
			return workflowTestNote(), nil
		case *step.NoteAudioGenerationScreen:
			return ankitts.GenerateResult{Filename: "voice.mp3"}, nil
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := runInteractiveWorkflow(
		context.Background(),
		client,
		app,
		runOptions{
			Selector:  ankitts.NoteSelector{Decks: []string{"Japanese"}},
			FromField: "Front",
			ToField:   "Audio",
			Service:   "openrouter",
			Yes:       true,
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("workflow error=%v", err)
	}
	if notes != cycles {
		t.Fatalf("processed cycles=%d, want %d", notes, cycles)
	}
}

func TestInteractiveWorkflowRejectsConfiguredUnknownService(t *testing.T) {
	err := runInteractiveWorkflow(
		context.Background(),
		&scriptedClient{},
		&workflowApplication{services: []string{"openrouter"}},
		runOptions{Service: "missing"},
	)
	if err == nil {
		t.Fatal("expected an unknown-service error")
	}
}

type scriptedClient struct {
	prompt  func(step.Screen, step.Display) (any, error)
	screens []step.Screen
}

func (c *scriptedClient) Prompt(
	_ context.Context,
	screen step.Screen,
	display step.Display,
) (any, error) {
	return c.prompt(screen, display)
}

type workflowApplication struct {
	services []string
}

func (*workflowApplication) ListDecks(context.Context) ([]string, error) {
	return nil, nil
}
func (*workflowApplication) SelectNotes(
	context.Context,
	ankitts.NoteSelector,
) ([]anki.Note, error) {
	return nil, nil
}
func (a *workflowApplication) ServiceNames() []string {
	return a.services
}
func (*workflowApplication) HasAudioProcessors() bool { return false }
func (*workflowApplication) Prepare(
	ankitts.GenerationRequest,
) (ankitts.Plan, error) {
	return ankitts.Plan{}, nil
}
func (*workflowApplication) Execute(
	context.Context,
	ankitts.Plan,
	ankitts.ExecuteOptions,
) (ankitts.BatchResult, error) {
	return ankitts.BatchResult{}, nil
}

func workflowTestNote() anki.Note {
	return anki.Note{
		ID:        42,
		ModelName: "Basic",
		Fields: map[string]anki.Field{
			"Front": {Value: "Hello", Order: 0},
			"Audio": {Value: "", Order: 1},
		},
	}
}
