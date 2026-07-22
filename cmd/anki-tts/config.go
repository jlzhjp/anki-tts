package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/ffmpeg"
	"jlzhjp.dev/anki-tts/openrouter"
	"jlzhjp.dev/anki-tts/pipeline"
)

const configFileName = "config.toml"

type config struct {
	OpenRouter *openRouterConfig `toml:"openrouter"`
	FFmpeg     *ffmpegConfig     `toml:"ffmpeg"`
	Anki       stageConfig       `toml:"anki"`
}

type stageConfig struct {
	Concurrency int                  `toml:"concurrency"`
	Retry       pipeline.RetryConfig `toml:"retry"`
}

type openRouterConfig struct {
	Model          string               `toml:"model"`
	APIKey         string               `toml:"api_key"`
	Voice          string               `toml:"voice"`
	ResponseFormat string               `toml:"response_format"`
	Concurrency    int                  `toml:"concurrency"`
	Retry          pipeline.RetryConfig `toml:"retry"`
}

type ffmpegConfig struct {
	Format      ffmpeg.Format        `toml:"format"`
	Args        []string             `toml:"args"`
	Concurrency int                  `toml:"concurrency"`
	Retry       pipeline.RetryConfig `toml:"retry"`
}

func buildApplication() (*ankitts.Application, error) {
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
	processors, err := buildAudioProcessors(cfg)
	if err != nil {
		return nil, err
	}
	return ankitts.New(anki.NewClient(), services, processors, pipelineConfig(cfg))
}

func loadConfig(path string) (config, error) {
	var cfg config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	return cfg, nil
}

func buildServices(cfg config) (*ankitts.ServiceContainer, error) {
	container := ankitts.NewServiceContainer()
	if cfg.OpenRouter == nil {
		return container, nil
	}
	service, err := openrouter.NewFactory().Create(openrouter.Config{
		Model: cfg.OpenRouter.Model, APIKey: cfg.OpenRouter.APIKey, Voice: cfg.OpenRouter.Voice,
		ResponseFormat: cfg.OpenRouter.ResponseFormat,
	})
	if err != nil {
		return nil, err
	}
	if err := container.Add("openrouter", service); err != nil {
		return nil, err
	}
	return container, nil
}

func buildAudioProcessors(cfg config) ([]ankitts.AudioProcessor, error) {
	if cfg.FFmpeg == nil {
		return nil, nil
	}
	transformer, err := ffmpeg.New(ffmpeg.Config{Format: cfg.FFmpeg.Format, Args: cfg.FFmpeg.Args})
	if err != nil {
		return nil, err
	}
	return []ankitts.AudioProcessor{{Name: "ffmpeg", Transformer: transformer}}, nil
}

func pipelineConfig(cfg config) pipeline.Config {
	configured := pipeline.Config{
		"anki": mergeStageConfig(pipeline.DefaultStageConfig(4), cfg.Anki),
	}
	if cfg.OpenRouter != nil {
		configured["openrouter"] = mergeStageConfig(pipeline.DefaultStageConfig(4), stageConfig{
			Concurrency: cfg.OpenRouter.Concurrency, Retry: cfg.OpenRouter.Retry,
		})
	}
	if cfg.FFmpeg != nil {
		configured["ffmpeg"] = mergeStageConfig(pipeline.DefaultStageConfig(2), stageConfig{
			Concurrency: cfg.FFmpeg.Concurrency, Retry: cfg.FFmpeg.Retry,
		})
	}
	return configured
}

func mergeStageConfig(defaults pipeline.StageConfig, configured stageConfig) pipeline.StageConfig {
	if configured.Concurrency != 0 {
		defaults.Concurrency = configured.Concurrency
	}
	if configured.Retry.MaxAttempts != 0 {
		defaults.Retry.MaxAttempts = configured.Retry.MaxAttempts
	}
	if configured.Retry.InitialBackoff != 0 {
		defaults.Retry.InitialBackoff = configured.Retry.InitialBackoff
	}
	if configured.Retry.MaxBackoff != 0 {
		defaults.Retry.MaxBackoff = configured.Retry.MaxBackoff
	}
	return defaults
}
