package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"jlzhjp.dev/anki-tts/tts"
)

func TestTransformCapturesAudioAndPreservesMetadata(t *testing.T) {
	runner := &fakeRunner{output: []byte("transformed")}
	transformer, err := NewWithRunner(Config{
		Format: "MP3",
		Args:   []string{"-codec:a", "libmp3lame", "-b:a", "64k"},
	}, runner, 1024)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := transformer.Transform(context.Background(), tts.Voice{
		Data:         []byte("provider audio"),
		MediaType:    "audio/wav",
		Format:       "wav",
		GenerationID: "generation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.path != "/test/ffmpeg" {
		t.Fatalf("path = %q", runner.path)
	}
	wantArgs := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-codec:a", "libmp3lame", "-b:a", "64k", "-f", "mp3", "pipe:1"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("args = %q, want %q", runner.args, wantArgs)
	}
	if string(runner.input) != "provider audio" || string(voice.Data) != "transformed" {
		t.Fatalf("stdin=%q output=%q", runner.input, voice.Data)
	}
	if voice.Format != "mp3" || voice.MediaType != "audio/wav" || voice.GenerationID != "generation-1" {
		t.Fatalf("voice metadata = %+v", voice)
	}
}

func TestTransformErrors(t *testing.T) {
	runErr := errors.New("exit status 1")
	spawnErr := errors.New("fork/exec ffmpeg: resource unavailable")
	tests := []struct {
		name   string
		runner *fakeRunner
		limit  int
		cancel bool
		want   string
		wantIs error
	}{
		{name: "spawn", runner: &fakeRunner{runErr: spawnErr}, limit: 10, want: "resource unavailable", wantIs: spawnErr},
		{name: "execution with stderr", runner: &fakeRunner{runErr: runErr, stderr: "bad codec"}, limit: 10, want: "exit status 1: bad codec", wantIs: runErr},
		{name: "empty output", runner: &fakeRunner{}, limit: 10, want: "command produced empty output"},
		{name: "size limit", runner: &fakeRunner{output: []byte("12345")}, limit: 4, want: "exceeds configured size limit (4 bytes)", wantIs: errOutputTooLarge},
		{name: "cancellation", runner: &fakeRunner{}, limit: 10, cancel: true, want: "context canceled", wantIs: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transformer, err := NewWithRunner(Config{Format: "mp3"}, test.runner, test.limit)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if test.cancel {
				cancel()
			} else {
				defer cancel()
			}
			_, err = transformer.Transform(ctx, tts.Voice{Data: []byte("input")})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if test.wantIs != nil && !errors.Is(err, test.wantIs) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.wantIs)
			}
		})
	}
}

func TestTransformBoundsStderr(t *testing.T) {
	runner := &fakeRunner{runErr: errors.New("failed"), stderr: strings.Repeat("x", maxStderrSize+10)}
	transformer, err := NewWithRunner(Config{Format: "mp3"}, runner, 1024)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transformer.Transform(context.Background(), tts.Voice{Data: []byte("input")})
	if err == nil || !strings.Contains(err.Error(), "[truncated]") || len(err.Error()) > maxStderrSize+100 {
		t.Fatalf("bounded stderr error length=%d error=%v", len(err.Error()), err)
	}
}

func TestConfigurationValidation(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		runner CommandRunner
		want   string
	}{
		{name: "missing format", config: Config{}, runner: &fakeRunner{}, want: "format must be"},
		{name: "empty format", config: Config{Format: "  "}, runner: &fakeRunner{}, want: "format must be"},
		{name: "unsafe format", config: Config{Format: "../mp3"}, runner: &fakeRunner{}, want: "format must be"},
		{name: "non-ASCII format", config: Config{Format: "ｍｐ3"}, runner: &fakeRunner{}, want: "format must be"},
		{name: "missing executable", config: Config{Format: "mp3"}, runner: &fakeRunner{lookErr: errors.New("not found")}, want: "find ffmpeg executable: not found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewWithRunner(test.config, test.runner, 1024)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

type fakeRunner struct {
	lookErr error
	runErr  error
	output  []byte
	stderr  string
	path    string
	args    []string
	input   []byte
}

func (f *fakeRunner) LookPath(string) (string, error) {
	if f.lookErr != nil {
		return "", f.lookErr
	}
	return "/test/ffmpeg", nil
}

func (f *fakeRunner) Run(ctx context.Context, path string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.path = path
	f.args = append([]string(nil), args...)
	input, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	f.input = input
	_, _ = io.WriteString(stderr, f.stderr)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.runErr != nil {
		return f.runErr
	}
	_, err = io.Copy(stdout, bytes.NewReader(f.output))
	return err
}
