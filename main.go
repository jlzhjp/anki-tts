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
	OpenRouter *openRouterConfig `toml:"openrouter"`
	FFmpeg     *ffmpegConfig     `toml:"ffmpeg"`
	Anki       stageConfig       `toml:"anki"`
}

type stageConfig struct {
	Concurrency int                  `toml:"concurrency"`
	Retry       workflow.RetryConfig `toml:"retry"`
}

type openRouterConfig struct {
	Model          string               `toml:"model"`
	APIKey         string               `toml:"api_key"`
	Voice          string               `toml:"voice"`
	ResponseFormat string               `toml:"response_format"`
	Concurrency    int                  `toml:"concurrency"`
	Retry          workflow.RetryConfig `toml:"retry"`
}

type ffmpegConfig struct {
	Format      string               `toml:"format"`
	Args        []string             `toml:"args"`
	Concurrency int                  `toml:"concurrency"`
	Retry       workflow.RetryConfig `toml:"retry"`
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

	pipeline := pipelineConfig(cfg)
	return workflow.NewWithConfig(anki.NewClient(), services, transformer, pipeline)
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
	return ffmpeg.New(ffmpeg.Config{Format: cfg.FFmpeg.Format, Args: cfg.FFmpeg.Args})
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
	service, err := openrouter.NewFactory().Create(openrouter.Config{
		Model: cfg.OpenRouter.Model, APIKey: cfg.OpenRouter.APIKey, Voice: cfg.OpenRouter.Voice,
		ResponseFormat: cfg.OpenRouter.ResponseFormat,
	})
	if err != nil {
		return nil, err
	}
	if err := container.Add("OpenRouter", service); err != nil {
		return nil, err
	}
	return container, nil
}

func pipelineConfig(cfg config) workflow.PipelineConfig {
	defaults := workflow.DefaultPipelineConfig()
	if cfg.OpenRouter != nil {
		defaults.Synthesis = mergeStageConfig(defaults.Synthesis, stageConfig{
			Concurrency: cfg.OpenRouter.Concurrency, Retry: cfg.OpenRouter.Retry,
		})
	}
	if cfg.FFmpeg != nil {
		defaults.Audio = mergeStageConfig(defaults.Audio, stageConfig{
			Concurrency: cfg.FFmpeg.Concurrency, Retry: cfg.FFmpeg.Retry,
		})
	}
	defaults.Persistence = mergeStageConfig(defaults.Persistence, cfg.Anki)
	return defaults
}

func mergeStageConfig(defaults workflow.StageConfig, configured stageConfig) workflow.StageConfig {
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
