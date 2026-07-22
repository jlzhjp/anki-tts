package workflow

import "time"

// ProgressKind identifies a pipeline lifecycle event.
type ProgressKind uint8

const (
	ProgressStarted ProgressKind = iota
	ProgressRetrying
	ProgressCompleted
	ProgressFailed
)

// Step describes the concrete operation currently performed for a note.
type Step string

const (
	StepSynthesize Step = "generate voice"
	StepTransform  Step = "process audio"
	StepStoreMedia Step = "store media"
	StepUpdateNote Step = "update note"
)

// ProgressEvent is an immutable worker-to-observer status update.
type ProgressEvent struct {
	Kind        ProgressKind
	Index       int
	NoteID      int64
	Stage       Stage
	Step        Step
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
