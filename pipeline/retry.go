package pipeline

import (
	"context"
	"errors"
	"time"
)

func retry(ctx context.Context, config RetryConfig, observe func(int, time.Time, error), operation func() error) (int, error) {
	delay := config.InitialBackoff
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return attempt - 1, err
		}
		err := operation()
		if err == nil {
			return attempt, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || attempt == config.MaxAttempts {
			return attempt, err
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
