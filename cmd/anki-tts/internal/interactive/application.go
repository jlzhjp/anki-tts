package interactive

import "jlzhjp.dev/anki-tts"

// Application contains only the capabilities used by the interactive workflow.
type Application interface {
	noteSource
	generationApplication
	ServiceNames() []string
}

// Options configures stages that can be supplied without prompting.
type Options struct {
	Query     ankitts.NoteQuery
	FromField string
	ToField   string
	Service   string
	Yes       bool
}
