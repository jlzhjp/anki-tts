package main

import (
	"os"
	"path/filepath"
	"testing"
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
