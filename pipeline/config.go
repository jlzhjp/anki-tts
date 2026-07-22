// Package pipeline executes dynamically assembled, named processing stages.
package pipeline

import (
	"errors"
	"fmt"
	"time"
)

const (
	DefaultMaxAttempts    = 3
	DefaultInitialBackoff = 500 * time.Millisecond
	DefaultMaxBackoff     = 5 * time.Second
)

// RetryConfig controls context-aware exponential retry backoff.
type RetryConfig struct {
	MaxAttempts    int           `toml:"max_attempts"`
	InitialBackoff time.Duration `toml:"initial_backoff"`
	MaxBackoff     time.Duration `toml:"max_backoff"`
}

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
	for name, config := range c {
		if name == "" {
			return errors.New("pipeline stage name is required")
		}
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

// Validate checks a retry policy.
func (c RetryConfig) Validate() error {
	if c.MaxAttempts < 1 {
		return errors.New("max attempts must be at least one")
	}
	if c.InitialBackoff <= 0 {
		return errors.New("initial backoff must be positive")
	}
	if c.MaxBackoff < c.InitialBackoff {
		return errors.New("max backoff must not be less than initial backoff")
	}
	return nil
}
