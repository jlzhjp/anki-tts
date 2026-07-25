package pipeline

import "context"

// Transform maps one pipeline value to another.
type Transform[I, O any] func(context.Context, I) (O, error)

// Stream is a lazy, typed pipeline execution plan.
type Stream[T any] struct {
	start func(*execution) <-chan entry[T]
}

type entry[T any] struct {
	index   int
	value   T
	stage   string
	failure error
}

type execution struct {
	ctx      context.Context
	observer Observer
}
