package interactive

import (
	"fmt"
	"strings"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/anki"
)

type navigation uint8

const (
	navigateNext navigation = iota
	navigateBack
)

type interactiveWorkflow struct {
	screens  workflowScreens
	client   client
	app      Application
	services []string
	state    workflowState
	options  Options
}

type workflowState struct {
	sourceField      string
	destinationField string
	service          string
	note             anki.Note
}

type workflowScreens struct {
	note        *noteScreen
	source      *sourceFieldScreen
	destination *destinationFieldScreen
	overwrite   *destinationOverwriteScreen
	service     *ttsServiceScreen
}

func (w *interactiveWorkflow) setNote(note anki.Note) {
	if note.ID == w.state.note.ID {
		return
	}
	w.state.note = note
	w.state.sourceField = ""
	w.state.destinationField = ""
	w.state.service = ""
	w.clearAfterNote()
}

func (w *interactiveWorkflow) setSourceField(field string) {
	if field == w.state.sourceField {
		return
	}
	w.state.sourceField = field
	w.state.destinationField = ""
	w.state.service = ""
	w.screens.destination = nil
	w.screens.overwrite = nil
	w.screens.service = nil
}

func (w *interactiveWorkflow) setDestinationField(field string) {
	if field == w.state.destinationField {
		return
	}
	w.state.destinationField = field
	w.state.service = ""
	w.screens.overwrite = nil
	w.screens.service = nil
}

func (w *interactiveWorkflow) setService(service string) {
	w.state.service = service
}

func (w *interactiveWorkflow) clearAfterNote() {
	w.screens.source = nil
	w.screens.destination = nil
	w.screens.overwrite = nil
	w.screens.service = nil
}

func (w *interactiveWorkflow) resetAfterGeneration() {
	w.state.note = anki.Note{}
	w.state.sourceField = ""
	w.state.destinationField = ""
	w.state.service = ""
	w.clearAfterNote()
}

func (w *interactiveWorkflow) display() display {
	return display{Context: w.state.contextLine()}
}

func (s workflowState) contextLine() string {
	parts := make([]string, 0, 4)
	if s.note.ID != 0 {
		parts = append(parts, fmt.Sprintf("Note: %d", s.note.ID))
	}
	if s.sourceField != "" {
		parts = append(parts, "Source: "+s.sourceField)
	}
	if s.destinationField != "" {
		parts = append(parts, "Destination: "+s.destinationField)
	}
	if s.service != "" {
		parts = append(parts, "Service: "+s.service)
	}
	return strings.Join(parts, " · ")
}

func saveStatus(result ankitts.GenerateResult, destination string) string {
	var costStatus string
	if result.Cost != nil {
		costStatus = fmt.Sprintf("Cost: $%.6f · ", *result.Cost)
	} else if result.CostErr != nil {
		costStatus = fmt.Sprintf("Cost unavailable: %v · ", result.CostErr)
	}
	return costStatus + fmt.Sprintf("Saved %s to %s", result.Filename, destination)
}
