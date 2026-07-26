package pipeline

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// StageConfig controls one bounded pipeline stage.
type StageConfig struct {
	Concurrency int
	Retry       RetryConfig
}

// Config maps component names directly to their execution policy.
type Config map[string]StageConfig

// DefaultStageConfig returns the default retry policy with the given concurrency.
func DefaultStageConfig(concurrency int) StageConfig {
	return StageConfig{
		Concurrency: concurrency,
		Retry: RetryConfig{
			MaxAttempts:    DefaultMaxAttempts,
			InitialBackoff: DefaultInitialBackoff,
			MaxBackoff:     DefaultMaxBackoff,
		},
	}
}

// Validate checks every configured stage policy.
func (c Config) Validate() error {
	for _, name := range slices.Sorted(maps.Keys(c)) {
		if name == "" {
			return errors.New("pipeline stage name is required")
		}
		config := c[name]
		if err := config.Validate(); err != nil {
			return fmt.Errorf("stage %q: %w", name, err)
		}
	}
	return nil
}

// Validate checks a stage policy.
func (c StageConfig) Validate() error {
	if c.Concurrency <= 0 {
		return errors.New("concurrency must be positive")
	}
	return c.Retry.Validate()
}
