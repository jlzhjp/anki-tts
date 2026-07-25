package pipeline

import (
	"context"
	"time"
)

type testItem struct {
	stages []string
	id     int
}

func appendStage(name string) Transform[testItem, testItem] {
	return func(_ context.Context, item testItem) (testItem, error) {
		item.stages = append(item.stages, name)
		return item, nil
	}
}

func retryConfig(attempts int) RetryConfig {
	return RetryConfig{
		MaxAttempts:    attempts,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	}
}
