// Package openrouter implements text-to-speech using OpenRouter.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	if strings.TrimSpace(f.modelsEndpoint) == "" {
		return nil, errors.New("create OpenRouter TTS service: models endpoint is required")
	}

	return &service{
		endpoint:       f.endpoint,
		modelsEndpoint: f.modelsEndpoint,
		apiKey:         apiKey,
		model:          model,
		voice:          voice,
		format:         format,
		httpClient:     f.httpClient,
	}, nil
}

type service struct {
	endpoint       string
	modelsEndpoint string
	apiKey         string
	model          string
	voice          string
	format         string
	httpClient     HTTPClient
	pricingMu      sync.Mutex
	pricePerChar   *float64
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
		stream:         &limitedAudioStream{body: resp.Body, remaining: maxAudioSize},
		mediaType:      mediaType,
		format:         s.format,
		characterCount: utf8.RuneCountInString(input.Text),
		service:        s,
	}, nil
}

type voiceResult struct {
	stream         io.ReadCloser
	mediaType      string
	format         string
	characterCount int
	service        *service
}

func (v *voiceResult) Read(p []byte) (int, error) { return v.stream.Read(p) }
func (v *voiceResult) Close() error               { return v.stream.Close() }
func (v *voiceResult) Format() string             { return v.format }
func (v *voiceResult) MediaType() string          { return v.mediaType }

func (v *voiceResult) LoadCost(ctx context.Context) (float64, error) {
	price, err := v.service.loadPricePerCharacter(ctx)
	if err != nil {
		return 0, err
	}
	return float64(v.characterCount) * price, nil
}

func (s *service) loadPricePerCharacter(ctx context.Context) (float64, error) {
	s.pricingMu.Lock()
	defer s.pricingMu.Unlock()
	if s.pricePerChar != nil {
		return *s.pricePerChar, nil
	}

	endpoint, err := url.Parse(s.modelsEndpoint)
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: parse endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("output_modalities", "speech")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, openRouterAPIError("load OpenRouter TTS pricing", resp, s.apiKey)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsResponseSize+1))
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: read response: %w", err)
	}
	if len(body) > maxModelsResponseSize {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: response exceeds %d bytes", maxModelsResponseSize)
	}
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt string `json:"prompt"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: decode response: %w", err)
	}
	for _, model := range result.Data {
		if model.ID != s.model {
			continue
		}
		price, err := strconv.ParseFloat(model.Pricing.Prompt, 64)
		if err != nil || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return 0, fmt.Errorf("load OpenRouter TTS pricing: model %q has invalid per-character price %q", s.model, model.Pricing.Prompt)
		}
		s.pricePerChar = &price
		return price, nil
	}
	return 0, fmt.Errorf("load OpenRouter TTS pricing: model %q was not found in speech models", s.model)
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
