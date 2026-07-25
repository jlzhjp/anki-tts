package batch

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/pipeline"
)

func TestBatchWorkflowComposesConfirmationExecutionAndSummary(t *testing.T) {
	app, ankiClient, selection := preparedWorkflow(t, true)
	failure := errors.New("generation failed")
	client := &scriptedClient{}
	var sequence []string
	var generationdisplay display
	client.prompt = func(screen screen, display display) (any, error) {
		switch screen := screen.(type) {
		case *confirmationScreen:
			if len(sequence) == 0 && ankiClient.notesInfoCount() != 0 {
				t.Fatal("note details loaded before initial confirmation")
			}
			sequence = append(sequence, "confirmation")
			return true, nil
		case *preparationScreen:
			sequence = append(sequence, "preparation")
			return screen.prepare()
		case *generationScreen:
			sequence = append(sequence, "generation")
			generationdisplay = display
			return outcome{
				result: ankitts.BatchResult{
					Items: []ankitts.ItemResult{
						{NoteID: 42, Err: failure},
					},
				},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	result := Run(
		context.Background(),
		client,
		app,
		Options{FromField: "Front", ToField: "Audio", Service: "Test"},
		selection,
	)

	want := []string{"confirmation", "preparation", "confirmation", "generation"}
	if fmt.Sprint(sequence) != fmt.Sprint(want) {
		t.Fatalf("sequence=%v, want %v", sequence, want)
	}
	if !generationdisplay.CancelIsError {
		t.Fatal("batch execution cancellation was not marked as an error")
	}
	if !result.ErrorPresented || !errors.Is(result.Err, failure) {
		t.Fatalf("result=%+v", result)
	}
}

func TestBatchWorkflowYesSkipsConfirmations(t *testing.T) {
	app, _, selection := preparedWorkflow(t, true)
	client := &scriptedClient{}
	client.prompt = func(screen screen, _ display) (any, error) {
		if preparation, ok := screen.(*preparationScreen); ok {
			return preparation.prepare()
		}
		if _, ok := screen.(*generationScreen); !ok {
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
		return outcome{
			result: ankitts.BatchResult{
				Items: []ankitts.ItemResult{{NoteID: 42}},
			},
		}, nil
	}

	result := Run(
		context.Background(),
		client,
		app,
		Options{FromField: "Front", ToField: "Audio", Service: "Test", Yes: true},
		selection,
	)

	if result.Err != nil || result.ErrorPresented {
		t.Fatalf("result=%+v", result)
	}
}

func TestBatchWorkflowRejectionSkipsNoteDetails(t *testing.T) {
	app, ankiClient, selection := preparedWorkflow(t, false)
	client := &scriptedClient{prompt: func(screen screen, _ display) (any, error) {
		if _, ok := screen.(*confirmationScreen); !ok {
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
		return false, nil
	}}
	result := Run(
		context.Background(),
		client,
		app,
		Options{FromField: "Front", ToField: "Audio", Service: "Test"},
		selection,
	)
	if result.Err != nil || ankiClient.notesInfoCount() != 0 {
		t.Fatalf("result=%+v notesInfoCalls=%d", result, ankiClient.notesInfoCount())
	}
}

func preparedWorkflow(
	t *testing.T,
	overwrite bool,
) (*ankitts.Application, *fakeAnkiClient, ankitts.NoteSelection) {
	t.Helper()
	destination := ""
	if overwrite {
		destination = "old"
	}
	notes := []anki.Note{
		{
			ID:        42,
			ModelName: "Basic",
			Fields: map[string]anki.Field{
				"Front": {Value: "hello"},
				"Audio": {Value: destination},
			},
		},
	}
	services := ankitts.NewServiceContainer()
	if err := services.Add("Test", fakeTTS{}); err != nil {
		t.Fatal(err)
	}
	ankiClient := &fakeAnkiClient{notes: notes}
	app, err := ankitts.New(
		ankiClient,
		services,
		nil,
		pipeline.Config{
			"Test": pipeline.DefaultStageConfig(1),
			"anki": pipeline.DefaultStageConfig(1),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return app, ankiClient, ankitts.NoteSelection{IDs: []int64{42}}
}
