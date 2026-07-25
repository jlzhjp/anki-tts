package main

import (
	"fmt"
	"strings"

	"jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/cmd/anki-tts/step"
)

type navigation uint8

const (
	navigateNext navigation = iota
	navigateBack
)

type interactiveWorkflow struct {
	client   step.Client
	app      application
	options  runOptions
	services []string
	state    workflowState
	screens  workflowScreens
}

type workflowState struct {
	note             anki.Note
	sourceField      string
	destinationField string
	service          string
}

type workflowScreens struct {
	note        *step.NoteScreen
	source      *step.SourceFieldScreen
	destination *step.DestinationFieldScreen
	overwrite   *step.DestinationOverwriteScreen
	service     *step.TTSServiceScreen
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

func (w *interactiveWorkflow) display() step.Display {
	return step.Display{Context: w.state.contextLine()}
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
