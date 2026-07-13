package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"jlzhjp.dev/anki-tts/openrouter"
	"jlzhjp.dev/anki-tts/tts"
)

const configFileName = "config.toml"

type config struct {
	OpenRouter map[string]any `toml:"openrouter"`
}

func main() {
	configHome, err := os.UserConfigDir()
	if err == nil {
		err = run(context.Background(), os.Args[1:], configHome, openrouter.NewFactory())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, configHome string, factory tts.ServiceFactory) error {
	if len(args) == 0 {
		return errors.New("usage: anki-tts <sentence>")
	}

	configPath := filepath.Join(configHome, "anki-tts", configFileName)
	var cfg config
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return fmt.Errorf("load config %q: %w", configPath, err)
	}
	if cfg.OpenRouter == nil {
		return fmt.Errorf("load config %q: [openrouter] table is required", configPath)
	}

	service, err := factory.Create(cfg.OpenRouter)
	if err != nil {
		return err
	}
	voice, err := service.Generate(ctx, tts.Input{Text: args[0]})
	if err != nil {
		return err
	}

	outputPath := "result." + voice.Format
	if err := os.WriteFile(outputPath, voice.Data, 0o644); err != nil {
		return fmt.Errorf("write generated voice to %q: %w", outputPath, err)
	}
	return nil
}
