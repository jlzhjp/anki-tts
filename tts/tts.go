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

// Service generates speech from text. Implementations must support concurrent
// Generate calls from the workflow synthesis stage.
type Service interface {
	Generate(ctx context.Context, input Input) (Voice, error)
}

// Transformer applies a provider-neutral transformation to generated audio.
// Implementations must support concurrent Transform calls. Transform consumes
// ownership of the input Voice: on failure it closes the input; on success the
// returned Voice owns the input and closes it when appropriate.
type Transformer interface {
	Transform(ctx context.Context, voice Voice) (Voice, error)
}

// Input describes text to synthesize.
type Input struct {
	Text string
}

// Voice is a generated audio stream with provider metadata. Callers must close
// it. LoadCost may perform network I/O and remains valid after Close.
type Voice interface {
	io.ReadCloser
	Format() string
	MediaType() string
	LoadCost(ctx context.Context) (float64, error)
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
