package step

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

func TestBatchConfirmationChoosesAlternateScreenFromHeight(t *testing.T) {
	for _, test := range []struct {
		name   string
		count  int
		height int
		want   bool
	}{
		{name: "fits inline", count: 2, height: 20},
		{name: "needs alternate screen", count: 20, height: 10, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			screen := &BatchConfirmationScreen{
				notes:  plannedNotes(test.count, false),
				width:  100,
				height: 24,
			}
			screen.SetSize(100, test.height)
			if screen.View().AltScreen != test.want {
				t.Fatalf(
					"alternate screen=%v, want %v",
					screen.View().AltScreen,
					test.want,
				)
			}
			screen.SetSize(100, 100)
			if screen.View().AltScreen != test.want {
				t.Fatal("screen mode changed after its initial size")
			}
		})
	}
}

func TestBatchGenerationShowsRetryAndSummary(t *testing.T) {
	screen := &BatchGenerationScreen{
		notes:    plannedNotes(1, false),
		progress: make(map[int]noteProgress),
	}
	updated, _ := screen.Update(ankitts.ProgressEvent{
		Kind:        ankitts.ProgressRetrying,
		Index:       0,
		NoteID:      1,
		Operation:   ankitts.OperationSynthesize,
		Attempt:     2,
		MaxAttempts: 3,
		RetryAt:     time.Now().Add(time.Second),
		Err:         errors.New("rate limited"),
	})
	screen = updated.(*BatchGenerationScreen)
	for _, want := range []string{
		"note 1",
		"generate voice",
		"retry 2/3",
		"rate limited",
	} {
		if !strings.Contains(screen.View().Content, want) {
			t.Fatalf(
				"progress missing %q: %s",
				want,
				screen.View().Content,
			)
		}
	}

	updated, cmd := screen.Update(batchFinishedMsg{
		result: ankitts.BatchResult{
			Items: []ankitts.ItemResult{{NoteID: 1}},
		},
	})
	screen = updated.(*BatchGenerationScreen)
	if cmd == nil {
		t.Fatal("finished batch did not complete its workflow step")
	}
	if got := screen.View().Content; !strings.Contains(
		got,
		"1 succeeded, 0 failed",
	) {
		t.Fatalf("summary=%s", got)
	}
	if _, ok := cmd().(CompletedMsg); !ok {
		t.Fatalf("completion message=%T", cmd())
	}
}

func TestBatchConfirmationAcceptsAndRejects(t *testing.T) {
	screen := &BatchConfirmationScreen{}
	_, accept := screen.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if msg := accept(); msg.(CompletedMsg).Value != true {
		t.Fatalf("accept message=%+v", msg)
	}
	_, reject := screen.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if msg := reject(); msg.(CompletedMsg).Value != false {
		t.Fatalf("reject message=%+v", msg)
	}
}

func TestBatchStepFunctionsConstructTypedScreens(t *testing.T) {
	confirmationClient := &fakeClient{value: true}
	accepted, confirmation, err := ConfirmBatch(
		context.Background(),
		confirmationClient,
		plannedNotes(1, false),
		false,
		nil,
		Display{},
	)
	if err != nil || !accepted || confirmation == nil {
		t.Fatalf(
			"accepted=%v screen=%T error=%v",
			accepted,
			confirmation,
			err,
		)
	}

	generationClient := &fakeClient{value: BatchOutcome{}}
	_, err = GenerateBatch(
		context.Background(),
		generationClient,
		&fakeBatchExecution{},
		ankitts.Plan{},
		Display{CancelIsError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !generationClient.displays[0].CancelIsError {
		t.Fatal("generation display policy was not preserved")
	}
}

func TestBatchGenerationExecutesWithProgressReporter(t *testing.T) {
	app := &fakeBatchExecution{
		result: ankitts.BatchResult{
			Items: []ankitts.ItemResult{{NoteID: 1}},
		},
	}
	screen := &BatchGenerationScreen{
		ctx:      context.Background(),
		app:      app,
		events:   make(chan ankitts.ProgressEvent, 1),
		progress: make(map[int]noteProgress),
	}

	finished := screen.execute()().(batchFinishedMsg)

	if finished.err != nil || len(finished.result.Items) != 1 {
		t.Fatalf("finished=%+v", finished)
	}
	select {
	case event := <-screen.events:
		if event.Kind != ankitts.ProgressStarted {
			t.Fatalf("event=%+v", event)
		}
	default:
		t.Fatal("execution did not report progress")
	}
}

type fakeBatchExecution struct {
	result ankitts.BatchResult
	err    error
}

func (a *fakeBatchExecution) Execute(
	_ context.Context,
	_ ankitts.Plan,
	options ankitts.ExecuteOptions,
) (ankitts.BatchResult, error) {
	if options.Progress != nil {
		options.Progress.Report(ankitts.ProgressEvent{
			Kind: ankitts.ProgressStarted,
		})
	}
	return a.result, a.err
}

func plannedNotes(count int, overwrite bool) []ankitts.PlannedNote {
	notes := make([]ankitts.PlannedNote, count)
	for index := range notes {
		notes[index] = ankitts.PlannedNote{
			Index: index,
			Note: anki.Note{
				ID:        int64(index + 1),
				ModelName: "Basic",
			},
			SourceText:    "hello",
			WillOverwrite: overwrite,
		}
	}
	return notes
}
