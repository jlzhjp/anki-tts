package workflow

import (
	"context"
	"errors"
	"testing"

	"jlzhjp.dev/anki-tts/anki"
)

func TestMapConcurrentDiscardsValueWhenCancellationWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	input := make(chan pipelineItem[int], 1)
	output := make(chan pipelineItem[string])
	job := preparedJob{index: 0, note: anki.Note{ID: 42}}
	input <- pipelineItem[int]{job: job, value: 1}
	close(input)
	discarded := 0

	done := make(chan error, 1)
	go func() {
		done <- mapConcurrent(ctx, 1, StageSynthesis, input, output,
			func(context.Context, preparedJob, int) (string, error) {
				cancel()
				return "owned resource", nil
			},
			func(string) { discarded++ })
	}()
	item := <-output
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if discarded != 1 {
		t.Fatalf("discard calls=%d, want 1", discarded)
	}
	if item.failure == nil || !errors.Is(item.failure.Err, context.Canceled) {
		t.Fatalf("failure=%+v", item.failure)
	}
}

func TestMapConcurrentForwardsTerminalFailureWithoutTransforming(t *testing.T) {
	input := make(chan pipelineItem[int], 1)
	output := make(chan pipelineItem[string])
	job := preparedJob{index: 0, note: anki.Note{ID: 42}}
	failure := failedResult(job, StageSynthesis, errors.New("failed"))
	input <- pipelineItem[int]{job: job, failure: &failure}
	close(input)
	transformed := false

	done := make(chan error, 1)
	go func() {
		done <- mapConcurrent(context.Background(), 2, StageAudio, input, output,
			func(context.Context, preparedJob, int) (string, error) {
				transformed = true
				return "", nil
			}, nil)
	}()
	item := <-output
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if transformed || item.failure != &failure {
		t.Fatalf("transformed=%v failure=%p want=%p", transformed, item.failure, &failure)
	}
}
