package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/anki"
	"jlzhjp.dev/ankitts/ffmpeg"
	"jlzhjp.dev/ankitts/openrouter"
	"jlzhjp.dev/ankitts/pipeline"
)

const configFileName = "config.toml"

type config struct {
	OpenRouter *openRouterConfig `toml:"openrouter"`
	FFmpeg     *ffmpegConfig     `toml:"ffmpeg"`
	Anki       stageOverrides    `toml:"anki"`
}

// stageOverrides contains optional configuration-file values. Zero values mean
// that the corresponding runtime default remains unchanged.
type stageOverrides struct {
	Concurrency int            `toml:"concurrency"`
	Retry       retryOverrides `toml:"retry"`
}

// retryOverrides is the TOML representation of pipeline.RetryConfig.
type retryOverrides struct {
	MaxAttempts    int           `toml:"max_attempts"`
	InitialBackoff time.Duration `toml:"initial_backoff"`
	MaxBackoff     time.Duration `toml:"max_backoff"`
}

type openRouterConfig struct {
	Model          string         `toml:"model"`
	APIKey         string         `toml:"api_key"`
	Voice          string         `toml:"voice"`
	ResponseFormat string         `toml:"response_format"`
	Concurrency    int            `toml:"concurrency"`
	Retry          retryOverrides `toml:"retry"`
}

type ffmpegConfig struct {
	Format      ffmpeg.Format  `toml:"format"`
	Args        []string       `toml:"args"`
	Concurrency int            `toml:"concurrency"`
	Retry       retryOverrides `toml:"retry"`
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
		"anki": cfg.Anki.apply(pipeline.DefaultStageConfig(4)),
	}
	if cfg.OpenRouter != nil {
		configured["openrouter"] = (stageOverrides{
			Concurrency: cfg.OpenRouter.Concurrency, Retry: cfg.OpenRouter.Retry,
		}).apply(pipeline.DefaultStageConfig(4))
	}
	if cfg.FFmpeg != nil {
		configured["ffmpeg"] = (stageOverrides{
			Concurrency: cfg.FFmpeg.Concurrency, Retry: cfg.FFmpeg.Retry,
		}).apply(pipeline.DefaultStageConfig(2))
	}
	return configured
}

func (overrides stageOverrides) apply(config pipeline.StageConfig) pipeline.StageConfig {
	if overrides.Concurrency != 0 {
		config.Concurrency = overrides.Concurrency
	}
	if overrides.Retry.MaxAttempts != 0 {
		config.Retry.MaxAttempts = overrides.Retry.MaxAttempts
	}
	if overrides.Retry.InitialBackoff != 0 {
		config.Retry.InitialBackoff = overrides.Retry.InitialBackoff
	}
	if overrides.Retry.MaxBackoff != 0 {
		config.Retry.MaxBackoff = overrides.Retry.MaxBackoff
	}
	return config
}
