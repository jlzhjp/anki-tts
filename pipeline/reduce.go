package pipeline

import (
	"context"
	"errors"
)

// Reduce executes a stream and folds item outcomes in emission order. On
// cancellation it returns the partial accumulator together with the context
// error.
func Reduce[T, A any](
	ctx context.Context,
	input Stream[T],
	observer Observer,
	initial A,
	reducer func(A, Result[T]) A,
) (A, error) {
	if reducer == nil {
		return initial, errors.New("pipeline reducer is required")
	}
	accumulator := initial
	err := consume(ctx, input, observer, func(result Result[T]) {
		accumulator = reducer(accumulator, result)
	})
	return accumulator, err
}
