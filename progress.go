package ankitts

import (
	"context"
	"time"

	"jlzhjp.dev/anki-tts/pipeline"
)

// ProgressKind identifies the meaning of an application progress event.
type ProgressKind uint8

const (
	ProgressStarted ProgressKind = iota
	ProgressUpdated
	ProgressRetrying
	ProgressCompleted
	ProgressFailed
	ProgressItemCompleted
)

// ProgressEvent is an immutable worker-to-observer status update.
type ProgressEvent struct {
	Kind        ProgressKind
	Index       int
	NoteID      int64
	Stage       string
	Description string
	// Attempt identifies the attempt being reported. For ProgressRetrying, it
	// is the failed attempt; the next attempt is Attempt + 1.
	Attempt     int
	MaxAttempts int
	RetryAt     time.Time
	Err         error
}

// ProgressReporter receives pipeline events. Implementations must be safe for
// concurrent calls and return promptly so they do not block stage workers.
type ProgressReporter interface {
	Report(ProgressEvent)
}

// ProgressReporterFunc adapts a function to a ProgressReporter.
type ProgressReporterFunc func(ProgressEvent)

func (f ProgressReporterFunc) Report(event ProgressEvent) { f(event) }

type progressContextKey struct{}

type progressScope struct {
	reporter ProgressReporter
	event    ProgressEvent
}

func withProgress(ctx context.Context, reporter ProgressReporter, event ProgressEvent) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, progressContextKey{}, progressScope{
		reporter: reporter,
		event:    event,
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
	scope.reporter.Report(event)
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
