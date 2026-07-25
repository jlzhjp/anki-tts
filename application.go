// Package ankitts provides reusable Anki text-to-speech application logic.
package ankitts

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"

	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/internal/textutil"
	"jlzhjp.dev/ankitts/pipeline"
)

const persistenceStage = "anki"

// AnkiClient contains the Anki operations used by the application.
type AnkiClient interface {
	FindNoteIDs(context.Context, string) ([]int64, error)
	NotesInfo(context.Context, []int64) ([]anki.Note, error)
	StoreMediaFile(context.Context, string, []byte) (string, error)
	UpdateNote(context.Context, anki.NoteUpdate) error
}

// GenerationRequest describes a note stream that shares generation settings.
type GenerationRequest struct {
	Notes            iter.Seq[NoteResult]
	SourceField      string
	DestinationField string
	Service          string
}

// PlannedNote is safe presentation data produced before execution.
type PlannedNote struct {
	NoteID        int64
	WillOverwrite bool
}

// Plan is a validated, prepared generation batch.
type Plan struct {
	jobs        []preparedJob
	serviceName string
}

// Items returns presentation copies in deterministic input order.
func (p Plan) Items() []PlannedNote {
	items := make([]PlannedNote, len(p.jobs))
	for index, job := range p.jobs {
		items[index] = PlannedNote{
			NoteID: job.noteID, WillOverwrite: job.willOverwrite,
		}
	}
	return items
}

type preparedJob struct {
	index            int
	noteID           int64
	text             string
	destinationField string
	service          Service
	willOverwrite    bool
}

// Application coordinates Anki browsing and text-to-speech generation.
type Application struct {
	anki       AnkiClient
	services   *ServiceContainer
	processors []AudioProcessor
	config     pipeline.Config
}

// New constructs an application from named components and their pipeline policies.
func New(client AnkiClient, services *ServiceContainer, processors []AudioProcessor, config pipeline.Config) (*Application, error) {
	if client == nil {
		return nil, errors.New("anki client is required")
	}
	if services == nil {
		services = NewServiceContainer()
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("configure pipeline: %w", err)
	}
	required := append(services.Names(), persistenceStage)
	seenProcessors := make(map[string]struct{}, len(processors))
	for _, processor := range processors {
		if strings.TrimSpace(processor.Name) == "" {
			return nil, errors.New("audio processor name is required")
		}
		if processor.Transformer == nil {
			return nil, fmt.Errorf("audio processor %q is nil", processor.Name)
		}
		if _, exists := seenProcessors[processor.Name]; exists {
			return nil, fmt.Errorf("audio processor %q is registered more than once", processor.Name)
		}
		seenProcessors[processor.Name] = struct{}{}
		required = append(required, processor.Name)
	}
	for _, name := range required {
		if _, ok := config[name]; !ok {
			return nil, fmt.Errorf("pipeline stage %q has no configuration", name)
		}
	}
	configCopy := maps.Clone(config)
	return &Application{
		anki: client, services: services,
		processors: slices.Clone(processors), config: configCopy,
	}, nil
}

// ServiceNames returns configured TTS service names in display order.
func (a *Application) ServiceNames() []string { return a.services.Names() }

// HasAudioProcessors reports whether generated audio passes through processors.
func (a *Application) HasAudioProcessors() bool { return len(a.processors) > 0 }

// Prepare fully validates and compacts a request before generation or mutation.
func (a *Application) Prepare(request GenerationRequest) (Plan, error) {
	service, ok := a.services.get(request.Service)
	if !ok {
		return Plan{}, fmt.Errorf("TTS service %q is not configured", request.Service)
	}
	if _, ok := a.config[request.Service]; !ok {
		return Plan{}, fmt.Errorf("pipeline stage %q has no configuration", request.Service)
	}
	if strings.TrimSpace(request.SourceField) == "" {
		return Plan{}, errors.New("source field is required")
	}
	if strings.TrimSpace(request.DestinationField) == "" {
		return Plan{}, errors.New("destination field is required")
	}

	if request.Notes == nil {
		return Plan{}, errors.New("notes are required")
	}

	jobs := make([]preparedJob, 0)
	var invalid []error
	for result := range request.Notes {
		if result.Err != nil {
			return Plan{}, result.Err
		}
		note := result.Note
		source, ok := note.Fields[request.SourceField]
		if !ok {
			invalid = append(invalid, fmt.Errorf("note %d: missing source field %q", note.ID, request.SourceField))
			continue
		}
		destination, ok := note.Fields[request.DestinationField]
		if !ok {
			invalid = append(invalid, fmt.Errorf("note %d: missing destination field %q", note.ID, request.DestinationField))
			continue
		}
		text, err := textutil.FromHTML(source.Value)
		if err != nil {
			invalid = append(invalid, fmt.Errorf("note %d: prepare source field: %w", note.ID, err))
			continue
		}
		if strings.TrimSpace(text) == "" {
			invalid = append(invalid, fmt.Errorf("note %d: source field %q has no speakable text", note.ID, request.SourceField))
			continue
		}
		jobs = append(jobs, preparedJob{
			index: len(jobs), noteID: note.ID, text: text, destinationField: request.DestinationField,
			service: service, willOverwrite: strings.TrimSpace(destination.Value) != "",
		})
	}
	if len(invalid) > 0 {
		return Plan{}, fmt.Errorf("selected notes cannot be processed: %w", errors.Join(invalid...))
	}
	return Plan{jobs: jobs, serviceName: request.Service}, nil
}
