package pipeline

import (
	"context"
	"errors"
)

// Result is the terminal state of one input item.
type Result[T any] struct {
	Value T
	Err   error
	Stage string
	Index int
}

// Collect executes a stream and returns input-ordered item outcomes. A canceled
// execution may return only the items that entered the pipeline.
func Collect[T any](ctx context.Context, input Stream[T], observer Observer) ([]Result[T], error) {
	if input.start == nil {
		return nil, errors.New("pipeline stream is not initialized")
	}
	output := input.start(&execution{ctx: ctx, observer: observer})
	resultsByIndex := make(map[int]Result[T])
	maximumIndex := -1
	for current := range output {
		resultsByIndex[current.index] = Result[T]{
			Index: current.index, Value: current.value,
			Stage: current.stage, Err: current.failure,
		}
		maximumIndex = max(maximumIndex, current.index)
	}
	results := make([]Result[T], maximumIndex+1)
	for index, result := range resultsByIndex {
		results[index] = result
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	return results, nil
}
