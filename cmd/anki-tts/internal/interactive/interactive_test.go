package interactive

import (
	"context"
	"strings"
	"testing"

	"jlzhjp.dev/ankitts"
	"jlzhjp.dev/ankitts/anki"
)

func TestChooseDestinationFieldCreatesThenResumesScreen(t *testing.T) {
	t.Parallel()
	client := &fakeClient{value: "Audio"}
	note := testNote()

	value, screen, err := chooseDestinationField(
		t.Context(),
		client,
		&note,
		nil,
		display{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if value != "Audio" || screen == nil || client.displays[0].Resume {
		t.Fatalf("value=%q screen=%T display=%+v", value, screen, client.displays[0])
	}

	_, resumed, err := chooseDestinationField(
		t.Context(),
		client,
		&note,
		screen,
		display{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed != screen || !client.displays[1].Resume {
		t.Fatalf("screen was not resumed: display=%+v", client.displays[1])
	}
}

func TestChooseSourceFieldRejectsNoteWithoutText(t *testing.T) {
	t.Parallel()
	_, _, err := chooseSourceField(
		t.Context(),
		&fakeClient{},
		&anki.Note{Fields: map[string]anki.Field{"Front": {Value: " "}}},
		nil,
		display{},
	)
	if err == nil || !strings.Contains(err.Error(), "no non-empty source fields") {
		t.Fatalf("error=%v", err)
	}
}

func TestChooseNoteMarksInvalidConfiguredFields(t *testing.T) {
	t.Parallel()
	items := noteListItems([]anki.Note{testNote()}, noteOptions{
		SourceField:      "Missing",
		DestinationField: "AlsoMissing",
	})
	item, ok := items[0].(listItem)
	if !ok {
		t.Fatalf("item=%T", items[0])
	}
	candidate, ok := item.value.(noteCandidate)
	if !ok {
		t.Fatalf("value=%T", item.value)
	}
	if !strings.Contains(candidate.invalid, `missing source field "Missing"`) {
		t.Fatalf("invalid=%q", candidate.invalid)
	}
}

func TestGenerateNoteAudioRunsApplication(t *testing.T) {
	t.Parallel()
	app := &fakeGenerationApplication{
		result: ankitts.GenerateResult{Filename: "voice.mp3"},
	}
	request := ankitts.GenerationRequest{
		Notes:            ankitts.NoteResults(testNote()),
		SourceField:      "Front",
		DestinationField: "Audio",
		Service:          "openrouter",
	}
	screen := newNoteAudioGenerationScreen(t.Context(), app, request)
	message := screen.generate()()
	result := requireMessage[generationFinishedMsg](t, message)
	if result.err != nil || result.result.Filename != "voice.mp3" {
		t.Fatalf("result=%+v", result)
	}
	if app.request.SourceField != "Front" || app.request.DestinationField != "Audio" {
		t.Fatalf("request=%+v", app.request)
	}
}

func requireMessage[T any](t *testing.T, message any) T {
	t.Helper()
	value, ok := message.(T)
	if !ok {
		t.Fatalf("message=%T", message)
	}
	return value
}

func TestPromptReportsUnexpectedResultType(t *testing.T) {
	t.Parallel()
	_, err := prompt[string](
		t.Context(),
		&fakeClient{value: true},
		&destinationOverwriteScreen{},
		display{},
	)
	if err == nil || !strings.Contains(err.Error(), "screen returned bool") {
		t.Fatalf("error=%v", err)
	}
}

type fakeClient struct {
	value    any
	err      error
	displays []display
}

func (c *fakeClient) Prompt(_ context.Context, _ screen, display display) (any, error) {
	c.displays = append(c.displays, display)
	return c.value, c.err
}

type fakeGenerationApplication struct {
	result  ankitts.GenerateResult
	err     error
	request ankitts.GenerationRequest
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
