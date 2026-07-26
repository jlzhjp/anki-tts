package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectRestoresOrderAfterConcurrentTypeChangingMaps(t *testing.T) {
	t.Parallel()
	var active atomic.Int32
	var maximum atomic.Int32
	generated, err := MapConcurrent(FromSlice([]int{0, 1, 2, 3, 4, 5}), "openrouter", 3,
		func(_ context.Context, id int) (testItem, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			if id%2 == 0 {
				time.Sleep(time.Millisecond)
			}
			return testItem{id: id, stages: []string{"openrouter"}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := MapConcurrent(generated, "ffmpeg", 2, appendStage("ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := MapConcurrent(processed, "anki", 1, func(_ context.Context, item testItem) (string, error) {
		return fmt.Sprintf("%c", 'a'+item.id), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(t.Context(), stored, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range results {
		if result.Index != index || result.Value != string(rune('a'+index)) || result.Err != nil {
			t.Fatalf("result %d = %+v", index, result)
		}
	}
	if maximum.Load() < 2 || maximum.Load() > 3 {
		t.Fatalf("maximum concurrency = %d, want 2..3", maximum.Load())
	}
}

func TestMapConcurrentClosesEmptyWorkerPool(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	stream, err := MapConcurrent(FromSlice([]int(nil)), "empty", 16,
		func(_ context.Context, value int) (int, error) {
			calls.Add(1)
			return value, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(t.Context(), stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 || calls.Load() != 0 {
		t.Fatalf("results=%v calls=%d", results, calls.Load())
	}
}

func TestMapConcurrentBypassesRemainingStagesAfterFailure(t *testing.T) {
	t.Parallel()
	first, err := MapConcurrent(FromSlice([]int{0, 1, 2}), "first", 2,
		func(_ context.Context, id int) (testItem, error) {
			if id == 1 {
				return testItem{}, errors.New("failed")
			}
			return testItem{id: id}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	var secondCalls atomic.Int32
	second, err := MapConcurrent(first, "second", 2, func(_ context.Context, item testItem) (string, error) {
		secondCalls.Add(1)
		return fmt.Sprintf("%c", 'a'+item.id), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(t.Context(), second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[1].Stage != "first" || results[1].Err == nil || results[1].Value != "" || secondCalls.Load() != 2 {
		t.Fatalf("results=%+v second calls=%d", results, secondCalls.Load())
	}
}

func TestMapConcurrentWaitsForInFlightTransforms(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 2)
	stream, err := MapConcurrent(FromSlice([]int{0, 1, 2}), "work", 2,
		func(ctx context.Context, value int) (int, error) {
			started <- struct{}{}
			<-ctx.Done()
			return value, ctx.Err()
		})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := Collect(ctx, stream, nil)
		done <- err
	}()
	<-started
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestMapConcurrentValidationDoesNotStartUpstream(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	stream, err := MapConcurrent(FromSlice([]int{1}), "configured", 1,
		func(_ context.Context, value int) (int, error) {
			calls.Add(1)
			return value, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("map ran before collection")
	}
	identity := func(_ context.Context, value int) (int, error) { return value, nil }
	if _, err := MapConcurrent(stream, "invalid", 0, identity); err == nil {
		t.Fatal("expected invalid concurrency error")
	}
	if calls.Load() != 0 {
		t.Fatal("validation failure started the pipeline")
	}
}

func TestMapConcurrentAllowsRepeatedStageLabels(t *testing.T) {
	t.Parallel()
	first, err := MapConcurrent(FromSlice([]testItem{{}}), "anki", 1, appendStage("store"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := MapConcurrent(first, "anki", 1, appendStage("update"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(t.Context(), second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"store", "update"}; !slices.Equal(results[0].Value.stages, want) {
		t.Fatalf("stages=%v want=%v", results[0].Value.stages, want)
	}
}
