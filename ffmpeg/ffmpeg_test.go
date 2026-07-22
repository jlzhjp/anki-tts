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

func TestTransformSeparatesMuxerFromExtension(t *testing.T) {
	runner := &fakeRunner{output: []byte("transformed")}
	transformer, err := NewWithRunner(Config{Format: FormatAAC}, runner, 1024)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := transformer.Transform(context.Background(), &testVoice{ReadCloser: io.NopCloser(strings.NewReader("input"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(voice); err != nil {
		t.Fatal(err)
	}
	if voice.Format() != "aac" {
		t.Fatalf("extension = %q", voice.Format())
	}
	if got := runner.args[len(runner.args)-2]; got != "adts" {
		t.Fatalf("muxer = %q", got)
	}
}

func TestTransformErrors(t *testing.T) {
	runErr := errors.New("exit status 1")
	spawnErr := errors.New("fork/exec ffmpeg: resource unavailable")
	tests := []struct {
		name       string
		runner     *fakeRunner
		limit      int64
		cancel     bool
		want       string
		wantIs     error
		startFail  bool
		wantKilled bool
	}{
		{name: "spawn", runner: &fakeRunner{startErr: spawnErr}, limit: 10, want: "resource unavailable", wantIs: spawnErr, startFail: true},
		{name: "execution with stderr", runner: &fakeRunner{waitErr: runErr, stderr: "bad codec", output: []byte("partial")}, limit: 10, want: "exit status 1: bad codec", wantIs: runErr},
		{name: "empty output", runner: &fakeRunner{}, limit: 10, want: "command produced empty output"},
		{name: "size limit", runner: &fakeRunner{output: []byte("12345")}, limit: 4, want: "exceeds configured size limit (4 bytes)", wantIs: errOutputTooLarge, wantKilled: true},
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
			if test.runner.killed != test.wantKilled {
				t.Fatalf("killed = %v, want %v", test.runner.killed, test.wantKilled)
			}
		})
	}
}

func TestOutputStreamCloseTerminatesOnce(t *testing.T) {
	runner := &fakeRunner{output: []byte("unread")}
	transformer, err := NewWithRunner(Config{Format: "mp3"}, runner, 10)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := transformer.Transform(context.Background(), &testVoice{ReadCloser: io.NopCloser(strings.NewReader("input"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := voice.Close(); err == nil || !strings.Contains(err.Error(), "closed before completion") {
		t.Fatalf("close error = %v", err)
	}
	if !runner.killed || !runner.waited {
		t.Fatalf("killed = %v, waited = %v", runner.killed, runner.waited)
	}
	if err := voice.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
}

func TestOutputStreamResultError(t *testing.T) {
	streamErr := errors.New("stream failed")
	waitErr := errors.New("exit status 1")
	tests := []struct {
		name   string
		result outputStreamResult
		want   string
		is     error
	}{
		{name: "success", result: outputStreamResult{bytesRead: 1}},
		{name: "limit takes precedence", result: outputStreamResult{outputErr: errOutputTooLarge, contextErr: context.Canceled, processErr: waitErr, maxBytes: 4}, want: "exceeds configured size limit (4 bytes)", is: errOutputTooLarge},
		{name: "stream takes precedence", result: outputStreamResult{outputErr: streamErr, contextErr: context.Canceled, processErr: waitErr}, want: "stream failed", is: streamErr},
		{name: "context takes precedence", result: outputStreamResult{contextErr: context.Canceled, processErr: waitErr}, want: "context canceled", is: context.Canceled},
		{name: "process with diagnostic", result: outputStreamResult{processErr: waitErr, diagnostic: "bad codec"}, want: "exit status 1: bad codec", is: waitErr},
		{name: "process without diagnostic", result: outputStreamResult{processErr: waitErr}, want: "exit status 1", is: waitErr},
		{name: "empty output", result: outputStreamResult{}, want: "command produced empty output"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.result.resultError()
			if test.want == "" {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
			if test.is != nil && !errors.Is(err, test.is) {
				t.Fatalf("error = %v, want errors.Is %v", err, test.is)
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
	killed   bool
}

func (f *fakeRunner) LookPath(string) (string, error) {
	if f.lookErr != nil {
		return "", f.lookErr
	}
	return "/test/ffmpeg", nil
}

func (f *fakeRunner) Start(_ context.Context, path string, args []string, stdin io.Reader, stderr io.Writer) (RunningCommand, error) {
	f.path = path
	f.args = append([]string(nil), args...)
	input, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	f.input = input
	if f.startErr != nil {
		return nil, f.startErr
	}
	_, _ = io.WriteString(stderr, f.stderr)
	return &fakeCommand{ReadCloser: io.NopCloser(bytes.NewReader(f.output)), runner: f}, nil
}

type fakeCommand struct {
	io.ReadCloser
	runner *fakeRunner
}

func (c *fakeCommand) Wait() error {
	c.runner.waited = true
	return c.runner.waitErr
}

func (c *fakeCommand) Kill() error {
	c.runner.killed = true
	return nil
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
