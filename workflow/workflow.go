// Package workflow implements the application use cases shared by the TUI.
package workflow

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/textutil"
	"jlzhjp.dev/anki-tts/tts"
)

const maxFinalAudioSize = 32 << 20 // 32 MiB

// AnkiClient contains the Anki operations used by the workflow.
type AnkiClient interface {
	ListDecks(context.Context) ([]string, error)
	ListNoteTemplates(context.Context) ([]string, error)
	ListTemplateFields(context.Context, string) ([]string, error)
	ListNotes(context.Context, string) ([]anki.Note, error)
	StoreMediaFile(context.Context, string, []byte) (string, error)
	UpdateNote(context.Context, anki.NoteUpdate) error
}

// DestinationMode determines how generated audio is written to a note field.
type DestinationMode uint8

const (
	ReplaceDestination DestinationMode = iota
	AppendDestination
)

// GenerateRequest describes one generate-and-save operation.
type GenerateRequest struct {
	Note             anki.Note
	SourceField      string
	DestinationField string
	DestinationMode  DestinationMode
	Service          tts.NamedService
}

// GenerateResult describes a successfully stored voice.
type GenerateResult struct {
	Filename string
	Cost     *float64
	CostErr  error
}

// Service coordinates browsing Anki and generating audio for notes.
type Service struct {
	anki        AnkiClient
	services    *tts.Container
	transformer tts.Transformer
}

// New creates a workflow service.
func New(client AnkiClient, services *tts.Container, transformer tts.Transformer) *Service {
	if services == nil {
		services = tts.NewContainer()
	}
	return &Service{anki: client, services: services, transformer: transformer}
}

func (s *Service) ListDecks(ctx context.Context) ([]string, error) {
	return s.anki.ListDecks(ctx)
}

func (s *Service) ListNoteTemplates(ctx context.Context) ([]string, error) {
	return s.anki.ListNoteTemplates(ctx)
}

func (s *Service) ListTemplateFields(ctx context.Context, template string) ([]string, error) {
	return s.anki.ListTemplateFields(ctx, template)
}

func (s *Service) ListNotes(ctx context.Context, deck string) ([]anki.Note, error) {
	return s.anki.ListNotes(ctx, deck)
}

// Services returns the configured TTS services in display order.
func (s *Service) Services() []tts.NamedService {
	return s.services.Services()
}

// TransformsAudio reports whether generated voices pass through a transformer.
func (s *Service) TransformsAudio() bool {
	return s.transformer != nil
}

// Generate creates audio, stores it in Anki, and updates the destination field.
func (s *Service) Generate(ctx context.Context, request GenerateRequest) (GenerateResult, error) {
	if request.Service.Service == nil {
		return GenerateResult{}, errors.New("TTS service is not configured")
	}
	text, err := textutil.FromHTML(request.Note.Fields[request.SourceField].Value)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("prepare source field: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return GenerateResult{}, errors.New("source field contains no speakable text")
	}

	voice, err := request.Service.Service.Generate(ctx, tts.Input{Text: text})
	if err != nil {
		return GenerateResult{}, err
	}
	if voice == nil {
		return GenerateResult{}, errors.New("TTS service returned no voice")
	}

	finalVoice := voice
	if s.transformer != nil {
		finalVoice, err = s.transformer.Transform(ctx, voice)
		if err != nil {
			return GenerateResult{}, err
		}
		if finalVoice == nil {
			return GenerateResult{}, errors.New("audio pipeline returned no voice")
		}
	}
	defer finalVoice.Close()

	format := safeFormat(finalVoice.Format())
	if format == "" {
		return GenerateResult{}, fmt.Errorf("audio pipeline returned invalid format %q", finalVoice.Format())
	}
	data, err := io.ReadAll(io.LimitReader(finalVoice, maxFinalAudioSize+1))
	if err != nil {
		return GenerateResult{}, fmt.Errorf("read final audio: %w", err)
	}
	if len(data) == 0 {
		return GenerateResult{}, errors.New("audio pipeline returned empty data")
	}
	if len(data) > maxFinalAudioSize {
		return GenerateResult{}, fmt.Errorf("final audio exceeds %d bytes", maxFinalAudioSize)
	}

	var cost *float64
	costValue, costErr := finalVoice.LoadCost(ctx)
	if costErr == nil {
		cost = &costValue
	}
	hash := sha256.Sum256(data)
	filename := fmt.Sprintf("anki-tts-%d-%x.%s", request.Note.ID, hash[:6], format)
	storedFilename, err := s.anki.StoreMediaFile(ctx, filename, data)
	if err != nil {
		return GenerateResult{}, err
	}

	tag := "[sound:" + storedFilename + "]"
	value := tag
	if request.DestinationMode == AppendDestination && request.Note.Fields[request.DestinationField].Value != "" {
		value = request.Note.Fields[request.DestinationField].Value + "<br>" + tag
	}
	err = s.anki.UpdateNote(ctx, anki.NoteUpdate{
		ID:     request.Note.ID,
		Fields: map[string]string{request.DestinationField: value},
	})
	if err != nil {
		return GenerateResult{}, fmt.Errorf("media %q was stored but the note update failed: %w", storedFilename, err)
	}
	return GenerateResult{Filename: storedFilename, Cost: cost, CostErr: costErr}, nil
}

func safeFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return ""
	}
	for _, r := range format {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ""
		}
	}
	return format
}
