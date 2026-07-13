package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"charm.land/bubbletea/v2"
	"github.com/BurntSushi/toml"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/openrouter"
	"jlzhjp.dev/anki-tts/tts"
	"jlzhjp.dev/anki-tts/tui"
)

const configFileName = "config.toml"

type config struct {
	OpenRouter map[string]any `toml:"openrouter"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve user config directory: %w", err)
	}
	cfg, err := loadConfig(filepath.Join(configHome, "anki-tts", configFileName))
	if err != nil {
		return err
	}
	services, err := buildServices(cfg)
	if err != nil {
		return err
	}

	model := tui.New(context.Background(), anki.NewClient(), services)
	_, err = tea.NewProgram(model).Run()
	if err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
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
