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

	service, err := factory.Create(Config{Model: "openai/tts"})
	if err != nil {
		t.Fatal(err)
	}
	voice, err := service.Generate(context.Background(), tts.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	audio := readVoiceAudio(t, voice)
	if string(audio) != "audio" || voice.Format() != "mp3" || voice.MediaType() != "audio/mpeg" {
		t.Fatalf("voice = %+v", voice)
	}
}

func TestFactoryConfigKeyTakesPrecedence(t *testing.T) {
	t.Setenv(apiKeyEnvironment, "environment-key")
	factory := NewFactory(WithHTTPClient(doerFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Authorization"); got != "Bearer map-key" {
			t.Fatalf("Authorization = %q", got)
		}
		return response(http.StatusOK, "", []byte("pcm")), nil
	})))

	service, err := factory.Create(Config{
		Model: "openai/tts", APIKey: "map-key", Voice: "nova", ResponseFormat: "pcm",
	})
	if err != nil {
		t.Fatal(err)
	}
	voice, err := service.Generate(context.Background(), tts.Input{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	_ = readVoiceAudio(t, voice)
	if voice.Format() != "pcm" || voice.MediaType() != "audio/pcm" {
		t.Fatalf("voice = %+v", voice)
	}
}

func TestFactoryConfigurationErrors(t *testing.T) {
	t.Setenv(apiKeyEnvironment, "")
	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{name: "missing model", config: Config{APIKey: "key"}, want: "model is required"},
		{name: "missing key", config: Config{Model: "model"}, want: "api_key is required"},
		{name: "unsupported format", config: Config{Model: "model", APIKey: "key", ResponseFormat: "wav"}, want: "response_format must be mp3 or pcm"},
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

func TestGenerateRequestAndPricing(t *testing.T) {
	const endpoint = "https://example.test/speech"
	const modelsEndpoint = "https://example.test/models"
	modelsCalls := 0
	factory := NewFactory(
		WithEndpoint(endpoint),
		WithModelsEndpoint(modelsEndpoint),
		WithHTTPClient(doerFunc(func(req *http.Request) (*http.Response, error) {
			switch req.Method {
			case http.MethodPost:
				if req.URL.String() != endpoint {
					t.Fatalf("request URL = %s", req.URL)
				}
				if got := req.Header.Get("Content-Type"); got != "application/json" {
					t.Fatalf("Content-Type = %q", got)
				}
				resp := response(http.StatusOK, "audio/mpeg", []byte{1, 2, 3})
				return resp, nil
			case http.MethodGet:
				modelsCalls++
				if req.URL.Path != "/models" || req.URL.Query().Get("output_modalities") != "speech" {
					t.Fatalf("pricing request URL = %s", req.URL)
				}
				if got := req.Header.Get("Authorization"); got != "Bearer secret" {
					t.Fatalf("Authorization = %q", got)
				}
				return response(http.StatusOK, "application/json", []byte(`{"data":[{"id":"model","pricing":{"prompt":"0.000625","completion":"0"}}]}`)), nil
			default:
				t.Fatalf("method = %s", req.Method)
				return nil, nil
			}
		})),
	)
	service, err := factory.Create(Config{Model: "model", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	voice, err := service.Generate(context.Background(), tts.Input{Text: "猫a"})
	if err != nil {
		t.Fatal(err)
	}
	audio := readVoiceAudio(t, voice)
	if !bytes.Equal(audio, []byte{1, 2, 3}) {
		t.Fatalf("voice = %+v", voice)
	}
	cost, err := voice.LoadCost(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0.00125 {
		t.Fatalf("cost = %v", cost)
	}
	if _, err := voice.LoadCost(context.Background()); err != nil {
		t.Fatal(err)
	}
	if modelsCalls != 1 {
		t.Fatalf("models API calls = %d, want 1", modelsCalls)
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
		service, err := NewFactory().Create(Config{Model: "model", APIKey: "key"})
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
	service, err := NewFactory(WithHTTPClient(client)).Create(Config{Model: "model", APIKey: "secret"})
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
	data, err := io.ReadAll(voice)
	if err != nil {
		t.Fatal(err)
	}
	if err := voice.Close(); err != nil {
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
