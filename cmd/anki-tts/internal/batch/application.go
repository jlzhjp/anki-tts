// Package batch implements terminal and plain batch generation workflows.
package batch

import (
	"context"
	"iter"

	"jlzhjp.dev/ankitts"
)

// Application contains only the capabilities used by batch execution.
type Application interface {
	Notes(context.Context, ankitts.NoteSelection, ankitts.NoteLoadOptions) iter.Seq[ankitts.NoteResult]
	Prepare(ankitts.GenerationRequest) (ankitts.Plan, error)
	Execute(context.Context, ankitts.Plan, ankitts.ExecuteOptions) (ankitts.BatchResult, error)
}

// Options configures non-interactive generation.
type Options struct {
	FromField string
	ToField   string
	Service   string
	Yes       bool
}
