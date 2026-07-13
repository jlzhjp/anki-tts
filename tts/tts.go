// Package tts defines provider-neutral text-to-speech interfaces.
package tts

import "context"

// ServiceFactory creates a text-to-speech service from provider configuration.
// The configuration is expected to be a TOML table unmarshaled into a map.
type ServiceFactory interface {
	Create(config map[string]any) (Service, error)
}

// Service generates speech from text.
type Service interface {
	Generate(ctx context.Context, input Input) (Voice, error)
}

// Input describes text to synthesize.
type Input struct {
	Text string
}

// Voice contains generated audio and provider response metadata.
type Voice struct {
	Data         []byte
	MediaType    string
	Format       string
	GenerationID string
}
