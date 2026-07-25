// Package interactive implements the guided terminal generation workflow.
package interactive

import "jlzhjp.dev/ankitts"

// Application contains only the capabilities used by the interactive workflow.
type Application interface {
	noteSource
	generationApplication
	ServiceNames() []string
}

// Options configures stages that can be supplied without prompting.
type Options struct {
	FromField string
	ToField   string
	Service   string
	Query     ankitts.NoteQuery
	Yes       bool
}
