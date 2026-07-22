package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/workflow"
)

func TestBatchConfirmationChoosesScreenFromTerminalHeight(t *testing.T) {
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
			model := newBatchModel(plannedNotes(test.count, false), false, make(chan bool, 1), make(chan tea.Msg, 1))
			updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: test.height})
			view := updated.(batchModel).View()
			if view.AltScreen != test.want {
				t.Fatalf("alternate screen = %v, want %v", view.AltScreen, test.want)
			}
			resized, _ := updated.(batchModel).Update(tea.WindowSizeMsg{Width: 100, Height: 100})
			if resized.(batchModel).View().AltScreen != test.want {
				t.Fatal("confirmation screen mode changed after resize")
			}
		})
	}
}

func TestBatchConfirmationExitsAlternateScreenBeforeStarting(t *testing.T) {
	decision := make(chan bool, 1)
	model := newBatchModel(plannedNotes(20, true), false, decision, make(chan tea.Msg, 1))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 10})
	model = updated.(batchModel)
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	model = updated.(batchModel)
	if model.phase != batchConfirmOverwrite || !model.View().AltScreen {
		t.Fatalf("first confirmation = phase %v alt %v", model.phase, model.View().AltScreen)
	}
	updated, cmd := model.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	model = updated.(batchModel)
	if cmd == nil || model.View().AltScreen {
		t.Fatalf("running transition alt = %v, cmd = %v", model.View().AltScreen, cmd)
	}
	updated, _ = model.Update(cmd())
	_ = updated.(batchModel)
	select {
	case accepted := <-decision:
		if !accepted {
			t.Fatal("confirmation was rejected")
		}
	default:
		t.Fatal("pipeline was not released")
	}
}

func TestBatchProgressShowsRetryAndSummary(t *testing.T) {
	model := newBatchModel(plannedNotes(1, false), true, nil, make(chan tea.Msg, 1))
	updated, _ := model.Update(workflow.ProgressEvent{
		Kind: workflow.ProgressRetrying, Index: 0, NoteID: 1, Step: workflow.StepSynthesize,
		Attempt: 2, MaxAttempts: 3, RetryAt: time.Now().Add(time.Second), Err: errors.New("rate limited"),
	})
	model = updated.(batchModel)
	for _, want := range []string{"note 1", "generate voice", "retry 2/3", "rate limited"} {
		if !strings.Contains(model.View().Content, want) {
			t.Fatalf("progress missing %q: %s", want, model.View().Content)
		}
	}
	updated, _ = model.Update(batchFinishedMsg{result: workflow.BatchResult{Items: []workflow.ItemResult{{NoteID: 1}}}})
	if got := updated.(batchModel).View().Content; !strings.Contains(got, "1 succeeded, 0 failed") {
		t.Fatalf("summary = %s", got)
	}
}

func plannedNotes(count int, overwrite bool) []workflow.PlannedNote {
	notes := make([]workflow.PlannedNote, count)
	for index := range notes {
		notes[index] = workflow.PlannedNote{Index: index, Note: anki.Note{ID: int64(index + 1), ModelName: "Basic"}, SourceText: "hello", WillOverwrite: overwrite}
	}
	return notes
}
