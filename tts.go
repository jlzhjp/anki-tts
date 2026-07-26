package ankitts

import (
	"context"
	"errors"
	"io"
)

// ErrCostUnavailable indicates that a voice has no cost information.
var ErrCostUnavailable = errors.New("voice cost is unavailable")

// CostLoader loads the cost associated with generated or transformed audio.
type CostLoader func(context.Context) (float64, error)

// CombineCostLoaders returns a loader that sums its inputs in order.
func CombineCostLoaders(loaders ...CostLoader) CostLoader {
	return func(ctx context.Context) (float64, error) {
		var total float64
		for _, load := range loaders {
			if load == nil {
				return 0, ErrCostUnavailable
			}
			cost, err := load(ctx)
			if err != nil {
				return 0, err
			}
			total += cost
		}
		return total, nil
	}
}

// Service generates speech from text. Implementations must support concurrent
// Generate calls from the selected service pipeline stage.
type Service interface {
	Generate(ctx context.Context, input Input) (Voice, error)
}

// Transformer applies a provider-neutral transformation to generated audio.
// Implementations must support concurrent Transform calls. Transform consumes
// ownership of the input Voice: on failure it closes the input; on success the
// returned Voice owns the input and closes it when appropriate.
type Transformer interface {
	Transform(ctx context.Context, voice Voice) (Voice, error)
}

// Input describes text to synthesize.
type Input struct {
	Text string
}

// Voice is a generated audio stream with provider metadata. Callers must close
// it. CostLoader returns a function that remains valid after Close.
type Voice interface {
	io.ReadCloser
	Format() string
	MediaType() string
	CostLoader() CostLoader
}

// AudioProcessor associates a pipeline stage name with a Transformer.
type AudioProcessor struct {
	Transformer Transformer
	Name        string
}
