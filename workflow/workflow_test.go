package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/tts"
)

func TestGenerateStoresAndUpdates(t *testing.T) {
	client := &fakeAnki{}
	provider := &fakeTTS{voice: voice("audio bytes", "mp3")}
	service := New(client, container(t, provider), nil)
	result, err := executeOne(context.Background(), service, spec(provider))
	if err != nil {
		t.Fatal(err)
	}
	if result.Cost == nil || *result.Cost != 0.00125 {
		t.Fatalf("result=%+v", result)
	}
	if provider.input.Text != "Hello world" {
		t.Fatalf("input=%q", provider.input.Text)
	}
	hash := sha256.Sum256([]byte("audio bytes"))
	wantFilename := fmt.Sprintf("anki-tts-42-%x.mp3", hash[:6])
	if client.mediaFilename != wantFilename {
		t.Fatalf("filename=%q want=%q", client.mediaFilename, wantFilename)
	}
	wantField := "[sound:" + wantFilename + "]"
	if client.update.Fields["Audio"] != wantField {
		t.Fatalf("Audio=%q want=%q", client.update.Fields["Audio"], wantField)
	}
}

func TestTransformationDeterminesUploadedMedia(t *testing.T) {
	client := &fakeAnki{}
	provider := &fakeTTS{voice: voice("provider audio", "wav")}
	transformer := &fakeTransformer{output: "transformed audio", format: "mp3"}
	service := New(client, container(t, provider), transformer)
	_, err := executeOne(context.Background(), service, spec(provider))
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte("transformed audio"))
	wantFilename := fmt.Sprintf("anki-tts-42-%x.mp3", wantHash[:6])
	if client.mediaFilename != wantFilename || string(client.mediaData) != "transformed audio" {
		t.Fatalf("filename=%q data=%q", client.mediaFilename, client.mediaData)
	}
}

func TestFailuresBeforeUploadLeaveAnkiUnchanged(t *testing.T) {
	tests := []struct {
		name        string
		provider    *fakeTTS
		transformer tts.Transformer
		want        string
	}{
		{name: "empty source", provider: &fakeTTS{voice: voice("audio", "mp3")}, want: "no speakable text"},
		{name: "nil voice", provider: &fakeTTS{}, want: "no voice"},
		{name: "transform failure", provider: &fakeTTS{voice: voice("audio", "wav")}, transformer: &fakeTransformer{err: errors.New("FFmpeg failed")}, want: "FFmpeg failed"},
		{name: "stream failure", provider: &fakeTTS{voice: voice("audio", "wav")}, transformer: &fakeTransformer{streamErr: errors.New("stream failed"), format: "mp3"}, want: "stream failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeAnki{}
			config := DefaultPipelineConfig()
			config.Synthesis.Retry.MaxAttempts = 1
			config.Audio.Retry.MaxAttempts = 1
			service, configErr := NewWithConfig(client, container(t, test.provider), test.transformer, config)
			if configErr != nil {
				t.Fatal(configErr)
			}
			req := spec(test.provider)
			if test.name == "empty source" {
				field := req.Notes[0].Fields["Front"]
				field.Value = "<br>"
				req.Notes[0].Fields["Front"] = field
			}
			_, err := executeOne(context.Background(), service, req)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want containing %q", err, test.want)
			}
			if client.storeCalls != 0 || client.updateCalls != 0 {
				t.Fatalf("Anki calls store=%d update=%d", client.storeCalls, client.updateCalls)
			}
		})
	}
}

func TestCostFailureIsNonFatal(t *testing.T) {
	client := &fakeAnki{}
	provider := &fakeTTS{voice: &fakeVoice{ReadCloser: io.NopCloser(strings.NewReader("audio")), format: "mp3", costErr: errors.New("cost unavailable")}}
	service := New(client, container(t, provider), nil)
	result, err := executeOne(context.Background(), service, spec(provider))
	if err != nil {
		t.Fatal(err)
	}
	if result.Cost != nil || result.CostErr == nil || client.updateCalls != 1 {
		t.Fatalf("result=%+v updates=%d", result, client.updateCalls)
	}
}

func TestFinalAudioValidationAndClosure(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		format string
		want   string
	}{
		{name: "empty", format: "mp3", want: "empty data"},
		{name: "invalid format", data: []byte("audio"), format: "../mp3", want: "invalid format"},
		{name: "oversized", data: make([]byte, maxFinalAudioSize+1), format: "mp3", want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeAnki{}
			voice := &fakeVoice{ReadCloser: io.NopCloser(bytes.NewReader(test.data)), format: test.format}
			provider := &fakeTTS{voice: voice}
			config := DefaultPipelineConfig()
			config.Synthesis.Retry.MaxAttempts = 1
			service, configErr := NewWithConfig(client, container(t, provider), nil, config)
			if configErr != nil {
				t.Fatal(configErr)
			}
			_, err := executeOne(context.Background(), service, spec(provider))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want containing %q", err, test.want)
			}
			if voice.closeCalls != 1 {
				t.Fatalf("close calls=%d", voice.closeCalls)
			}
			if client.storeCalls != 0 {
				t.Fatalf("store calls=%d", client.storeCalls)
			}
		})
	}
}

func TestMissingTTSServiceIsRejected(t *testing.T) {
	service := New(&fakeAnki{}, tts.NewContainer(), nil)
	_, err := service.Plan(GenerationSpec{})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error=%v", err)
	}
}

func TestNoteUpdateFailureReportsStoredMedia(t *testing.T) {
	client := &fakeAnki{updateErr: errors.New("update failed")}
	provider := &fakeTTS{voice: voice("audio", "mp3")}
	config := DefaultPipelineConfig()
	config.Persistence.Retry.MaxAttempts = 1
	service, configErr := NewWithConfig(client, container(t, provider), nil, config)
	if configErr != nil {
		t.Fatal(configErr)
	}
	_, err := executeOne(context.Background(), service, spec(provider))
	if err == nil || !strings.Contains(err.Error(), "was stored") || client.storeCalls != 1 {
		t.Fatalf("error=%v stores=%d", err, client.storeCalls)
	}
}

func container(t *testing.T, service tts.Service) *tts.Container {
	t.Helper()
	services := tts.NewContainer()
	if err := services.Add("OpenRouter", service); err != nil {
		t.Fatal(err)
	}
	return services
}

func spec(service tts.Service) GenerationSpec {
	return GenerationSpec{
		Notes: []anki.Note{{ID: 42, Fields: map[string]anki.Field{
			"Front": {Value: `<b>Hello</b>&nbsp;world`},
			"Audio": {Value: "existing"},
		}}},
		SourceField: "Front", DestinationField: "Audio",
		Service: tts.NamedService{Name: "OpenRouter", Service: service},
	}
}

func executeOne(ctx context.Context, service *Service, request GenerationSpec) (GenerateResult, error) {
	plan, err := service.Plan(request)
	if err != nil {
		return GenerateResult{}, err
	}
	batch, err := service.Execute(ctx, plan, ExecuteOptions{})
	if err != nil {
		return GenerateResult{}, err
	}
	if len(batch.Items) != 1 {
		return GenerateResult{}, fmt.Errorf("got %d results", len(batch.Items))
	}
	return batch.Items[0].Result, batch.Items[0].Err
}

type fakeAnki struct {
	mediaFilename string
	mediaData     []byte
	update        anki.NoteUpdate
	storeCalls    int
	updateCalls   int
	updateErr     error
}

func (*fakeAnki) ListDecks(context.Context) ([]string, error)                  { return nil, nil }
func (*fakeAnki) ListNotes(context.Context, string) ([]anki.Note, error)       { return nil, nil }
func (*fakeAnki) ListNoteTemplates(context.Context) ([]string, error)          { return nil, nil }
func (*fakeAnki) ListTemplateFields(context.Context, string) ([]string, error) { return nil, nil }
func (f *fakeAnki) StoreMediaFile(_ context.Context, filename string, data []byte) (string, error) {
	f.storeCalls++
	f.mediaFilename = filename
	f.mediaData = append([]byte(nil), data...)
	return filename, nil
}
func (f *fakeAnki) UpdateNote(_ context.Context, update anki.NoteUpdate) error {
	f.updateCalls++
	f.update = update
	return f.updateErr
}

type fakeTTS struct {
	input tts.Input
	voice tts.Voice
}

func (f *fakeTTS) Generate(_ context.Context, input tts.Input) (tts.Voice, error) {
	f.input = input
	return f.voice, nil
}

type fakeTransformer struct {
	output    string
	format    string
	err       error
	streamErr error
}

func (f *fakeTransformer) Transform(_ context.Context, input tts.Voice) (tts.Voice, error) {
	_, _ = io.ReadAll(input)
	if f.err != nil {
		_ = input.Close()
		return nil, f.err
	}
	var output io.ReadCloser = io.NopCloser(bytes.NewBufferString(f.output))
	if f.streamErr != nil {
		output = io.NopCloser(errorReader{err: f.streamErr})
	}
	return &fakeVoice{ReadCloser: output, format: f.format, source: input}, nil
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func voice(data, format string) tts.Voice {
	return &fakeVoice{ReadCloser: io.NopCloser(bytes.NewBufferString(data)), format: format, cost: 0.00125}
}

type fakeVoice struct {
	io.ReadCloser
	format     string
	cost       float64
	costErr    error
	source     tts.Voice
	closeCalls int
}

func (v *fakeVoice) Format() string    { return v.format }
func (v *fakeVoice) MediaType() string { return "audio/" + v.format }
func (v *fakeVoice) LoadCost(context.Context) (float64, error) {
	if v.source != nil {
		return v.source.LoadCost(context.Background())
	}
	return v.cost, v.costErr
}
func (v *fakeVoice) Close() error {
	v.closeCalls++
	err := v.ReadCloser.Close()
	if v.source != nil {
		_ = v.source.Close()
	}
	return err
}
