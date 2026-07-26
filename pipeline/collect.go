package pipeline

import (
	"cmp"
	"context"
	"errors"
	"slices"
)

// Result is the terminal state of one input item.
type Result[T any] struct {
	Value T
	Err   error
	Stage string
	Index int
}

// Collect executes a stream and returns input-ordered item outcomes. Stage is
// empty on success and identifies the stage that failed otherwise. A canceled
// execution may return only the items that entered the pipeline.
func Collect[T any](ctx context.Context, input Stream[T], observer Observer) ([]Result[T], error) {
	results := make([]Result[T], 0)
	err := consume(ctx, input, observer, func(result Result[T]) {
		results = append(results, result)
	})
	slices.SortFunc(results, func(left, right Result[T]) int {
		return cmp.Compare(left.Index, right.Index)
	})
	return results, err
}

func consume[T any](
	ctx context.Context,
	input Stream[T],
	observer Observer,
	consumeResult func(Result[T]),
) error {
	if input.start == nil {
		return errors.New("pipeline stream is not initialized")
	}
	output := input.start(&execution{ctx: ctx, observer: observer})
	for current := range output {
		consumeResult(Result[T]{
			Index: current.index, Value: current.value,
			Stage: current.stage, Err: current.failure,
		})
	}
	return ctx.Err()
}
