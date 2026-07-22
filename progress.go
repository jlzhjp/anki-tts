package ankitts

import (
	"time"

	"jlzhjp.dev/anki-tts/pipeline"
)

// ProgressKind identifies an application operation lifecycle event.
type ProgressKind = pipeline.EventKind

const (
	ProgressStarted   = pipeline.Started
	ProgressRetrying  = pipeline.Retrying
	ProgressCompleted = pipeline.Completed
	ProgressFailed    = pipeline.Failed
)

// Operation describes the concrete action currently performed for a note.
type Operation string

const (
	OperationSynthesize Operation = "generate voice"
	OperationTransform  Operation = "process audio"
	OperationStoreMedia Operation = "store media"
	OperationUpdateNote Operation = "update note"
)

// ProgressEvent is an immutable worker-to-observer status update.
type ProgressEvent struct {
	Kind        ProgressKind
	Index       int
	NoteID      int64
	Stage       string
	Operation   Operation
	Attempt     int
	MaxAttempts int
	RetryAt     time.Time
	Err         error
}

// ProgressReporter receives pipeline events. Implementations must be safe for
// concurrent calls from stage workers.
type ProgressReporter interface {
	Report(ProgressEvent)
}

// ProgressReporterFunc adapts a function to a ProgressReporter.
type ProgressReporterFunc func(ProgressEvent)

func (f ProgressReporterFunc) Report(event ProgressEvent) { f(event) }
