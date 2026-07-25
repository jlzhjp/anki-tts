package pipeline

import (
	"context"
	"time"
)

// EventKind identifies an operation lifecycle event.
type EventKind uint8

const (
	// Started indicates that an operation attempt has begun.
	Started EventKind = iota
	// Retrying indicates that a failed operation will be attempted again.
	Retrying
	// Completed indicates that an operation succeeded.
	Completed
	// Failed indicates that an operation exhausted its attempts.
	Failed
)

// Event describes one operation performed for a pipeline item.
type Event struct {
	Kind      EventKind
	Index     int
	Stage     string
	Operation string
	// Attempt identifies the attempt being reported. For Retrying, it is the
	// failed attempt; the next attempt is Attempt + 1.
	Attempt     int
	MaxAttempts int
	RetryAt     time.Time
	Err         error
}

// Observer receives concurrent pipeline events. Implementations must be safe
// for concurrent use and return promptly so they do not block stage workers.
type Observer interface {
	Report(Event)
}

// ObserverFunc adapts a function to an Observer.
type ObserverFunc func(Event)

// Report calls f with event.
func (f ObserverFunc) Report(event Event) { f(event) }

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
