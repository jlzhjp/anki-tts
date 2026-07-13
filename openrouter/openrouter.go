// Package openrouter implements text-to-speech using OpenRouter.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"jlzhjp.dev/anki-tts/tts"
)

const (
	defaultEndpoint   = "https://openrouter.ai/api/v1/audio/speech"
	defaultVoice      = "alloy"
	defaultFormat     = "mp3"
	maxAudioSize      = 32 << 20 // 32 MiB
	maxErrorBodySize  = 64 << 10 // 64 KiB
	apiKeyEnvironment = "OPENROUTER_API_KEY"
)

// HTTPClient is implemented by *http.Client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Factory creates OpenRouter text-to-speech services.
type Factory struct {
	endpoint   string
	httpClient HTTPClient
}

// Option configures a Factory.
type Option func(*Factory)

// WithEndpoint overrides the OpenRouter text-to-speech endpoint.
func WithEndpoint(endpoint string) Option {
	return func(factory *Factory) {
		factory.endpoint = strings.TrimSpace(endpoint)
	}
}

// WithHTTPClient overrides the client used for OpenRouter requests.
func WithHTTPClient(client HTTPClient) Option {
	return func(factory *Factory) {
		if client != nil {
			factory.httpClient = client
		}
	}
}

// NewFactory creates an OpenRouter service factory.
func NewFactory(options ...Option) *Factory {
	factory := &Factory{
		endpoint: defaultEndpoint,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}
	for _, option := range options {
		if option != nil {
			option(factory)
		}
	}
	return factory
}

// Create validates config and creates an OpenRouter text-to-speech service.
// Supported keys are model, api_key, voice, and response_format.
func (f *Factory) Create(config map[string]any) (tts.Service, error) {
	model, err := requiredString(config, "model")
	if err != nil {
		return nil, fmt.Errorf("create OpenRouter TTS service: %w", err)
	}

	apiKey, err := optionalString(config, "api_key", "")
	if err != nil {
		return nil, fmt.Errorf("create OpenRouter TTS service: %w", err)
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(apiKeyEnvironment))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("create OpenRouter TTS service: api_key is required (or set %s)", apiKeyEnvironment)
	}

	voice, err := optionalString(config, "voice", defaultVoice)
	if err != nil {
		return nil, fmt.Errorf("create OpenRouter TTS service: %w", err)
	}
	format, err := optionalString(config, "response_format", defaultFormat)
	if err != nil {
		return nil, fmt.Errorf("create OpenRouter TTS service: %w", err)
	}
	if format != "mp3" && format != "pcm" {
		return nil, fmt.Errorf("create OpenRouter TTS service: response_format must be mp3 or pcm, got %q", format)
	}
	if strings.TrimSpace(f.endpoint) == "" {
		return nil, errors.New("create OpenRouter TTS service: endpoint is required")
	}

	return &service{
		endpoint:   f.endpoint,
		apiKey:     apiKey,
		model:      model,
		voice:      voice,
		format:     format,
		httpClient: f.httpClient,
	}, nil
}

type service struct {
	endpoint   string
	apiKey     string
	model      string
	voice      string
	format     string
	httpClient HTTPClient
}

type speechRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
}

// Generate synthesizes input text through OpenRouter.
func (s *service) Generate(ctx context.Context, input tts.Input) (tts.Voice, error) {
	if strings.TrimSpace(input.Text) == "" {
		return tts.Voice{}, errors.New("generate OpenRouter speech: input text is required")
	}

	body, err := json.Marshal(speechRequest{
		Model:          s.model,
		Input:          input.Text,
		Voice:          s.voice,
		ResponseFormat: s.format,
	})
	if err != nil {
		return tts.Voice{}, fmt.Errorf("generate OpenRouter speech: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return tts.Voice{}, fmt.Errorf("generate OpenRouter speech: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return tts.Voice{}, fmt.Errorf("generate OpenRouter speech: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return tts.Voice{}, openRouterError(resp, s.apiKey)
	}

	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioSize+1))
	if err != nil {
		return tts.Voice{}, fmt.Errorf("generate OpenRouter speech: read response: %w", err)
	}
	if len(audio) > maxAudioSize {
		return tts.Voice{}, fmt.Errorf("generate OpenRouter speech: response exceeds %d bytes", maxAudioSize)
	}

	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = mediaTypeForFormat(s.format)
	}
	return tts.Voice{
		Data:         audio,
		MediaType:    mediaType,
		Format:       s.format,
		GenerationID: resp.Header.Get("X-Generation-Id"),
	}, nil
}

func openRouterError(resp *http.Response, apiKey string) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	if readErr == nil {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Error.Message) != "" {
			message := strings.ReplaceAll(envelope.Error.Message, apiKey, "[REDACTED]")
			return fmt.Errorf("generate OpenRouter speech: HTTP %s: %s", resp.Status, message)
		}
	}
	return fmt.Errorf("generate OpenRouter speech: HTTP %s", resp.Status)
}

func requiredString(config map[string]any, key string) (string, error) {
	value, ok := config[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return text, nil
}

func optionalString(config map[string]any, key, fallback string) (string, error) {
	value, ok := config[key]
	if !ok {
		return fallback, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback, nil
	}
	return text, nil
}

func mediaTypeForFormat(format string) string {
	if format == "pcm" {
		return "audio/pcm"
	}
	return "audio/mpeg"
}

var (
	_ tts.ServiceFactory = (*Factory)(nil)
	_ tts.Service        = (*service)(nil)
)
