package ankitts

import (
	"context"
	"time"

	"jlzhjp.dev/ankitts/pipeline"
)

// ProgressKind identifies the meaning of an application progress event.
type ProgressKind uint8

const (
	// ProgressStarted indicates that a pipeline operation has begun.
	ProgressStarted ProgressKind = iota
	// ProgressUpdated indicates that an operation description changed.
	ProgressUpdated
	// ProgressRetrying indicates that a failed operation will be attempted again.
	ProgressRetrying
	// ProgressCompleted indicates that a pipeline operation succeeded.
	ProgressCompleted
	// ProgressFailed indicates that a pipeline operation exhausted its attempts.
	ProgressFailed
	// ProgressItemCompleted indicates that all work for an item succeeded.
	ProgressItemCompleted
)

// ProgressEvent is an immutable worker-to-observer status update.
type ProgressEvent struct {
	RetryAt     time.Time
	Err         error
	Stage       string
	Description string
	Index       int
	NoteID      int64
	Attempt     int
	MaxAttempts int
	Kind        ProgressKind
}

// ProgressReporter receives pipeline events. Implementations must be safe for
// concurrent calls and return promptly so they do not block stage workers.
type ProgressReporter interface {
	Report(*ProgressEvent)
}

// ProgressReporterFunc adapts a function to a ProgressReporter.
type ProgressReporterFunc func(*ProgressEvent)

// Report calls f with event.
func (f ProgressReporterFunc) Report(event *ProgressEvent) { f(event) }

type progressContextKey struct{}

type progressScope struct {
	reporter ProgressReporter
	event    ProgressEvent
}

func withProgress(ctx context.Context, reporter ProgressReporter, event *ProgressEvent) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, progressContextKey{}, progressScope{
		reporter: reporter,
		event:    *event,
	})
}

// ReportProgress publishes a component-owned description when ctx belongs to
// an observed application execution. It is otherwise a no-op.
func ReportProgress(ctx context.Context, description string) {
	if description == "" {
		return
	}
	reportProgress(ctx, ProgressUpdated, description)
}

func reportProgress(ctx context.Context, kind ProgressKind, description string) {
	scope, ok := ctx.Value(progressContextKey{}).(progressScope)
	if !ok || scope.reporter == nil {
		return
	}
	event := scope.event
	event.Kind = kind
	event.Description = description
	scope.reporter.Report(&event)
}

func progressKind(kind pipeline.EventKind) ProgressKind {
	switch kind {
	case pipeline.Started:
		return ProgressStarted
	case pipeline.Retrying:
		return ProgressRetrying
	case pipeline.Completed:
		return ProgressCompleted
	case pipeline.Failed:
		return ProgressFailed
	default:
		panic("unknown pipeline progress event kind")
	}
}
