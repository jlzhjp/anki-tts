package interactive

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"testing"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/anki"
)

func TestInteractiveWorkflowSkipsConfiguredStagesAndLoopsNotes(t *testing.T) {
	app := &workflowApplication{services: []string{"openrouter"}}
	client := &scriptedClient{}
	client.prompt = func(screen screen, _ display) (any, error) {
		client.screens = append(client.screens, screen)
		switch screen.(type) {
		case *noteScreen:
			if len(client.screens) == 1 {
				return workflowTestNote(), nil
			}
			return nil, context.Canceled
		case *noteAudioGenerationScreen:
			return ankitts.GenerateResult{Filename: "voice.mp3"}, nil
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := Run(
		t.Context(),
		client,
		app,
		Options{
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
	client.prompt = func(screen screen, _ display) (any, error) {
		switch screen.(type) {
		case *noteScreen:
			sequence = append(sequence, "note")
			if len(sequence) == 1 {
				return workflowTestNote(), nil
			}
			return nil, context.Canceled
		case *sourceFieldScreen:
			sequence = append(sequence, "source")
			if len(sequence) == 2 {
				return "Front", nil
			}
			return nil, errBack
		case *destinationFieldScreen:
			sequence = append(sequence, "destination")
			if len(sequence) == 3 {
				return "Audio", nil
			}
			return nil, errBack
		case *ttsServiceScreen:
			sequence = append(sequence, "service")
			return nil, errBack
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := Run(
		t.Context(),
		client,
		app,
		Options{
			Yes: true,
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
	client.prompt = func(screen screen, _ display) (any, error) {
		switch screen.(type) {
		case *noteScreen:
			sequence = append(sequence, "note")
			if len(sequence) == 1 {
				return workflowTestNote(), nil
			}
			return nil, context.Canceled
		case *sourceFieldScreen:
			sequence = append(sequence, "source")
			if len(sequence) == 2 {
				return "Front", nil
			}
			return nil, errBack
		case *ttsServiceScreen:
			sequence = append(sequence, "service")
			return nil, errBack
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := Run(
		t.Context(),
		client,
		app,
		Options{
			ToField: "Audio",
			Yes:     true,
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
	client.prompt = func(screen screen, _ display) (any, error) {
		switch screen.(type) {
		case *noteScreen:
			if notes == cycles {
				return nil, context.Canceled
			}
			notes++
			return workflowTestNote(), nil
		case *noteAudioGenerationScreen:
			return ankitts.GenerateResult{Filename: "voice.mp3"}, nil
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	err := Run(
		t.Context(),
		client,
		app,
		Options{
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
	err := Run(
		t.Context(),
		&scriptedClient{},
		&workflowApplication{services: []string{"openrouter"}},
		Options{Service: "missing"},
	)
	if err == nil {
		t.Fatal("expected an unknown-service error")
	}
}

type scriptedClient struct {
	prompt  func(screen, display) (any, error)
	screens []screen
}

func (c *scriptedClient) Prompt(
	_ context.Context,
	screen screen,
	display display,
) (any, error) {
	return c.prompt(screen, display)
}

type workflowApplication struct {
	services []string
}

func (*workflowApplication) SearchNotes(
	context.Context,
	ankitts.NoteQuery,
) (ankitts.NoteSelection, error) {
	return ankitts.NoteSelection{}, nil
}

func (*workflowApplication) Notes(
	context.Context,
	ankitts.NoteSelection,
	ankitts.NoteLoadOptions,
) iter.Seq[ankitts.NoteResult] {
	return ankitts.NoteResults()
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
