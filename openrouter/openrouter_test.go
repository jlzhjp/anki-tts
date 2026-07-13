package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"jlzhjp.dev/anki-tts/tts"
)

func TestFactoryCreateDefaultsAndEnvironmentKey(t *testing.T) {
	t.Setenv(apiKeyEnvironment, "environment-key")
	factory := NewFactory(WithHTTPClient(doerFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer environment-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var body speechRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := speechRequest{Model: "openai/tts", Input: "hello", Voice: defaultVoice, ResponseFormat: defaultFormat}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("request = %+v, want %+v", body, want)
		}
		return response(http.StatusOK, "audio/mpeg", []byte("audio")), nil
	})))

	service, err := factory.Create(map[string]any{"model": "openai/tts"})
	if err != nil {
		t.Fatal(err)
	}
	voice, err := service.Generate(context.Background(), tts.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	audio := readVoiceAudio(t, voice)
	if string(audio) != "audio" || voice.Audio.Format != "mp3" || voice.Audio.MediaType != "audio/mpeg" {
		t.Fatalf("voice = %+v", voice)
	}
}

func TestFactoryMapKeyTakesPrecedence(t *testing.T) {
	t.Setenv(apiKeyEnvironment, "environment-key")
	factory := NewFactory(WithHTTPClient(doerFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer map-key" {
			t.Fatalf("Authorization = %q", got)
		}
		return response(http.StatusOK, "", []byte("pcm")), nil
	})))

	service, err := factory.Create(map[string]any{
		"model":           "openai/tts",
		"api_key":         "map-key",
		"voice":           "nova",
		"response_format": "pcm",
	})
	if err != nil {
		t.Fatal(err)
	}
	voice, err := service.Generate(context.Background(), tts.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	_ = readVoiceAudio(t, voice)
	if voice.Audio.Format != "pcm" || voice.Audio.MediaType != "audio/pcm" {
		t.Fatalf("voice = %+v", voice)
	}
}

func TestFactoryConfigurationErrors(t *testing.T) {
	t.Setenv(apiKeyEnvironment, "")
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{name: "missing model", config: map[string]any{"api_key": "key"}, want: "model is required"},
		{name: "model type", config: map[string]any{"model": 42, "api_key": "key"}, want: "model must be a string"},
		{name: "missing key", config: map[string]any{"model": "model"}, want: "api_key is required"},
		{name: "key type", config: map[string]any{"model": "model", "api_key": true}, want: "api_key must be a string"},
		{name: "voice type", config: map[string]any{"model": "model", "api_key": "key", "voice": 1}, want: "voice must be a string"},
		{name: "format type", config: map[string]any{"model": "model", "api_key": "key", "response_format": 1}, want: "response_format must be a string"},
		{name: "unsupported format", config: map[string]any{"model": "model", "api_key": "key", "response_format": "wav"}, want: "response_format must be mp3 or pcm"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewFactory().Create(test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestGenerateRequestAndMetadata(t *testing.T) {
	const endpoint = "https://example.test/speech"
	factory := NewFactory(
		WithEndpoint(endpoint),
		WithHTTPClient(doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost || req.URL.String() != endpoint {
				t.Fatalf("request = %s %s", req.Method, req.URL)
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q", got)
			}
			resp := response(http.StatusOK, "audio/mpeg", []byte{1, 2, 3})
			resp.Header.Set("X-Generation-Id", "generation-123")
			return resp, nil
		})),
	)
	service, err := factory.Create(map[string]any{"model": "model", "api_key": "secret"})
	if err != nil {
		t.Fatal(err)
	}

	voice, err := service.Generate(context.Background(), tts.Input{Text: "speak this"})
	if err != nil {
		t.Fatal(err)
	}
	audio := readVoiceAudio(t, voice)
	if !bytes.Equal(audio, []byte{1, 2, 3}) || voice.GenerationID != "generation-123" {
		t.Fatalf("voice = %+v", voice)
	}
}

func TestGenerateLeavesSuccessfulResponseStreaming(t *testing.T) {
	body := &countingReadCloser{Reader: strings.NewReader("streamed audio")}
	service := mustService(t, doerFunc(func(*http.Request) (*http.Response, error) {
		resp := response(http.StatusOK, "audio/mpeg", nil)
		resp.Body = body
		resp.ContentLength = -1
		return resp, nil
	}))
	voice, err := service.Generate(context.Background(), tts.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if body.reads != 0 {
		t.Fatalf("response was read during Generate: %d reads", body.reads)
	}
	if got := string(readVoiceAudio(t, voice)); got != "streamed audio" {
		t.Fatalf("audio = %q", got)
	}
}

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

func TestGenerateErrors(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		service, err := NewFactory().Create(map[string]any{"model": "model", "api_key": "key"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Generate(context.Background(), tts.Input{Text: "  "})
		if err == nil || !strings.Contains(err.Error(), "input text is required") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("API error", func(t *testing.T) {
		service := mustService(t, doerFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, "application/json", []byte(`{"error":{"message":"invalid credentials for secret"}}`)), nil
		}))
		_, err := service.Generate(context.Background(), tts.Input{Text: "hello"})
		if err == nil || !strings.Contains(err.Error(), "401 Unauthorized: invalid credentials for [REDACTED]") {
			t.Fatalf("error = %v", err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error exposes API key: %v", err)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		transportErr := errors.New("offline")
		service := mustService(t, doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportErr
		}))
		_, err := service.Generate(context.Background(), tts.Input{Text: "hello"})
		if !errors.Is(err, transportErr) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		service := mustService(t, doerFunc(func(req *http.Request) (*http.Response, error) {
			return nil, req.Context().Err()
		}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := service.Generate(ctx, tts.Input{Text: "hello"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
}

func mustService(t *testing.T, client HTTPClient) tts.Service {
	t.Helper()
	service, err := NewFactory(WithHTTPClient(client)).Create(map[string]any{
		"model":   "model",
		"api_key": "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(status int, mediaType string, body []byte) *http.Response {
	header := make(http.Header)
	if mediaType != "" {
		header.Set("Content-Type", mediaType)
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func readVoiceAudio(t *testing.T, voice tts.Voice) []byte {
	t.Helper()
	data, err := io.ReadAll(voice.Audio.Data)
	if err != nil {
		t.Fatal(err)
	}
	if err := voice.Audio.Data.Close(); err != nil {
		t.Fatal(err)
	}
	return data
}

type countingReadCloser struct {
	io.Reader
	reads  int
	closed bool
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}

func (r *countingReadCloser) Close() error {
	r.closed = true
	return nil
}
