package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestTransformStreamsAudio(t *testing.T) {
	runner := &fakeRunner{output: []byte("transformed")}
	transformer, err := NewWithRunner(Config{
		Format: "MP3",
		Args:   []string{"-codec:a", "libmp3lame", "-b:a", "64k"},
	}, runner, 1024)
	if err != nil {
		t.Fatal(err)
	}
	input := &trackedReadCloser{Reader: strings.NewReader("provider audio")}
	voice, err := transformer.Transform(context.Background(), &testVoice{
		ReadCloser: input,
		mediaType:  "audio/wav",
		format:     "wav",
		cost:       0.0025,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.waited {
		t.Fatal("FFmpeg process was awaited before its output was consumed")
	}
	if runner.path != "/test/ffmpeg" {
		t.Fatalf("path = %q", runner.path)
	}
	wantArgs := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-codec:a", "libmp3lame", "-b:a", "64k", "-f", "mp3", "pipe:1"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("args = %q, want %q", runner.args, wantArgs)
	}
	if string(runner.input) != "provider audio" {
		t.Fatalf("stdin = %q", runner.input)
	}
	output, err := io.ReadAll(voice)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "transformed" || voice.Format() != "mp3" || voice.MediaType() != "audio/wav" {
		t.Fatalf("voice = %+v output=%q", voice, output)
	}
	if !runner.waited {
		t.Fatal("FFmpeg process was not awaited after its output was consumed")
	}
	if !input.closed {
		t.Fatal("input stream was not closed")
	}
	cost, err := voice.LoadCost(context.Background())
	if err != nil || cost != 0.0025 {
		t.Fatalf("delegated cost = %v, error = %v", cost, err)
	}
}

func TestTransformErrors(t *testing.T) {
	runErr := errors.New("exit status 1")
	spawnErr := errors.New("fork/exec ffmpeg: resource unavailable")
	tests := []struct {
		name      string
		runner    *fakeRunner
		limit     int64
		cancel    bool
		want      string
		wantIs    error
		startFail bool
	}{
		{name: "spawn", runner: &fakeRunner{startErr: spawnErr}, limit: 10, want: "resource unavailable", wantIs: spawnErr, startFail: true},
		{name: "execution with stderr", runner: &fakeRunner{waitErr: runErr, stderr: "bad codec", output: []byte("partial")}, limit: 10, want: "exit status 1: bad codec", wantIs: runErr},
		{name: "empty output", runner: &fakeRunner{}, limit: 10, want: "command produced empty output"},
		{name: "size limit", runner: &fakeRunner{output: []byte("12345")}, limit: 4, want: "exceeds configured size limit (4 bytes)", wantIs: errOutputTooLarge},
		{name: "cancellation", runner: &fakeRunner{waitErr: errors.New("killed")}, limit: 10, cancel: true, want: "context canceled", wantIs: context.Canceled},
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
			voice, err := transformer.Transform(ctx, &testVoice{ReadCloser: io.NopCloser(strings.NewReader("input"))})
			if test.startFail {
				if err == nil || !strings.Contains(err.Error(), test.want) || !errors.Is(err, test.wantIs) {
					t.Fatalf("start error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(voice)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("stream error = %v, want containing %q", err, test.want)
			}
			if test.wantIs != nil && !errors.Is(err, test.wantIs) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.wantIs)
			}
		})
	}
}

func TestTransformBoundsStderr(t *testing.T) {
	runner := &fakeRunner{waitErr: errors.New("failed"), stderr: strings.Repeat("x", maxStderrSize+10)}
	transformer, err := NewWithRunner(Config{Format: "mp3"}, runner, 1024)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := transformer.Transform(context.Background(), &testVoice{ReadCloser: io.NopCloser(strings.NewReader("input"))})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(voice)
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
	lookErr  error
	startErr error
	waitErr  error
	output   []byte
	stderr   string
	path     string
	args     []string
	input    []byte
	waited   bool
}

func (f *fakeRunner) LookPath(string) (string, error) {
	if f.lookErr != nil {
		return "", f.lookErr
	}
	return "/test/ffmpeg", nil
}

func (f *fakeRunner) Start(_ context.Context, path string, args []string, stdin io.Reader, stderr io.Writer) (io.ReadCloser, func() error, func() error, error) {
	f.path = path
	f.args = append([]string(nil), args...)
	input, err := io.ReadAll(stdin)
	if err != nil {
		return nil, nil, nil, err
	}
	f.input = input
	if f.startErr != nil {
		return nil, nil, nil, f.startErr
	}
	_, _ = io.WriteString(stderr, f.stderr)
	return io.NopCloser(bytes.NewReader(f.output)), func() error { f.waited = true; return f.waitErr }, func() error { return nil }, nil
}

type trackedReadCloser struct {
	io.Reader
	closed bool
}

type testVoice struct {
	io.ReadCloser
	mediaType string
	format    string
	cost      float64
}

func (v *testVoice) Format() string    { return v.format }
func (v *testVoice) MediaType() string { return v.mediaType }
func (v *testVoice) LoadCost(context.Context) (float64, error) {
	return v.cost, nil
}

func (r *trackedReadCloser) Close() error {
	r.closed = true
	return nil
}
