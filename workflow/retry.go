package workflow

import (
	"context"
	"errors"
	"time"
)

type retryObserver func(attempt int, retryAt time.Time, err error)

func retry[T any](ctx context.Context, config RetryConfig, observe retryObserver, operation func() (T, error)) (T, error) {
	var zero T
	delay := config.InitialBackoff
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		value, err := operation()
		if err == nil {
			return value, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || attempt == config.MaxAttempts {
			return zero, err
		}
		retryAt := time.Now().Add(delay)
		if observe != nil {
			observe(attempt+1, retryAt, err)
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
			return zero, ctx.Err()
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
	return zero, errors.New("retry loop exhausted")
}
