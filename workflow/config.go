package workflow

import (
	"errors"
	"fmt"
	"time"
)

const (
	DefaultSynthesisConcurrency   = 4
	DefaultAudioConcurrency       = 2
	DefaultPersistenceConcurrency = 4
	DefaultMaxAttempts            = 3
)

const (
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

// PipelineConfig controls the three generation stages.
type PipelineConfig struct {
	Synthesis   StageConfig
	Audio       StageConfig
	Persistence StageConfig
}

// DefaultPipelineConfig returns the default execution policy.
func DefaultPipelineConfig() PipelineConfig {
	retry := RetryConfig{
		MaxAttempts: DefaultMaxAttempts, InitialBackoff: DefaultInitialBackoff, MaxBackoff: DefaultMaxBackoff,
	}
	return PipelineConfig{
		Synthesis:   StageConfig{Concurrency: DefaultSynthesisConcurrency, Retry: retry},
		Audio:       StageConfig{Concurrency: DefaultAudioConcurrency, Retry: retry},
		Persistence: StageConfig{Concurrency: DefaultPersistenceConcurrency, Retry: retry},
	}
}

func (c PipelineConfig) validate() error {
	for _, stage := range []struct {
		name   Stage
		config StageConfig
	}{
		{StageSynthesis, c.Synthesis},
		{StageAudio, c.Audio},
		{StagePersistence, c.Persistence},
	} {
		if stage.config.Concurrency <= 0 {
			return fmt.Errorf("%s concurrency must be positive", stage.name)
		}
		if err := stage.config.Retry.validate(); err != nil {
			return fmt.Errorf("%s retry: %w", stage.name, err)
		}
	}
	return nil
}

func (c RetryConfig) validate() error {
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
