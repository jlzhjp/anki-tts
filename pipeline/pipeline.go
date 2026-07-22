package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// EventKind identifies an operation lifecycle event.
type EventKind uint8

const (
	Started EventKind = iota
	Retrying
	Completed
	Failed
)

// Event describes one operation performed for a pipeline item.
type Event struct {
	Kind        EventKind
	Index       int
	Stage       string
	Operation   string
	Attempt     int
	MaxAttempts int
	RetryAt     time.Time
	Err         error
}

// Observer receives concurrent pipeline events.
type Observer interface {
	Report(Event)
}

// ObserverFunc adapts a function to an Observer.
type ObserverFunc func(Event)

func (f ObserverFunc) Report(event Event) { f(event) }

// Result is the terminal state of one input item.
type Result[T any] struct {
	Index int
	Value T
	Stage string
	Err   error
}

type entry[T any] struct {
	index   int
	value   T
	stage   string
	failure error
}

type execution struct {
	ctx      context.Context
	observer Observer
}

// Stream is a lazy, typed pipeline execution plan.
type Stream[T any] struct {
	start func(*execution) <-chan entry[T]
}

// FromSlice creates a lazy source from a snapshot of values.
func FromSlice[T any](values []T) Stream[T] {
	values = append([]T(nil), values...)
	return Stream[T]{start: func(*execution) <-chan entry[T] {
		output := make(chan entry[T])
		go func() {
			defer close(output)
			for index, value := range values {
				output <- entry[T]{index: index, value: value}
			}
		}()
		return output
	}}
}

// Transform maps one pipeline value to another.
type Transform[I, O any] func(context.Context, I) (O, error)

// MapConcurrent appends a bounded, named transform to a stream.
func MapConcurrent[I, O any](input Stream[I], stage string, concurrency int, transform Transform[I, O]) (Stream[O], error) {
	if input.start == nil {
		return Stream[O]{}, errors.New("pipeline stream is not initialized")
	}
	if stage == "" {
		return Stream[O]{}, errors.New("pipeline stage name is required")
	}
	if concurrency <= 0 {
		return Stream[O]{}, fmt.Errorf("pipeline stage %q concurrency must be positive", stage)
	}
	if transform == nil {
		return Stream[O]{}, fmt.Errorf("pipeline stage %q has no transform", stage)
	}

	return Stream[O]{start: func(run *execution) <-chan entry[O] {
		stageInput := input.start(run)
		output := make(chan entry[O])
		var workers sync.WaitGroup
		workers.Add(concurrency)
		for range concurrency {
			go func() {
				defer workers.Done()
				for current := range stageInput {
					next := entry[O]{index: current.index, stage: current.stage, failure: current.failure}
					if current.failure == nil {
						if err := run.ctx.Err(); err != nil {
							next.stage, next.failure = stage, err
						} else {
							ctx := context.WithValue(run.ctx, scopeContextKey{}, operationScope{
								index: current.index, stage: stage, observer: run.observer,
							})
							value, err := transform(ctx, current.value)
							if err != nil {
								next.stage, next.failure = stage, err
							} else {
								next.value = value
							}
						}
					}
					output <- next
				}
			}()
		}
		go func() {
			workers.Wait()
			close(output)
		}()
		return output
	}}, nil
}

// Collect executes a stream and returns input-ordered item outcomes.
func Collect[T any](ctx context.Context, input Stream[T], observer Observer) ([]Result[T], error) {
	if input.start == nil {
		return nil, errors.New("pipeline stream is not initialized")
	}
	output := input.start(&execution{ctx: ctx, observer: observer})
	resultsByIndex := make(map[int]Result[T])
	maximumIndex := -1
	for current := range output {
		resultsByIndex[current.index] = Result[T]{
			Index: current.index, Value: current.value,
			Stage: current.stage, Err: current.failure,
		}
		if current.index > maximumIndex {
			maximumIndex = current.index
		}
	}
	results := make([]Result[T], maximumIndex+1)
	for index, result := range resultsByIndex {
		results[index] = result
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}

type scopeContextKey struct{}

type operationScope struct {
	index    int
	stage    string
	observer Observer
}

func report(ctx context.Context, config RetryConfig, event Event) {
	scope, ok := ctx.Value(scopeContextKey{}).(operationScope)
	if !ok || scope.observer == nil {
		return
	}
	event.Index = scope.index
	event.Stage = scope.stage
	event.MaxAttempts = config.MaxAttempts
	scope.observer.Report(event)
}

// Retry wraps a transform with a validated, named retry policy.
func Retry[I, O any](config RetryConfig, operation string, transform Transform[I, O]) (Transform[I, O], error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("retry operation %q: %w", operation, err)
	}
	if operation == "" {
		return nil, errors.New("retry operation name is required")
	}
	if transform == nil {
		return nil, fmt.Errorf("retry operation %q has no transform", operation)
	}
	return func(ctx context.Context, input I) (O, error) {
		report(ctx, config, Event{Kind: Started, Operation: operation, Attempt: 1})
		var output O
		attempts, err := retry(ctx, config, func(attempt int, retryAt time.Time, err error) {
			report(ctx, config, Event{Kind: Retrying, Operation: operation, Attempt: attempt, RetryAt: retryAt, Err: err})
		}, func() error {
			value, err := transform(ctx, input)
			if err == nil {
				output = value
			}
			return err
		})
		kind := Completed
		if err != nil {
			kind = Failed
		}
		if attempts == 0 {
			attempts = 1
		}
		report(ctx, config, Event{Kind: kind, Operation: operation, Attempt: attempts, Err: err})
		return output, err
	}, nil
}
