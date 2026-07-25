package pipeline

import (
	"context"
	"errors"
	"testing"
)

func TestFromSliceStopsAfterCancellation(t *testing.T) {
	values := make([]int, 100_000)
	started := make(chan struct{})
	release := make(chan struct{})
	stream, err := MapConcurrent(
		FromSlice(values),
		"work",
		1,
		func(ctx context.Context, value int) (int, error) {
			close(started)
			<-release
			return value, ctx.Err()
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct {
		results []Result[int]
		err     error
	}, 1)
	go func() {
		results, err := Collect(ctx, stream, nil)
		done <- struct {
			results []Result[int]
			err     error
		}{results: results, err: err}
	}()

	<-started
	cancel()
	close(release)
	outcome := <-done
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("error=%v", outcome.err)
	}
	if len(outcome.results) > 2 {
		t.Fatalf("processed %d results after cancellation", len(outcome.results))
	}
}
