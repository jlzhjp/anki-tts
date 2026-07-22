package pipeline

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testItem struct {
	id     int
	stages []string
}

func TestMapConcurrentChangesTypesAndPreservesInputOrder(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	generated, err := MapConcurrent(FromSlice([]int{0, 1, 2, 3, 4, 5}), "openrouter", 3,
		func(_ context.Context, id int) (testItem, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
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
		return string(rune('a' + item.id)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(context.Background(), stored, nil)
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
	var calls atomic.Int32
	stream, err := MapConcurrent(FromSlice([]int(nil)), "empty", 16,
		func(_ context.Context, value int) (int, error) {
			calls.Add(1)
			return value, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(context.Background(), stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 || calls.Load() != 0 {
		t.Fatalf("results=%v calls=%d", results, calls.Load())
	}
}

func TestFailureBypassesRemainingMaps(t *testing.T) {
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
		return string(rune('a' + item.id)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(context.Background(), second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[1].Stage != "first" || results[1].Err == nil || results[1].Value != "" || secondCalls.Load() != 2 {
		t.Fatalf("results=%+v second calls=%d", results, secondCalls.Load())
	}
}

func TestRetryCombinatorsRetryOnlyTheirOwnOperation(t *testing.T) {
	policy := retryConfig(3)
	storeCalls, updateCalls := 0, 0
	store, err := Retry(policy, "store", func(_ context.Context, _ int) (string, error) {
		storeCalls++
		return "file.mp3", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	update, err := Retry(policy, "update", func(_ context.Context, stored string) (string, error) {
		updateCalls++
		if updateCalls < 3 {
			return "", errors.New("temporary")
		}
		return stored, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	persist := func(ctx context.Context, value int) (string, error) {
		stored, err := store(ctx, value)
		if err != nil {
			return "", err
		}
		return update(ctx, stored)
	}
	stream, err := MapConcurrent(FromSlice([]int{1}), "anki", 1, persist)
	if err != nil {
		t.Fatal(err)
	}
	var eventsMu sync.Mutex
	var events []Event
	_, err = Collect(context.Background(), stream, ObserverFunc(func(event Event) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, event)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if storeCalls != 1 || updateCalls != 3 {
		t.Fatalf("store calls=%d update calls=%d", storeCalls, updateCalls)
	}
	var updateKinds []EventKind
	for _, event := range events {
		if event.Operation == "update" {
			updateKinds = append(updateKinds, event.Kind)
			if event.Stage != "anki" || event.Index != 0 {
				t.Fatalf("scoped event=%+v", event)
			}
			if event.Kind == Completed && event.Attempt != 3 {
				t.Fatalf("completed event=%+v", event)
			}
		}
	}
	if want := []EventKind{Started, Retrying, Retrying, Completed}; !reflect.DeepEqual(updateKinds, want) {
		t.Fatalf("update events=%v want=%v", updateKinds, want)
	}
}

func TestCancellationReturnsEveryOutcomeAndClosesChannels(t *testing.T) {
	blocking, err := MapConcurrent(FromSlice([]int{0, 1, 2}), "blocking", 2,
		func(ctx context.Context, id int) (int, error) {
			<-ctx.Done()
			return id, ctx.Err()
		})
	if err != nil {
		t.Fatal(err)
	}
	var laterCalls atomic.Int32
	later, err := MapConcurrent(blocking, "later", 2, func(_ context.Context, id int) (int, error) {
		laterCalls.Add(1)
		return id, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := Collect(ctx, later, nil)
	if !errors.Is(err, context.Canceled) || len(results) != 3 {
		t.Fatalf("results=%d error=%v", len(results), err)
	}
	for _, result := range results {
		if !errors.Is(result.Err, context.Canceled) || result.Stage != "blocking" {
			t.Fatalf("result=%+v", result)
		}
	}
	if laterCalls.Load() != 0 {
		t.Fatalf("later calls=%d", laterCalls.Load())
	}
}

func TestCancellationWaitsForInFlightTransforms(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
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
	<-started
	cancel()
	outcome := <-done
	if !errors.Is(outcome.err, context.Canceled) || len(outcome.results) != 3 {
		t.Fatalf("results=%d error=%v", len(outcome.results), outcome.err)
	}
	for _, result := range outcome.results {
		if result.Stage != "work" || !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("result=%+v", result)
		}
	}
}

func TestCancellationStopsRetryBackoffAndReportsFailure(t *testing.T) {
	policy := RetryConfig{MaxAttempts: 3, InitialBackoff: time.Hour, MaxBackoff: time.Hour}
	started := make(chan struct{})
	retrying, err := Retry(policy, "operation", func(_ context.Context, value int) (int, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		return value, errors.New("temporary")
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := MapConcurrent(FromSlice([]int{1}), "retry", 1, retrying)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var eventsMu sync.Mutex
	var events []Event
	done := make(chan error, 1)
	go func() {
		_, err := Collect(ctx, stream, ObserverFunc(func(event Event) {
			eventsMu.Lock()
			defer eventsMu.Unlock()
			events = append(events, event)
		}))
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt retry backoff")
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	if last := events[len(events)-1]; last.Kind != Failed || !errors.Is(last.Err, context.Canceled) || last.Attempt != 1 {
		t.Fatalf("last event=%+v", last)
	}
}

func TestCompositionValidationIsLazy(t *testing.T) {
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
	if _, err := Retry(RetryConfig{}, "operation", identity); err == nil {
		t.Fatal("expected invalid retry policy error")
	}
	if calls.Load() != 0 {
		t.Fatal("validation failure started the pipeline")
	}
}

func TestRepeatedStageLabelsAreAllowed(t *testing.T) {
	first, err := MapConcurrent(FromSlice([]testItem{{}}), "anki", 1, appendStage("store"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := MapConcurrent(first, "anki", 1, appendStage("update"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := Collect(context.Background(), second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"store", "update"}; !reflect.DeepEqual(results[0].Value.stages, want) {
		t.Fatalf("stages=%v want=%v", results[0].Value.stages, want)
	}
}

func appendStage(name string) Transform[testItem, testItem] {
	return func(_ context.Context, item testItem) (testItem, error) {
		item.stages = append(item.stages, name)
		return item, nil
	}
}

func retryConfig(attempts int) RetryConfig {
	return RetryConfig{
		MaxAttempts:    attempts,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	}
}
