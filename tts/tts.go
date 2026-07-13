// Package tts defines provider-neutral text-to-speech interfaces.
package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// ServiceFactory creates a text-to-speech service from provider configuration.
// The configuration is expected to be a TOML table unmarshaled into a map.
type ServiceFactory interface {
	Create(config map[string]any) (Service, error)
}

// Service generates speech from text.
type Service interface {
	Generate(ctx context.Context, input Input) (Voice, error)
}

// Transformer applies a provider-neutral transformation to generated audio.
// On success, the returned stream owns and eventually closes the input stream.
type Transformer interface {
	Transform(ctx context.Context, audio AudioStream) (AudioStream, error)
}

// Input describes text to synthesize.
type Input struct {
	Text string
}

// AudioStream contains audio data and its media description. The consumer owns
// Data and must close it.
type AudioStream struct {
	Data      io.ReadCloser
	MediaType string
	Format    string
}

// Voice contains generated audio and provider response metadata.
type Voice struct {
	Audio        AudioStream
	GenerationID string
}

// NamedService associates a display name with a Service.
type NamedService struct {
	Name    string
	Service Service
}

// Container stores the text-to-speech services available to the application.
type Container struct {
	mu       sync.RWMutex
	services map[string]Service
}

// NewContainer creates an empty service container.
func NewContainer() *Container {
	return &Container{services: make(map[string]Service)}
}

// Add registers a service under name.
func (c *Container) Add(name string, service Service) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("TTS service name is required")
	}
	if service == nil {
		return fmt.Errorf("TTS service %q is nil", name)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.services[name]; exists {
		return fmt.Errorf("TTS service %q is already registered", name)
	}
	c.services[name] = service
	return nil
}

// Services returns registered services sorted by name.
func (c *Container) Services() []NamedService {
	c.mu.RLock()
	defer c.mu.RUnlock()

	services := make([]NamedService, 0, len(c.services))
	for name, service := range c.services {
		services = append(services, NamedService{Name: name, Service: service})
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	return services
}
