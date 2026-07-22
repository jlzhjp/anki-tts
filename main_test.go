package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/workflow"
)

func TestLoadConfigAndBuildServices(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`[openrouter]
model = "openai/tts"
voice = "nova"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	services, err := buildServices(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := services.Services(); len(got) != 1 || got[0].Name != "OpenRouter" {
		t.Fatalf("services = %+v", got)
	}
}

func TestBuildServicesWithoutProviders(t *testing.T) {
	services, err := buildServices(config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(services.Services()) != 0 {
		t.Fatalf("services = %+v", services.Services())
	}
}

func TestLoadFFmpegConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`[ffmpeg]
format = "mp3"
args = ["-codec:a", "libmp3lame", "-b:a", "64k"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FFmpeg == nil || cfg.FFmpeg.Format != "mp3" || len(cfg.FFmpeg.Args) != 4 || cfg.FFmpeg.Args[3] != "64k" {
		t.Fatalf("FFmpeg config = %+v", cfg.FFmpeg)
	}
}

func TestBuildTransformer(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		transformer, err := buildTransformer(config{})
		if err != nil || transformer != nil {
			t.Fatalf("transformer=%v error=%v", transformer, err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		directory := t.TempDir()
		executable := filepath.Join(directory, "ffmpeg")
		if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", directory)
		transformer, err := buildTransformer(configFromFFmpeg("mp3"))
		if err != nil || transformer == nil {
			t.Fatalf("transformer=%v error=%v", transformer, err)
		}
	})

	for _, test := range []struct {
		name   string
		format string
		want   string
	}{
		{name: "missing format", format: "", want: "format must be"},
		{name: "unsafe extension", format: "../mp3", want: "format must be"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildTransformer(configFromFFmpeg(test.format))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}

	t.Run("missing executable", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := buildTransformer(configFromFFmpeg("mp3"))
		if err == nil || !strings.Contains(err.Error(), "find ffmpeg executable") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestLoadFFmpegConfigRejectsNonStringArgument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[ffmpeg]\nformat = \"mp3\"\nargs = [1]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "string") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadExecutionConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `[openrouter]
model = "openai/tts"
concurrency = 7
[openrouter.retry]
max_attempts = 4
initial_backoff = "250ms"
max_backoff = "3s"
[ffmpeg]
format = "mp3"
concurrency = 3
[anki]
concurrency = 5
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := pipelineConfig(cfg)
	if pipeline.Synthesis.Concurrency != 7 || pipeline.Synthesis.Retry.MaxAttempts != 4 || pipeline.Synthesis.Retry.InitialBackoff != 250*time.Millisecond || pipeline.Synthesis.Retry.MaxBackoff != 3*time.Second {
		t.Fatalf("synthesis config = %+v", pipeline.Synthesis)
	}
	if pipeline.Audio.Concurrency != 3 || pipeline.Persistence.Concurrency != 5 {
		t.Fatalf("pipeline config = %+v", pipeline)
	}
}

func TestPipelineConfigUsesDefaultsAndRejectsInvalidValues(t *testing.T) {
	defaults := pipelineConfig(config{})
	if defaults.Synthesis.Concurrency != 4 || defaults.Audio.Concurrency != 2 || defaults.Persistence.Concurrency != 4 {
		t.Fatalf("defaults = %+v", defaults)
	}
	defaults.Persistence.Concurrency = -1
	if _, err := workflow.NewWithConfig(anki.NewClient(), nil, nil, defaults); err == nil || !strings.Contains(err.Error(), "persistence concurrency") {
		t.Fatalf("error = %v", err)
	}
}

func configFromFFmpeg(format string) config {
	cfg := config{}
	cfg.FFmpeg = &ffmpegConfig{Format: format}
	return cfg
}
