package pipeline

import "slices"

// FromSlice creates a lazy source from a snapshot of values.
func FromSlice[T any](values []T) Stream[T] {
	values = slices.Clone(values)
	return Stream[T]{start: func(run *execution) <-chan entry[T] {
		output := make(chan entry[T])
		go func() {
			defer close(output)
			for index, value := range values {
				if run.ctx.Err() != nil {
					return
				}
				select {
				case output <- entry[T]{index: index, value: value}:
				case <-run.ctx.Done():
					return
				}
			}
		}()
		return output
	}}
}
