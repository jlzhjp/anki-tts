package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jlzhjp.dev/anki-tts/tts"
)

func TestRun(t *testing.T) {
	configHome := t.TempDir()
	configDir := filepath.Join(configHome, "anki-tts")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configData := []byte(`[openrouter]
model = "openai/tts"
voice = "nova"
response_format = "mp3"
`)
	if err := os.WriteFile(filepath.Join(configDir, configFileName), configData, 0o600); err != nil {
		t.Fatal(err)
	}

	workingDir := t.TempDir()
	t.Chdir(workingDir)
	factory := &fakeFactory{service: &fakeService{voice: tts.Voice{
		Data:   []byte("generated audio"),
		Format: "mp3",
	}}}

	if err := run(context.Background(), []string{"hello world", "ignored"}, configHome, factory); err != nil {
		t.Fatal(err)
	}
	wantConfig := map[string]any{
		"model":           "openai/tts",
		"voice":           "nova",
		"response_format": "mp3",
	}
	if !reflect.DeepEqual(factory.config, wantConfig) {
		t.Fatalf("factory config = %#v, want %#v", factory.config, wantConfig)
	}
	if factory.service.(*fakeService).input.Text != "hello world" {
		t.Fatalf("input = %q", factory.service.(*fakeService).input.Text)
	}
	got, err := os.ReadFile(filepath.Join(workingDir, "result.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "generated audio" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunRequiresSentence(t *testing.T) {
	err := run(context.Background(), nil, t.TempDir(), &fakeFactory{})
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRequiresOpenRouterTable(t *testing.T) {
	configHome := t.TempDir()
	configDir := filepath.Join(configHome, "anki-tts")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, configFileName), []byte("title = \"test\""), 0o600); err != nil {
		t.Fatal(err)
	}

	err := run(context.Background(), []string{"hello"}, configHome, &fakeFactory{})
	if err == nil || !strings.Contains(err.Error(), "[openrouter] table is required") {
		t.Fatalf("error = %v", err)
	}
}

type fakeFactory struct {
	config  map[string]any
	service tts.Service
	err     error
}

func (f *fakeFactory) Create(config map[string]any) (tts.Service, error) {
	f.config = config
	if f.err != nil {
		return nil, f.err
	}
	if f.service == nil {
		return nil, errors.New("fake service is not configured")
	}
	return f.service, nil
}

type fakeService struct {
	input tts.Input
	voice tts.Voice
	err   error
}

func (s *fakeService) Generate(_ context.Context, input tts.Input) (tts.Voice, error) {
	s.input = input
	return s.voice, s.err
}
