package step

import (
	"context"
	"strings"
	"testing"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

func TestChooseDestinationFieldCreatesThenResumesScreen(t *testing.T) {
	client := &fakeClient{value: "Audio"}
	note := testNote()

	value, screen, err := ChooseDestinationField(
		context.Background(),
		client,
		note,
		nil,
		Display{Step: 4},
	)
	if err != nil {
		t.Fatal(err)
	}
	if value != "Audio" || screen == nil || client.displays[0].Resume {
		t.Fatalf("value=%q screen=%T display=%+v", value, screen, client.displays[0])
	}

	_, resumed, err := ChooseDestinationField(
		context.Background(),
		client,
		note,
		screen,
		Display{Step: 4},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed != screen || !client.displays[1].Resume {
		t.Fatalf("screen was not resumed: display=%+v", client.displays[1])
	}
}

func TestChooseSourceFieldRejectsNoteWithoutText(t *testing.T) {
	_, _, err := ChooseSourceField(
		context.Background(),
		&fakeClient{},
		anki.Note{Fields: map[string]anki.Field{"Front": {Value: " "}}},
		nil,
		Display{},
	)
	if err == nil || !strings.Contains(err.Error(), "no non-empty source fields") {
		t.Fatalf("error=%v", err)
	}
}

func TestChooseNoteMarksInvalidConfiguredFields(t *testing.T) {
	items := noteListItems([]anki.Note{testNote()}, NoteOptions{
		SourceField:      "Missing",
		DestinationField: "AlsoMissing",
	})
	candidate := items[0].(listItem).value.(noteCandidate)
	if !strings.Contains(candidate.invalid, `missing source field "Missing"`) {
		t.Fatalf("invalid=%q", candidate.invalid)
	}
}

func TestGenerateNoteAudioRunsApplication(t *testing.T) {
	app := &fakeGenerationApplication{
		result: ankitts.GenerateResult{Filename: "voice.mp3"},
	}
	request := ankitts.GenerationRequest{
		Notes:            []anki.Note{testNote()},
		SourceField:      "Front",
		DestinationField: "Audio",
		Service:          "openrouter",
	}
	screen := newNoteAudioGenerationScreen(context.Background(), app, request)
	message := screen.generate()()
	result := message.(generationFinishedMsg)
	if result.err != nil || result.result.Filename != "voice.mp3" {
		t.Fatalf("result=%+v", result)
	}
	if app.request.SourceField != "Front" || app.request.DestinationField != "Audio" {
		t.Fatalf("request=%+v", app.request)
	}
}

func TestPromptReportsUnexpectedResultType(t *testing.T) {
	_, err := prompt[string](
		context.Background(),
		&fakeClient{value: true},
		&DestinationOverwriteScreen{},
		Display{},
	)
	if err == nil || !strings.Contains(err.Error(), "screen returned bool") {
		t.Fatalf("error=%v", err)
	}
}

type fakeClient struct {
	value    any
	err      error
	displays []Display
}

func (c *fakeClient) Prompt(_ context.Context, _ Screen, display Display) (any, error) {
	c.displays = append(c.displays, display)
	return c.value, c.err
}

type fakeGenerationApplication struct {
	request ankitts.GenerationRequest
	result  ankitts.GenerateResult
	err     error
}

func (a *fakeGenerationApplication) HasAudioProcessors() bool { return false }
func (a *fakeGenerationApplication) Prepare(request ankitts.GenerationRequest) (ankitts.Plan, error) {
	a.request = request
	return ankitts.Plan{}, nil
}
func (a *fakeGenerationApplication) Execute(
	context.Context,
	ankitts.Plan,
	ankitts.ExecuteOptions,
) (ankitts.BatchResult, error) {
	if a.err != nil {
		return ankitts.BatchResult{}, a.err
	}
	return ankitts.BatchResult{
		Items: []ankitts.ItemResult{{Result: a.result}},
	}, nil
}

func testNote() anki.Note {
	return anki.Note{ID: 42, ModelName: "Basic", Fields: map[string]anki.Field{
		"Front": {Value: "Hello", Order: 0},
		"Audio": {Value: "", Order: 1},
	}}
}
