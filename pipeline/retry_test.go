package pipeline

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestRetryRetriesOnlyWrappedOperation(t *testing.T) {
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
	_, err = Collect(t.Context(), stream, ObserverFunc(func(event Event) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		if event.Operation == "update" {
			events = append(events, event)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if storeCalls != 1 || updateCalls != 3 {
		t.Fatalf("store calls=%d update calls=%d", storeCalls, updateCalls)
	}
	kinds := make([]EventKind, 0, len(events))
	attempts := make([]int, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
		attempts = append(attempts, event.Attempt)
		if event.Stage != "anki" || event.Index != 0 {
			t.Fatalf("scoped event=%+v", event)
		}
	}
	if want := []EventKind{Started, Retrying, Started, Retrying, Started, Completed}; !slices.Equal(kinds, want) {
		t.Fatalf("event kinds=%v want=%v", kinds, want)
	}
	if want := []int{1, 1, 2, 2, 3, 3}; !slices.Equal(attempts, want) {
		t.Fatalf("attempts=%v want=%v", attempts, want)
	}
}

func TestRetryCancellationDuringBackoffReportsLastAttempt(t *testing.T) {
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
	ctx, cancel := context.WithCancel(t.Context())
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
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	if len(events) != 3 {
		t.Fatalf("events=%+v", events)
	}
	if want := []EventKind{Started, Retrying, Failed}; !slices.Equal(
		[]EventKind{events[0].Kind, events[1].Kind, events[2].Kind},
		want,
	) {
		t.Fatalf("events=%+v", events)
	}
	for _, event := range events {
		if event.Attempt != 1 {
			t.Fatalf("event=%+v", event)
		}
	}
}

func TestRetryCanceledBeforeFirstAttemptReportsNothing(t *testing.T) {
	var calls int
	transform, err := Retry(retryConfig(3), "operation", func(_ context.Context, value int) (int, error) {
		calls++
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	ctx, cancel := context.WithCancel(t.Context())
	ctx = context.WithValue(ctx, scopeContextKey{}, operationScope{
		observer: ObserverFunc(func(event Event) { events = append(events, event) }),
	})
	cancel()
	if _, err := transform(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if calls != 0 || len(events) != 0 {
		t.Fatalf("calls=%d events=%+v", calls, events)
	}
}

func TestRetryValidationIsLazy(t *testing.T) {
	identity := func(_ context.Context, value int) (int, error) { return value, nil }
	if _, err := Retry(RetryConfig{}, "operation", identity); err == nil {
		t.Fatal("expected invalid retry policy error")
	}
}
