// Package ffmpeg transforms generated audio by running the FFmpeg command.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"unicode"

	"jlzhjp.dev/anki-tts/tts"
)

const (
	defaultMaxOutputSize = 32 << 20 // 32 MiB
	maxStderrSize        = 64 << 10 // 64 KiB
)

var errOutputTooLarge = errors.New("FFmpeg output exceeds configured size limit")

// Config describes the optional FFmpeg output pipeline.
type Config struct {
	Format string   `toml:"format"`
	Args   []string `toml:"args"`
}

// CommandRunner starts commands and exposes their stdout and lifecycle hooks.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Start(ctx context.Context, path string, args []string, stdin io.Reader, stderr io.Writer) (stdout io.ReadCloser, wait func() error, kill func() error, err error)
}

// Transformer transforms audio streams with FFmpeg.
type Transformer struct {
	path          string
	format        string
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
	format := strings.TrimSpace(config.Format)
	if !safeFormat(format) {
		return nil, fmt.Errorf("configure FFmpeg: format must be a non-empty filename extension containing only ASCII letters and digits, got %q", config.Format)
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
		format:        strings.ToLower(format),
		args:          append([]string(nil), config.Args...),
		runner:        runner,
		maxOutputSize: maxOutputSize,
	}, nil
}

// Transform starts FFmpeg and returns its stdout as a bounded stream.
func (t *Transformer) Transform(ctx context.Context, audio tts.AudioStream) (tts.AudioStream, error) {
	if audio.Data == nil {
		return tts.AudioStream{}, errors.New("transform audio with FFmpeg: input data is required")
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0"}
	args = append(args, t.args...)
	args = append(args, "-f", t.format, "pipe:1")

	stderr := &boundedBuffer{limit: maxStderrSize}
	stdout, wait, kill, err := t.runner.Start(ctx, t.path, args, audio.Data, stderr)
	if err != nil {
		_ = audio.Data.Close()
		return tts.AudioStream{}, fmt.Errorf("transform audio with FFmpeg: start command: %w", err)
	}
	return tts.AudioStream{
		Data: &outputStream{
			ctx:    ctx,
			stdout: stdout,
			input:  audio.Data,
			stderr: stderr,
			wait:   wait,
			kill:   kill,
			limit:  t.maxOutputSize,
		},
		MediaType: audio.MediaType,
		Format:    t.format,
	}, nil
}

type outputStream struct {
	ctx    context.Context
	stdout io.ReadCloser
	input  io.Closer
	stderr *boundedBuffer
	wait   func() error
	kill   func() error
	limit  int64
	read   int64
	done   bool
}

func (s *outputStream) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	remaining := s.limit - s.read
	readBuffer := p
	if int64(len(readBuffer)) > remaining+1 {
		readBuffer = readBuffer[:remaining+1]
	}
	n, readErr := s.stdout.Read(readBuffer)
	if int64(n) > remaining {
		s.read += int64(n)
		return 0, s.finish(errOutputTooLarge, true)
	}
	s.read += int64(n)
	if readErr == nil {
		return n, nil
	}
	if errors.Is(readErr, io.EOF) {
		finishErr := s.finish(nil, false)
		if finishErr != nil {
			return n, finishErr
		}
		return n, io.EOF
	}
	return n, s.finish(fmt.Errorf("read stdout: %w", readErr), true)
}

func (s *outputStream) Close() error {
	if s.done {
		return nil
	}
	return s.finish(errors.New("output stream closed before completion"), true)
}

func (s *outputStream) finish(streamErr error, terminate bool) error {
	if s.done {
		return streamErr
	}
	s.done = true
	if terminate {
		_ = s.kill()
	}
	_ = s.stdout.Close()
	_ = s.input.Close()
	waitErr := s.wait()
	if errors.Is(streamErr, errOutputTooLarge) {
		return fmt.Errorf("transform audio with FFmpeg: %w (%d bytes)", streamErr, s.limit)
	}
	if streamErr != nil {
		return fmt.Errorf("transform audio with FFmpeg: %w", streamErr)
	}
	if ctxErr := s.ctx.Err(); ctxErr != nil {
		return fmt.Errorf("transform audio with FFmpeg: %w", ctxErr)
	}
	if waitErr != nil {
		message := strings.TrimSpace(s.stderr.String())
		if message != "" {
			return fmt.Errorf("transform audio with FFmpeg: %w: %s", waitErr, message)
		}
		return fmt.Errorf("transform audio with FFmpeg: %w", waitErr)
	}
	if s.read == 0 {
		return errors.New("transform audio with FFmpeg: command produced empty output")
	}
	return nil
}

func safeFormat(format string) bool {
	if format == "" {
		return false
	}
	for _, r := range format {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

type boundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = b.buffer.Write(chunk)
	}
	if len(p) > remaining {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	message := b.buffer.String()
	if b.truncated {
		message += " [truncated]"
	}
	return message
}

type execRunner struct{}

func (execRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (execRunner) Start(ctx context.Context, path string, args []string, stdin io.Reader, stderr io.Writer) (io.ReadCloser, func() error, func() error, error) {
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = stdin
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		return nil, nil, nil, err
	}
	return stdout, command.Wait, command.Process.Kill, nil
}

var _ tts.Transformer = (*Transformer)(nil)
