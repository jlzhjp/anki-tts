package workflow

import (
	"context"
	"errors"

	"golang.org/x/sync/errgroup"
)

// pipelineItem carries either a stage value or a terminal per-note failure.
// Failed items continue through later operators so every planned note reaches
// the collector exactly once.
type pipelineItem[T any] struct {
	job     preparedJob
	value   T
	failure *ItemResult
}

// mapConcurrent applies transform with a fixed worker count and closes output
// after every worker has drained input. Per-item errors are values, not group
// errors, so one failed note never cancels unrelated work. discard releases a
// successful value if cancellation wins immediately after transformation.
func mapConcurrent[I, O any](
	ctx context.Context,
	workers int,
	stage Stage,
	input <-chan pipelineItem[I],
	output chan<- pipelineItem[O],
	transform func(context.Context, preparedJob, I) (O, error),
	discard func(O),
) error {
	defer close(output)
	if workers <= 0 {
		return errors.New("map concurrent worker count must be positive")
	}
	var group errgroup.Group
	for range workers {
		group.Go(func() error {
			for item := range input {
				if item.failure != nil {
					output <- pipelineItem[O]{job: item.job, failure: item.failure}
					continue
				}
				if err := ctx.Err(); err != nil {
					failure := failedResult(item.job, stage, err)
					output <- pipelineItem[O]{job: item.job, failure: &failure}
					continue
				}
				value, err := transform(ctx, item.job, item.value)
				if err != nil {
					failure := failedResult(item.job, stage, err)
					output <- pipelineItem[O]{job: item.job, failure: &failure}
					continue
				}
				if err := ctx.Err(); err != nil {
					if discard != nil {
						discard(value)
					}
					failure := failedResult(item.job, stage, err)
					output <- pipelineItem[O]{job: item.job, failure: &failure}
					continue
				}
				output <- pipelineItem[O]{job: item.job, value: value}
			}
			return nil
		})
	}
	return group.Wait()
}
