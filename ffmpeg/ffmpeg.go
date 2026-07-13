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

// CommandRunner abstracts command lookup and execution for tests.
type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, path string, args []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// Transformer transforms generated voices with FFmpeg.
type Transformer struct {
	path          string
	format        string
	args          []string
	runner        CommandRunner
	maxOutputSize int
}

// New validates config and verifies that the ffmpeg executable is available.
func New(config Config) (*Transformer, error) {
	return NewWithRunner(config, execRunner{}, defaultMaxOutputSize)
}

// NewWithRunner constructs a transformer with an injectable command runner.
// maxOutputSize is the largest transformed audio payload accepted in bytes.
func NewWithRunner(config Config, runner CommandRunner, maxOutputSize int) (*Transformer, error) {
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

// Transform runs FFmpeg with voice data on stdin and buffers its bounded stdout.
func (t *Transformer) Transform(ctx context.Context, voice tts.Voice) (tts.Voice, error) {
	args := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0"}
	args = append(args, t.args...)
	args = append(args, "-f", t.format, "pipe:1")

	stdout := &limitedBuffer{limit: t.maxOutputSize}
	stderr := &boundedBuffer{limit: maxStderrSize}
	err := t.runner.Run(ctx, t.path, args, bytes.NewReader(voice.Data), stdout, stderr)
	if stdout.exceeded {
		return tts.Voice{}, fmt.Errorf("transform audio with FFmpeg: %w (%d bytes)", errOutputTooLarge, t.maxOutputSize)
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tts.Voice{}, fmt.Errorf("transform audio with FFmpeg: %w", ctxErr)
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return tts.Voice{}, fmt.Errorf("transform audio with FFmpeg: %w: %s", err, message)
		}
		return tts.Voice{}, fmt.Errorf("transform audio with FFmpeg: %w", err)
	}
	if stdout.Len() == 0 {
		return tts.Voice{}, errors.New("transform audio with FFmpeg: command produced empty output")
	}

	voice.Data = append([]byte(nil), stdout.Bytes()...)
	voice.Format = t.format
	return voice, nil
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

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Len()
	if len(p) <= remaining {
		return b.Buffer.Write(p)
	}
	b.exceeded = true
	if remaining > 0 {
		_, _ = b.Buffer.Write(p[:remaining])
	}
	return remaining, errOutputTooLarge
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

func (execRunner) Run(ctx context.Context, path string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

var _ tts.Transformer = (*Transformer)(nil)
