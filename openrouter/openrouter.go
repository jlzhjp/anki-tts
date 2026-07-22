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

	"jlzhjp.dev/anki-tts/internal/streamutil"
	"jlzhjp.dev/anki-tts/tts"
)

const (
	defaultEndpoint       = "https://openrouter.ai/api/v1/audio/speech"
	defaultModelsEndpoint = "https://openrouter.ai/api/v1/models"
	defaultVoice          = "alloy"
	defaultFormat         = "mp3"
	maxAudioSize          = 32 << 20 // 32 MiB
	maxErrorBodySize      = 64 << 10 // 64 KiB
	maxModelsResponseSize = 4 << 20  // 4 MiB
	apiKeyEnvironment     = "OPENROUTER_API_KEY"
)

// HTTPClient is implemented by *http.Client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Factory creates OpenRouter text-to-speech services.
type Factory struct {
	endpoint       string
	modelsEndpoint string
	httpClient     HTTPClient
}

// Config describes an OpenRouter text-to-speech service.
type Config struct {
	Model          string `toml:"model"`
	APIKey         string `toml:"api_key"`
	Voice          string `toml:"voice"`
	ResponseFormat string `toml:"response_format"`
}

// Option configures a Factory.
type Option func(*Factory)

// WithEndpoint overrides the OpenRouter text-to-speech endpoint.
func WithEndpoint(endpoint string) Option {
	return func(factory *Factory) {
		factory.endpoint = strings.TrimSpace(endpoint)
	}
}

// WithModelsEndpoint overrides the model metadata endpoint.
func WithModelsEndpoint(endpoint string) Option {
	return func(factory *Factory) {
		factory.modelsEndpoint = strings.TrimSpace(endpoint)
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
		endpoint:       defaultEndpoint,
		modelsEndpoint: defaultModelsEndpoint,
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
func (f *Factory) Create(config Config) (tts.Service, error) {
	model := strings.TrimSpace(config.Model)
	if model == "" {
		return nil, errors.New("create OpenRouter TTS service: model is required")
	}

	// Load OpenRouter API Key
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(apiKeyEnvironment))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("create OpenRouter TTS service: api_key is required (or set %s)", apiKeyEnvironment)
	}

	// Load voice
	voice := strings.TrimSpace(config.Voice)
	if voice == "" {
		voice = defaultVoice
	}

	// Load format
	format := strings.TrimSpace(config.ResponseFormat)
	if format == "" {
		format = defaultFormat
	}
	if format != "mp3" && format != "pcm" {
		return nil, fmt.Errorf("create OpenRouter TTS service: response_format must be mp3 or pcm, got %q", format)
	}
	if strings.TrimSpace(f.endpoint) == "" {
		return nil, errors.New("create OpenRouter TTS service: endpoint is required")
	}
	if strings.TrimSpace(f.modelsEndpoint) == "" {
		return nil, errors.New("create OpenRouter TTS service: models endpoint is required")
	}

	return &service{
		endpoint:       f.endpoint,
		apiKey:         apiKey,
		model:          model,
		voice:          voice,
		format:         format,
		httpClient:     f.httpClient,
		costCalculator: newOpenRouterCostCalculator(f.modelsEndpoint, apiKey, f.httpClient),
	}, nil
}

type service struct {
	endpoint       string
	apiKey         string
	model          string
	voice          string
	format         string
	httpClient     HTTPClient
	costCalculator costCalculator
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
		return nil, errors.New("generate OpenRouter speech: input text is required")
	}

	body, err := json.Marshal(speechRequest{
		Model:          s.model,
		Input:          input.Text,
		Voice:          s.voice,
		ResponseFormat: s.format,
	})
	if err != nil {
		return nil, fmt.Errorf("generate OpenRouter speech: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("generate OpenRouter speech: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("generate OpenRouter speech: send request: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		return nil, openRouterError(resp, s.apiKey)
	}
	if resp.ContentLength > maxAudioSize {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("generate OpenRouter speech: response exceeds %d bytes", maxAudioSize)
	}

	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = mediaTypeForFormat(s.format)
	}
	return &voiceResult{
		body:           resp.Body,
		stream:         streamutil.NewLimitedReader(resp.Body, maxAudioSize),
		mediaType:      mediaType,
		format:         s.format,
		sentence:       input.Text,
		model:          s.model,
		costCalculator: s.costCalculator,
	}, nil
}

type voiceResult struct {
	body           io.Closer
	stream         *streamutil.LimitedReader
	mediaType      string
	format         string
	sentence       string
	model          string
	costCalculator costCalculator
}

func (v *voiceResult) Read(p []byte) (int, error) {
	n, err := v.stream.Read(p)
	if errors.Is(err, streamutil.ErrLimitExceeded) {
		return 0, fmt.Errorf("generate OpenRouter speech: response exceeds %d bytes", v.stream.Limit())
	}
	return n, err
}

func (v *voiceResult) Close() error      { return v.body.Close() }
func (v *voiceResult) Format() string    { return v.format }
func (v *voiceResult) MediaType() string { return v.mediaType }
func (v *voiceResult) LoadCost(ctx context.Context) (float64, error) {
	return v.costCalculator.Calculate(ctx, v.sentence, v.model)
}

// openRouterError converts an unsuccessful speech response into a descriptive error.
func openRouterError(resp *http.Response, apiKey string) error {
	return openRouterAPIError("generate OpenRouter speech", resp, apiKey)
}

// openRouterAPIError reads a bounded OpenRouter error response, includes its
// message when available, and redacts the API key before returning it.
// For example: "generate OpenRouter speech: HTTP 401 Unauthorized: invalid API key".
func openRouterAPIError(operation string, resp *http.Response, apiKey string) error {
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
	if readErr == nil {
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Error.Message) != "" {
			message := strings.ReplaceAll(envelope.Error.Message, apiKey, "[REDACTED]")
			return fmt.Errorf("%s: HTTP %s: %s", operation, resp.Status, message)
		}
	}
	return fmt.Errorf("%s: HTTP %s", operation, resp.Status)
}

func mediaTypeForFormat(format string) string {
	if format == "pcm" {
		return "audio/pcm"
	}
	return "audio/mpeg"
}

var _ tts.Service = (*service)(nil)
