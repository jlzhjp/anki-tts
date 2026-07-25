package batch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"jlzhjp.dev/ankitts"
)

func TestBatchConfirmationChoosesAlternateScreenFromHeight(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			screen := &confirmationScreen{
				noteIDs: noteIDs(test.count),
				height:  24,
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
	t.Parallel()
	screen := &generationScreen{
		notes:    plannedNotes(1, false),
		progress: make(map[int]noteProgress),
	}
	updated, _ := screen.Update(ankitts.ProgressEvent{
		Kind:        ankitts.ProgressRetrying,
		Index:       0,
		NoteID:      1,
		Description: "Generating speech with test-model",
		Attempt:     1,
		MaxAttempts: 3,
		RetryAt:     time.Now().Add(time.Second),
		Err:         errors.New("rate limited"),
	})
	var ok bool
	screen, ok = updated.(*generationScreen)
	if !ok {
		t.Fatalf("updated model=%T", updated)
	}
	for _, want := range []string{
		"note 1",
		"Generating speech with test-model",
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

	updated, cmd := screen.Update(finishedMsg{
		result: ankitts.BatchResult{
			Items: []ankitts.ItemResult{{NoteID: 1}},
		},
	})
	screen, ok = updated.(*generationScreen)
	if !ok {
		t.Fatalf("updated model=%T", updated)
	}
	if cmd == nil {
		t.Fatal("finished batch did not complete its workflow step")
	}
	if got := screen.View().Content; !strings.Contains(
		got,
		"1 succeeded, 0 failed",
	) {
		t.Fatalf("summary=%s", got)
	}
	if _, ok := cmd().(completedMsg); !ok {
		t.Fatalf("completion message=%T", cmd())
	}
}

func TestBatchConfirmationAcceptsAndRejects(t *testing.T) {
	t.Parallel()
	screen := &confirmationScreen{}
	_, accept := screen.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if msg, ok := accept().(completedMsg); !ok || msg.Value != true {
		t.Fatalf("accept message=%+v", msg)
	}
	_, reject := screen.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if msg, ok := reject().(completedMsg); !ok || msg.Value != false {
		t.Fatalf("reject message=%+v", msg)
	}
}

func TestBatchStepFunctionsConstructTypedScreens(t *testing.T) {
	t.Parallel()
	confirmationClient := &fakeClient{value: true}
	accepted, confirmation, err := confirm(
		t.Context(),
		confirmationClient,
		[]int64{1},
		false,
		nil,
		display{},
	)
	if err != nil || !accepted || confirmation == nil {
		t.Fatalf(
			"accepted=%v screen=%T error=%v",
			accepted,
			confirmation,
			err,
		)
	}

	generationClient := &fakeClient{value: outcome{}}
	_, err = generate(
		t.Context(),
		generationClient,
		&fakeBatchExecution{},
		ankitts.Plan{},
		display{CancelIsError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !generationClient.displays[0].CancelIsError {
		t.Fatal("generation display policy was not preserved")
	}
}

func TestBatchGenerationExecutesWithProgressReporter(t *testing.T) {
	t.Parallel()
	app := &fakeBatchExecution{
		result: ankitts.BatchResult{
			Items: []ankitts.ItemResult{{NoteID: 1}},
		},
	}
	screen := &generationScreen{
		ctx:      t.Context(),
		app:      app,
		events:   make(chan ankitts.ProgressEvent, 1),
		progress: make(map[int]noteProgress),
	}

	finished, ok := screen.execute()().(finishedMsg)
	if !ok {
		t.Fatal("execute did not return finishedMsg")
	}

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
	err    error
	result ankitts.BatchResult
}

func (a *fakeBatchExecution) Execute(
	_ context.Context,
	_ ankitts.Plan,
	options ankitts.ExecuteOptions,
) (ankitts.BatchResult, error) {
	if options.Progress != nil {
		options.Progress.Report(&ankitts.ProgressEvent{
			Kind: ankitts.ProgressStarted,
		})
	}
	return a.result, a.err
}

func plannedNotes(count int, overwrite bool) []ankitts.PlannedNote {
	notes := make([]ankitts.PlannedNote, count)
	for index := range notes {
		notes[index] = ankitts.PlannedNote{
			NoteID:        int64(index + 1),
			WillOverwrite: overwrite,
		}
	}
	return notes
}

func noteIDs(count int) []int64 {
	ids := make([]int64, count)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	return ids
}
