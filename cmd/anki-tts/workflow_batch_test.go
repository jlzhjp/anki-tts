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
	app, plan := preparedBatchWorkflow(t, true)
	failure := errors.New("generation failed")
	client := &scriptedClient{}
	var sequence []string
	var generationDisplay step.Display
	client.prompt = func(screen step.Screen, display step.Display) (any, error) {
		switch screen.(type) {
		case *step.BatchConfirmationScreen:
			sequence = append(sequence, "confirmation")
			return true, nil
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
		runOptions{},
		plan,
	)

	want := []string{"confirmation", "confirmation", "generation"}
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
	app, plan := preparedBatchWorkflow(t, true)
	client := &scriptedClient{}
	client.prompt = func(screen step.Screen, display step.Display) (any, error) {
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
		runOptions{Yes: true},
		plan,
	)

	if result.err != nil || result.errorPresented {
		t.Fatalf("result=%+v", result)
	}
}

func preparedBatchWorkflow(
	t *testing.T,
	overwrite bool,
) (*ankitts.Application, ankitts.Plan) {
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
	app, err := ankitts.New(
		&batchAnki{notes: notes},
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
	plan, err := app.Prepare(ankitts.GenerationRequest{
		Notes:            notes,
		SourceField:      "Front",
		DestinationField: "Audio",
		Service:          "Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, plan
}
