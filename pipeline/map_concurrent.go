package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// MapConcurrent appends a bounded, named transform to a stream.
func MapConcurrent[I, O any](input Stream[I], stage string, concurrency int, transform Transform[I, O]) (Stream[O], error) {
	if input.start == nil {
		return Stream[O]{}, errors.New("pipeline stream is not initialized")
	}
	if stage == "" {
		return Stream[O]{}, errors.New("pipeline stage name is required")
	}
	if concurrency <= 0 {
		return Stream[O]{}, fmt.Errorf("pipeline stage %q concurrency must be positive", stage)
	}
	if transform == nil {
		return Stream[O]{}, fmt.Errorf("pipeline stage %q has no transform", stage)
	}

	return Stream[O]{start: func(run *execution) <-chan entry[O] {
		stageInput := input.start(run)
		output := make(chan entry[O])
		var workers sync.WaitGroup
		for range concurrency {
			workers.Go(func() {
				for current := range stageInput {
					next := entry[O]{index: current.index, stage: current.stage, failure: current.failure}
					if current.failure == nil {
						if err := run.ctx.Err(); err != nil {
							next.stage, next.failure = stage, err
						} else {
							ctx := context.WithValue(run.ctx, scopeContextKey{}, operationScope{
								index: current.index, stage: stage, observer: run.observer,
							})
							value, err := transform(ctx, current.value)
							if err != nil {
								next.stage, next.failure = stage, err
							} else {
								next.value = value
							}
						}
					}
					output <- next
				}
			})
		}
		go func() {
			workers.Wait()
			close(output)
		}()
		return output
	}}, nil
}
