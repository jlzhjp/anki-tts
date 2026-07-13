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
	"net/url"
	"os"
	"strings"
	"time"

	"jlzhjp.dev/anki-tts/tts"
)

const (
	defaultEndpoint           = "https://openrouter.ai/api/v1/audio/speech"
	defaultGenerationEndpoint = "https://openrouter.ai/api/v1/generation"
	defaultVoice              = "alloy"
	defaultFormat             = "mp3"
	maxAudioSize              = 32 << 20 // 32 MiB
	maxErrorBodySize          = 64 << 10 // 64 KiB
	apiKeyEnvironment         = "OPENROUTER_API_KEY"
)

// HTTPClient is implemented by *http.Client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Factory creates OpenRouter text-to-speech services.
type Factory struct {
	endpoint           string
	generationEndpoint string
	httpClient         HTTPClient
}

// Option configures a Factory.
type Option func(*Factory)

// WithEndpoint overrides the OpenRouter text-to-speech endpoint.
func WithEndpoint(endpoint string) Option {
	return func(factory *Factory) {
		factory.endpoint = strings.TrimSpace(endpoint)
	}
}

// WithGenerationEndpoint overrides the generation metadata endpoint.
func WithGenerationEndpoint(endpoint string) Option {
	return func(factory *Factory) {
		factory.generationEndpoint = strings.TrimSpace(endpoint)
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
		endpoint:           defaultEndpoint,
		generationEndpoint: defaultGenerationEndpoint,
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
	if strings.TrimSpace(f.generationEndpoint) == "" {
		return nil, errors.New("create OpenRouter TTS service: generation endpoint is required")
	}

	return &service{
		endpoint:           f.endpoint,
		generationEndpoint: f.generationEndpoint,
		apiKey:             apiKey,
		model:              model,
		voice:              voice,
		format:             format,
		httpClient:         f.httpClient,
	}, nil
}

type service struct {
	endpoint           string
	generationEndpoint string
	apiKey             string
	model              string
	voice              string
	format             string
	httpClient         HTTPClient
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
		stream:       &limitedAudioStream{body: resp.Body, remaining: maxAudioSize},
		mediaType:    mediaType,
		format:       s.format,
		generationID: resp.Header.Get("X-Generation-Id"),
		service:      s,
	}, nil
}

type voiceResult struct {
	stream       io.ReadCloser
	mediaType    string
	format       string
	generationID string
	service      *service
}

func (v *voiceResult) Read(p []byte) (int, error) { return v.stream.Read(p) }
func (v *voiceResult) Close() error               { return v.stream.Close() }
func (v *voiceResult) Format() string             { return v.format }
func (v *voiceResult) MediaType() string          { return v.mediaType }

func (v *voiceResult) LoadCost(ctx context.Context) (float64, error) {
	if v.generationID == "" {
		return 0, errors.New("lookup OpenRouter generation cost: response did not include X-Generation-Id")
	}
	return v.service.lookupCost(ctx, v.generationID)
}

func (s *service) lookupCost(ctx context.Context, generationID string) (float64, error) {
	endpoint, err := url.Parse(s.generationEndpoint)
	if err != nil {
		return 0, fmt.Errorf("lookup OpenRouter generation cost: parse endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("id", generationID)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("lookup OpenRouter generation cost: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("lookup OpenRouter generation cost: send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, openRouterAPIError("lookup OpenRouter generation cost", resp, s.apiKey)
	}
	var result struct {
		Data struct {
			TotalCost *float64 `json:"total_cost"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBodySize)).Decode(&result); err != nil {
		return 0, fmt.Errorf("lookup OpenRouter generation cost: decode response: %w", err)
	}
	if result.Data.TotalCost == nil {
		return 0, errors.New("lookup OpenRouter generation cost: response does not contain total_cost")
	}
	return *result.Data.TotalCost, nil
}

type limitedAudioStream struct {
	body      io.ReadCloser
	remaining int64
}

func (s *limitedAudioStream) Read(p []byte) (int, error) {
	buffer := p
	if int64(len(buffer)) > s.remaining+1 {
		buffer = buffer[:s.remaining+1]
	}
	n, err := s.body.Read(buffer)
	if int64(n) > s.remaining {
		return 0, fmt.Errorf("generate OpenRouter speech: response exceeds %d bytes", maxAudioSize)
	}
	s.remaining -= int64(n)
	return n, err
}

func (s *limitedAudioStream) Close() error {
	return s.body.Close()
}

func openRouterError(resp *http.Response, apiKey string) error {
	return openRouterAPIError("generate OpenRouter speech", resp, apiKey)
}

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
