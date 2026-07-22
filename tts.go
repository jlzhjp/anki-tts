package ankitts

import (
	"context"
	"io"
)

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
// it. LoadCost may perform network I/O and remains valid after Close.
type Voice interface {
	io.ReadCloser
	Format() string
	MediaType() string
	LoadCost(ctx context.Context) (float64, error)
}

// AudioProcessor associates a pipeline stage name with a Transformer.
type AudioProcessor struct {
	Name        string
	Transformer Transformer
}
