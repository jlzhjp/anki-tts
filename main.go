package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"charm.land/bubbletea/v2"
	"github.com/BurntSushi/toml"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/ffmpeg"
	"jlzhjp.dev/anki-tts/openrouter"
	"jlzhjp.dev/anki-tts/tts"
	"jlzhjp.dev/anki-tts/tui"
	"jlzhjp.dev/anki-tts/workflow"
)

const configFileName = "config.toml"

type config struct {
	OpenRouter map[string]any `toml:"openrouter"`
	FFmpeg     *ffmpeg.Config `toml:"ffmpeg"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCommand(os.Stdin, os.Stdout, os.Stderr).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildWorkflow() (*workflow.Service, error) {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user config directory: %w", err)
	}
	cfg, err := loadConfig(filepath.Join(configHome, "anki-tts", configFileName))
	if err != nil {
		return nil, err
	}
	services, err := buildServices(cfg)
	if err != nil {
		return nil, err
	}
	transformer, err := buildTransformer(cfg)
	if err != nil {
		return nil, err
	}

	return workflow.New(anki.NewClient(), services, transformer), nil
}

func runTUI(ctx context.Context, appWorkflow *workflow.Service, options tui.Options, input io.Reader, output io.Writer) error {
	model := tui.NewWithOptions(ctx, appWorkflow, options)
	_, err := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output)).Run()
	if err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func buildTransformer(cfg config) (tts.Transformer, error) {
	if cfg.FFmpeg == nil {
		return nil, nil
	}
	return ffmpeg.New(*cfg.FFmpeg)
}

func loadConfig(path string) (config, error) {
	var cfg config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	return cfg, nil
}

func buildServices(cfg config) (*tts.Container, error) {
	container := tts.NewContainer()
	if cfg.OpenRouter == nil {
		return container, nil
	}
	service, err := openrouter.NewFactory().Create(cfg.OpenRouter)
	if err != nil {
		return nil, err
	}
	if err := container.Add("OpenRouter", service); err != nil {
		return nil, err
	}
	return container, nil
}
