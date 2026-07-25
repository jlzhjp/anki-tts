package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/step"
	"jlzhjp.dev/anki-tts/pipeline"
)

func TestBatchWorkflowComposesConfirmationExecutionAndSummary(t *testing.T) {
	app, ankiClient, selection := preparedBatchWorkflow(t, true)
	failure := errors.New("generation failed")
	client := &scriptedClient{}
	var sequence []string
	var generationDisplay step.Display
	client.prompt = func(screen step.Screen, display step.Display) (any, error) {
		switch screen := screen.(type) {
		case *step.BatchConfirmationScreen:
			if len(sequence) == 0 && ankiClient.notesInfoCount() != 0 {
				t.Fatal("note details loaded before initial confirmation")
			}
			sequence = append(sequence, "confirmation")
			return true, nil
		case *batchPreparationScreen:
			sequence = append(sequence, "preparation")
			return screen.prepare()
		case *step.BatchGenerationScreen:
			sequence = append(sequence, "generation")
			generationDisplay = display
			return step.BatchOutcome{
				Result: ankitts.BatchResult{
					Items: []ankitts.ItemResult{
						{NoteID: 42, Err: failure},
					},
				},
			}, nil
		default:
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
	}

	result := runBatchWorkflow(
		context.Background(),
		client,
		app,
		runOptions{FromField: "Front", ToField: "Audio", Service: "Test"},
		selection,
	)

	want := []string{"confirmation", "preparation", "confirmation", "generation"}
	if fmt.Sprint(sequence) != fmt.Sprint(want) {
		t.Fatalf("sequence=%v, want %v", sequence, want)
	}
	if !generationDisplay.CancelIsError {
		t.Fatal("batch execution cancellation was not marked as an error")
	}
	if !result.errorPresented || !errors.Is(result.err, failure) {
		t.Fatalf("result=%+v", result)
	}
}

func TestBatchWorkflowYesSkipsConfirmations(t *testing.T) {
	app, _, selection := preparedBatchWorkflow(t, true)
	client := &scriptedClient{}
	client.prompt = func(screen step.Screen, display step.Display) (any, error) {
		if preparation, ok := screen.(*batchPreparationScreen); ok {
			return preparation.prepare()
		}
		if _, ok := screen.(*step.BatchGenerationScreen); !ok {
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
		return step.BatchOutcome{
			Result: ankitts.BatchResult{
				Items: []ankitts.ItemResult{{NoteID: 42}},
			},
		}, nil
	}

	result := runBatchWorkflow(
		context.Background(),
		client,
		app,
		runOptions{FromField: "Front", ToField: "Audio", Service: "Test", Yes: true},
		selection,
	)

	if result.err != nil || result.errorPresented {
		t.Fatalf("result=%+v", result)
	}
}

func TestBatchWorkflowRejectionSkipsNoteDetails(t *testing.T) {
	app, ankiClient, selection := preparedBatchWorkflow(t, false)
	client := &scriptedClient{prompt: func(screen step.Screen, _ step.Display) (any, error) {
		if _, ok := screen.(*step.BatchConfirmationScreen); !ok {
			return nil, fmt.Errorf("unexpected screen %T", screen)
		}
		return false, nil
	}}
	result := runBatchWorkflow(
		context.Background(),
		client,
		app,
		runOptions{FromField: "Front", ToField: "Audio", Service: "Test"},
		selection,
	)
	if result.err != nil || ankiClient.notesInfoCount() != 0 {
		t.Fatalf("result=%+v notesInfoCalls=%d", result, ankiClient.notesInfoCount())
	}
}

func preparedBatchWorkflow(
	t *testing.T,
	overwrite bool,
) (*ankitts.Application, *batchAnki, ankitts.NoteSelection) {
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
	if err := services.Add("Test", batchTTS{}); err != nil {
		t.Fatal(err)
	}
	ankiClient := &batchAnki{notes: notes}
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
