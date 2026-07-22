// Package ffmpeg transforms generated audio by running the FFmpeg command.
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/internal/streamutil"
)

const (
	defaultMaxOutputSize = 32 << 20 // 32 MiB
	maxStderrSize        = 64 << 10 // 64 KiB
)

var (
	errOutputTooLarge = errors.New("FFmpeg output exceeds configured size limit")
	errOutputClosed   = errors.New("output stream closed before completion")
)

// Config describes the optional FFmpeg output pipeline.
type Config struct {
	Format Format   `toml:"format"`
	Args   []string `toml:"args"`
}

// RunningCommand is a started command whose output can be read.
type RunningCommand interface {
	io.ReadCloser
	Wait() error
	Kill() error
}

// CommandRunner starts commands and returns their output and lifecycle.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Start(ctx context.Context, path string, args []string, stdin io.Reader, stderr io.Writer) (RunningCommand, error)
}

// Transformer transforms audio streams with FFmpeg.
type Transformer struct {
	path          string
	format        Format
	args          []string
	runner        CommandRunner
	maxOutputSize int64
}

// New validates config and verifies that the ffmpeg executable is available.
func New(config Config) (*Transformer, error) {
	return NewWithRunner(config, execRunner{}, defaultMaxOutputSize)
}

// NewWithRunner constructs a transformer with an injectable command runner.
func NewWithRunner(config Config, runner CommandRunner, maxOutputSize int64) (*Transformer, error) {
	format, err := ParseFormat(string(config.Format))
	if err != nil {
		return nil, fmt.Errorf("configure FFmpeg: %w", err)
	}
	if runner == nil {
		return nil, errors.New("configure FFmpeg: command runner is required")
	}
	if maxOutputSize <= 0 {
		return nil, errors.New("configure FFmpeg: maximum output size must be positive")
	}
	path, err := runner.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("configure FFmpeg: find ffmpeg executable: %w", err)
	}
	return &Transformer{
		path:          path,
		format:        format,
		args:          append([]string(nil), config.Args...),
		runner:        runner,
		maxOutputSize: maxOutputSize,
	}, nil
}

// Transform starts FFmpeg and wraps the source voice with transformed audio.
func (t *Transformer) Transform(ctx context.Context, voice ankitts.Voice) (ankitts.Voice, error) {
	if voice == nil {
		return nil, errors.New("transform audio with FFmpeg: input voice is required")
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0"}
	args = append(args, t.args...)
	args = append(args, "-f", t.format.Muxer(), "pipe:1")

	stderr := streamutil.NewBoundedBuffer(maxStderrSize)
	command, err := t.runner.Start(ctx, t.path, args, voice, stderr)
	if err != nil {
		_ = voice.Close()
		return nil, fmt.Errorf("transform audio with FFmpeg: start command: %w", err)
	}
	return &transformedVoice{
		source: voice,
		stream: &outputStream{
			ctx:     ctx,
			command: command,
			output:  streamutil.NewLimitedReader(command, t.maxOutputSize),
			input:   voice,
			stderr:  stderr,
		},
		mediaType: voice.MediaType(),
		format:    t.format.Extension(),
	}, nil
}

type transformedVoice struct {
	source    ankitts.Voice
	stream    io.ReadCloser
	mediaType string
	format    string
}

func (v *transformedVoice) Read(p []byte) (int, error) { return v.stream.Read(p) }
func (v *transformedVoice) Close() error               { return v.stream.Close() }
func (v *transformedVoice) Format() string             { return v.format }
func (v *transformedVoice) MediaType() string          { return v.mediaType }

func (v *transformedVoice) LoadCost(ctx context.Context) (float64, error) {
	return v.source.LoadCost(ctx)
}

type outputStream struct {
	ctx       context.Context
	command   RunningCommand
	output    *streamutil.LimitedReader
	input     io.Closer
	stderr    *streamutil.BoundedBuffer
	finalized bool
}

type outputStreamResult struct {
	outputErr  error
	contextErr error
	processErr error
	diagnostic string
	bytesRead  int64
	maxBytes   int64
}

func (s *outputStream) Read(p []byte) (int, error) {
	if s.finalized {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	n, readErr := s.output.Read(p)
	if readErr == nil {
		return n, nil
	}
	finalErr := s.finalize(readErr)
	if finalErr != nil {
		return n, finalErr
	}
	return n, io.EOF
}

func (s *outputStream) Close() error {
	if s.finalized {
		return nil
	}
	return s.finalize(errOutputClosed)
}

func (s *outputStream) finalize(endErr error) error {
	if s.finalized {
		return endErr
	}
	s.finalized = true

	var outputErr error
	switch {
	case errors.Is(endErr, io.EOF):
		outputErr = nil
	case errors.Is(endErr, streamutil.ErrLimitExceeded):
		outputErr = errOutputTooLarge
	case errors.Is(endErr, errOutputClosed):
		outputErr = errOutputClosed
	default:
		outputErr = fmt.Errorf("read stdout: %w", endErr)
	}
	if outputErr != nil {
		_ = s.command.Kill()
	}
	_ = s.command.Close()
	_ = s.input.Close()
	return outputStreamResult{
		outputErr:  outputErr,
		contextErr: s.ctx.Err(),
		processErr: s.command.Wait(),
		diagnostic: strings.TrimSpace(s.stderr.String()),
		bytesRead:  s.output.BytesRead(),
		maxBytes:   s.output.Limit(),
	}.resultError()
}

func (r outputStreamResult) resultError() error {
	switch {
	case errors.Is(r.outputErr, errOutputTooLarge):
		return fmt.Errorf("transform audio with FFmpeg: %w (%d bytes)", r.outputErr, r.maxBytes)
	case r.outputErr != nil:
		return fmt.Errorf("transform audio with FFmpeg: %w", r.outputErr)
	case r.contextErr != nil:
		return fmt.Errorf("transform audio with FFmpeg: %w", r.contextErr)
	case r.processErr != nil && r.diagnostic != "":
		return fmt.Errorf("transform audio with FFmpeg: %w: %s", r.processErr, r.diagnostic)
	case r.processErr != nil:
		return fmt.Errorf("transform audio with FFmpeg: %w", r.processErr)
	case r.bytesRead == 0:
		return errors.New("transform audio with FFmpeg: command produced empty output")
	default:
		return nil
	}
}

var _ ankitts.Transformer = (*Transformer)(nil)
