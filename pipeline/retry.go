package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// DefaultMaxAttempts is the default total number of operation attempts.
	DefaultMaxAttempts = 3
	// DefaultInitialBackoff is the delay before the first retry.
	DefaultInitialBackoff = 500 * time.Millisecond
	// DefaultMaxBackoff caps exponential retry delays.
	DefaultMaxBackoff = 5 * time.Second
)

// RetryConfig controls context-aware exponential retry backoff.
type RetryConfig struct {
	MaxAttempts    int           `toml:"max_attempts"`
	InitialBackoff time.Duration `toml:"initial_backoff"`
	MaxBackoff     time.Duration `toml:"max_backoff"`
}

// RetryPredicate reports whether an operation error should be retried while
// the parent context remains active and attempts remain.
type RetryPredicate func(error) bool

// RetryOption customizes one retry transform.
type RetryOption func(*retryOptions)

type retryOptions struct {
	predicate    RetryPredicate
	hasPredicate bool
}

// WithRetryPredicate supplies operation-specific error classification.
func WithRetryPredicate(predicate RetryPredicate) RetryOption {
	return func(options *retryOptions) {
		options.predicate = predicate
		options.hasPredicate = true
	}
}

// Retry wraps a transform with a validated, named retry policy.
func Retry[I, O any](
	config RetryConfig,
	operation string,
	transform Transform[I, O],
	options ...RetryOption,
) (Transform[I, O], error) {
	if operation == "" {
		return nil, errors.New("retry operation name is required")
	}
	if transform == nil {
		return nil, fmt.Errorf("retry operation %q has no transform", operation)
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("retry operation %q: %w", operation, err)
	}
	retryOptions := retryOptions{predicate: defaultRetryPredicate}
	for _, option := range options {
		if option != nil {
			option(&retryOptions)
		}
	}
	if retryOptions.hasPredicate && retryOptions.predicate == nil {
		return nil, fmt.Errorf("retry operation %q has a nil retry predicate", operation)
	}

	return func(ctx context.Context, input I) (O, error) {
		var zero O
		delay := config.InitialBackoff
		for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			report(ctx, config, &Event{
				Kind: Started, Operation: operation, Attempt: attempt,
			})

			output, operationErr := transform(ctx, input)
			if operationErr == nil {
				report(ctx, config, &Event{
					Kind: Completed, Operation: operation, Attempt: attempt,
				})
				return output, nil
			}

			if contextErr := ctx.Err(); contextErr != nil {
				report(ctx, config, &Event{
					Kind: Failed, Operation: operation, Attempt: attempt, Err: contextErr,
				})
				return zero, contextErr
			}
			if attempt == config.MaxAttempts || !retryOptions.predicate(operationErr) {
				report(ctx, config, &Event{
					Kind: Failed, Operation: operation, Attempt: attempt, Err: operationErr,
				})
				return zero, operationErr
			}

			retryAt := time.Now().Add(delay)
			report(ctx, config, &Event{
				Kind: Retrying, Operation: operation, Attempt: attempt,
				RetryAt: retryAt, Err: operationErr,
			})
			if err := waitForRetry(ctx, delay); err != nil {
				report(ctx, config, &Event{
					Kind: Failed, Operation: operation, Attempt: attempt, Err: err,
				})
				return zero, err
			}
			delay = nextBackoff(delay, config.MaxBackoff)
		}
		return zero, errors.New("retry loop exhausted")
	}, nil
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

func defaultRetryPredicate(err error) bool {
	return !errors.Is(err, context.Canceled)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextBackoff(delay, maximum time.Duration) time.Duration {
	if delay >= maximum || delay > maximum-delay {
		return maximum
	}
	return delay * 2
}
