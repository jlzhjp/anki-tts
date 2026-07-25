package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Retry wraps a transform with a validated, named retry policy.
func Retry[I, O any](config RetryConfig, operation string, transform Transform[I, O]) (Transform[I, O], error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("retry operation %q: %w", operation, err)
	}
	if operation == "" {
		return nil, errors.New("retry operation name is required")
	}
	if transform == nil {
		return nil, fmt.Errorf("retry operation %q has no transform", operation)
	}
	return func(ctx context.Context, input I) (O, error) {
		var output O
		attempts, err := retry(
			ctx,
			config,
			func(attempt int) {
				report(ctx, config, &Event{
					Kind: Started, Operation: operation, Attempt: attempt,
				})
			},
			func(attempt int, retryAt time.Time, err error) {
				report(ctx, config, &Event{
					Kind: Retrying, Operation: operation, Attempt: attempt,
					RetryAt: retryAt, Err: err,
				})
			},
			func() error {
				value, err := transform(ctx, input)
				if err == nil {
					output = value
				}
				return err
			},
		)
		if attempts > 0 {
			kind := Completed
			if err != nil {
				kind = Failed
			}
			report(ctx, config, &Event{
				Kind: kind, Operation: operation, Attempt: attempts, Err: err,
			})
		}
		return output, err
	}, nil
}

func retry(
	ctx context.Context,
	config RetryConfig,
	start func(int),
	retrying func(int, time.Time, error),
	operation func() error,
) (int, error) {
	delay := config.InitialBackoff
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return attempt - 1, err
		}
		if start != nil {
			start(attempt)
		}
		err := operation()
		if err == nil {
			return attempt, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || attempt == config.MaxAttempts {
			return attempt, err
		}
		retryAt := time.Now().Add(delay)
		if retrying != nil {
			retrying(attempt, retryAt, err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return attempt, ctx.Err()
		case <-timer.C:
		}
		if delay < config.MaxBackoff {
			if delay > config.MaxBackoff-delay {
				delay = config.MaxBackoff
			} else {
				delay *= 2
			}
		}
	}
	return config.MaxAttempts, errors.New("retry loop exhausted")
}
