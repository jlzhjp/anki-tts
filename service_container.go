package ankitts

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// ServiceContainer stores the text-to-speech services available to the application.
type ServiceContainer struct {
	services map[string]Service
	mu       sync.RWMutex
}

// NewServiceContainer creates an empty service container.
func NewServiceContainer() *ServiceContainer {
	return &ServiceContainer{services: make(map[string]Service)}
}

// Add registers a service under name.
func (c *ServiceContainer) Add(name string, service Service) error {
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

// Names returns registered service names in sorted order.
func (c *ServiceContainer) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return slices.Sorted(maps.Keys(c.services))
}

func (c *ServiceContainer) get(name string) (Service, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	service, ok := c.services[name]
	return service, ok
}
