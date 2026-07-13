package anki

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestListDecks(t *testing.T) {
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "deckNames" || got.Version != apiVersion {
			t.Fatalf("unexpected request: %+v", got)
		}
		return []string{"Default", "Japanese"}
	})

	got, err := client.ListDecks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Default", "Japanese"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListDecks() = %v, want %v", got, want)
	}
}

func TestListNotes(t *testing.T) {
	call := 0
	client := testClient(t, func(t *testing.T, got request) any {
		call++
		switch call {
		case 1:
			if got.Action != "findNotes" {
				t.Fatalf("action = %q, want findNotes", got.Action)
			}
			params := decodeParams[struct {
				Query string `json:"query"`
			}](t, got.Params)
			if params.Query != `deck:"Japanese \"Core\""` {
				t.Fatalf("query = %q", params.Query)
			}
			return []int64{42}
		case 2:
			if got.Action != "notesInfo" {
				t.Fatalf("action = %q, want notesInfo", got.Action)
			}
			return []Note{{ID: 42, ModelName: "Basic", Fields: map[string]Field{"Front": {Value: "猫", Order: 0}}}}
		default:
			t.Fatalf("unexpected call %d", call)
			return nil
		}
	})

	got, err := client.ListNotes(context.Background(), `Japanese "Core"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 42 || got[0].Fields["Front"].Value != "猫" {
		t.Fatalf("unexpected notes: %+v", got)
	}
}

func TestListNotesEmptySkipsNotesInfo(t *testing.T) {
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "findNotes" {
			t.Fatalf("action = %q, want findNotes", got.Action)
		}
		return []int64{}
	})

	got, err := client.ListNotes(context.Background(), "Empty")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("ListNotes() = %#v, want non-nil empty slice", got)
	}
}

func TestUpdateNotes(t *testing.T) {
	var ids []int64
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "updateNoteFields" {
			t.Fatalf("action = %q, want updateNoteFields", got.Action)
		}
		params := decodeParams[struct {
			Note struct {
				ID     int64             `json:"id"`
				Fields map[string]string `json:"fields"`
			} `json:"note"`
		}](t, got.Params)
		ids = append(ids, params.Note.ID)
		return nil
	})

	err := client.UpdateNotes(context.Background(), []NoteUpdate{
		{ID: 1, Fields: map[string]string{"Front": "one"}},
		{ID: 2, Fields: map[string]string{"Back": "two"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []int64{1, 2}) {
		t.Fatalf("updated IDs = %v", ids)
	}
}

func TestStoreMediaFile(t *testing.T) {
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "storeMediaFile" {
			t.Fatalf("action = %q, want storeMediaFile", got.Action)
		}
		params := decodeParams[struct {
			Filename string `json:"filename"`
			Data     string `json:"data"`
		}](t, got.Params)
		if params.Filename != "_anki-tts.mp3" || params.Data != "YXVkaW8=" {
			t.Fatalf("params = %+v", params)
		}
		return "_anki-tts.mp3"
	})

	filename, err := client.StoreMediaFile(context.Background(), "_anki-tts.mp3", []byte("audio"))
	if err != nil {
		t.Fatal(err)
	}
	if filename != "_anki-tts.mp3" {
		t.Fatalf("filename = %q", filename)
	}
}

func TestStoreMediaFileValidation(t *testing.T) {
	client := NewClient()
	if _, err := client.StoreMediaFile(context.Background(), "../audio.mp3", []byte("audio")); err == nil {
		t.Fatal("expected invalid filename error")
	}
	if _, err := client.StoreMediaFile(context.Background(), "audio.mp3", nil); err == nil {
		t.Fatal("expected empty data error")
	}
}

func TestAnkiConnectError(t *testing.T) {
	client := NewClient(WithHTTPClient(doerFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"result":null,"error":"collection unavailable"}`), nil
	})))
	_, err := client.ListDecks(context.Background())
	if err == nil || err.Error() != "list decks: collection unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func testClient(t *testing.T, handler func(*testing.T, request) any) *Client {
	t.Helper()
	httpClient := doerFunc(func(r *http.Request) (*http.Response, error) {
		var raw struct {
			Action  string          `json:"action"`
			Version int             `json:"version"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		got := request{Action: raw.Action, Version: raw.Version, Params: raw.Params}
		result := handler(t, got)
		var body bytes.Buffer
		if err := json.NewEncoder(&body).Encode(map[string]any{"result": result, "error": nil}); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(body.String()), nil
	})
	return NewClient(WithHTTPClient(httpClient))
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

func decodeParams[T any](t *testing.T, params any) T {
	t.Helper()
	raw, ok := params.(json.RawMessage)
	if !ok {
		t.Fatalf("params have type %T", params)
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
