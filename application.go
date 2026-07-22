// Package ankitts provides reusable Anki text-to-speech application logic.
package ankitts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/internal/textutil"
	"jlzhjp.dev/anki-tts/pipeline"
)

const persistenceStage = "anki"

// AnkiClient contains the Anki operations used by the application.
type AnkiClient interface {
	ListDecks(context.Context) ([]string, error)
	ListNoteTemplates(context.Context) ([]string, error)
	ListTemplateFields(context.Context, string) ([]string, error)
	ListNotes(context.Context, string) ([]anki.Note, error)
	StoreMediaFile(context.Context, string, []byte) (string, error)
	UpdateNote(context.Context, anki.NoteUpdate) error
}

// GenerationRequest describes notes that share generation settings.
type GenerationRequest struct {
	Notes            []anki.Note
	SourceField      string
	DestinationField string
	Service          string
}

// PlannedNote is safe presentation data produced before execution.
type PlannedNote struct {
	Index         int
	Note          anki.Note
	SourceText    string
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
			Index: job.index, Note: job.note, SourceText: job.text,
			WillOverwrite: job.willOverwrite,
		}
	}
	return items
}

type preparedJob struct {
	index            int
	note             anki.Note
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
		return nil, errors.New("Anki client is required")
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
	configCopy := make(pipeline.Config, len(config))
	for name, stage := range config {
		configCopy[name] = stage
	}
	return &Application{
		anki: client, services: services,
		processors: append([]AudioProcessor(nil), processors...), config: configCopy,
	}, nil
}

func (a *Application) ListDecks(ctx context.Context) ([]string, error) {
	return a.anki.ListDecks(ctx)
}

func (a *Application) ListNoteTemplates(ctx context.Context) ([]string, error) {
	return a.anki.ListNoteTemplates(ctx)
}

func (a *Application) ListTemplateFields(ctx context.Context, template string) ([]string, error) {
	return a.anki.ListTemplateFields(ctx, template)
}

// ServiceNames returns configured TTS service names in display order.
func (a *Application) ServiceNames() []string { return a.services.Names() }

// HasAudioProcessors reports whether generated audio passes through processors.
func (a *Application) HasAudioProcessors() bool { return len(a.processors) > 0 }

// Prepare validates a generation request and extracts source text before external work.
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

	jobs := make([]preparedJob, 0, len(request.Notes))
	var invalid []string
	for index, note := range request.Notes {
		source, ok := note.Fields[request.SourceField]
		if !ok {
			invalid = append(invalid, fmt.Sprintf("note %d: missing source field %q", note.ID, request.SourceField))
			continue
		}
		destination, ok := note.Fields[request.DestinationField]
		if !ok {
			invalid = append(invalid, fmt.Sprintf("note %d: missing destination field %q", note.ID, request.DestinationField))
			continue
		}
		text, err := textutil.FromHTML(source.Value)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("note %d: prepare source field: %v", note.ID, err))
			continue
		}
		if strings.TrimSpace(text) == "" {
			invalid = append(invalid, fmt.Sprintf("note %d: source field %q has no speakable text", note.ID, request.SourceField))
			continue
		}
		jobs = append(jobs, preparedJob{
			index: index, note: note, text: text, destinationField: request.DestinationField,
			service: service, willOverwrite: strings.TrimSpace(destination.Value) != "",
		})
	}
	if len(invalid) > 0 {
		return Plan{}, fmt.Errorf("selected notes cannot be processed:\n  %s", strings.Join(invalid, "\n  "))
	}
	return Plan{jobs: jobs, serviceName: request.Service}, nil
}
