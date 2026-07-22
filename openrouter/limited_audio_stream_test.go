package openrouter

import (
	"io"
	"strings"
	"testing"
)

func TestGeneratedAudioStreamEnforcesSizeLimit(t *testing.T) {
	stream := &limitedAudioStream{
		body:      io.NopCloser(strings.NewReader("12345")),
		remaining: 4,
	}
	_, err := io.ReadAll(stream)
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v", err)
	}
}
