// Package pipeline builds lazy, typed processing plans from named concurrent
// stages. Each terminal collector starts a new execution of its input stream.
//
// Concurrent stages emit values in completion order. Collect restores source
// index order, while other terminal collectors consume values as they arrive.
// When an item fails, later stages are bypassed and the transform value returned
// with the error is discarded. Terminal collectors drain in-flight work before
// returning, including after cancellation.
package pipeline

import "context"

// Transform maps one pipeline value to another.
type Transform[I, O any] func(context.Context, I) (O, error)

// Stream is a lazy, typed pipeline execution plan.
type Stream[T any] struct {
	start func(*execution) <-chan entry[T]
}

type entry[T any] struct {
	value   T
	failure error
	stage   string
	index   int
}

type execution struct {
	ctx      context.Context
	observer Observer
}
