package anki

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"testing"
)

func TestListDecks(t *testing.T) {
	t.Parallel()
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "deckNames" || got.Version != apiVersion {
			t.Fatalf("unexpected request: %+v", got)
		}
		return []string{"Default", "Japanese"}
	})

	got, err := client.ListDecks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Default", "Japanese"}
	if !slices.Equal(got, want) {
		t.Fatalf("ListDecks() = %v, want %v", got, want)
	}
}

func TestListNoteTemplateMetadata(t *testing.T) {
	t.Parallel()
	client := NewClient(WithHTTPClient(doerFunc(func(req *http.Request) (*http.Response, error) {
		var got request
		if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		switch got.Action {
		case "modelNames":
			return jsonResponse(`{"result":["Basic"],"error":null}`), nil
		case "modelFieldNames":
			return jsonResponse(`{"result":["Front","Back"],"error":null}`), nil
		default:
			t.Fatalf("unexpected action %q", got.Action)
			return nil, errors.New("unexpected action")
		}
	})))
	templates, err := client.ListNoteTemplates(t.Context())
	if err != nil || !slices.Equal(templates, []string{"Basic"}) {
		t.Fatalf("templates=%v error=%v", templates, err)
	}
	fields, err := client.ListTemplateFields(t.Context(), "Basic")
	if err != nil || !slices.Equal(fields, []string{"Front", "Back"}) {
		t.Fatalf("fields=%v error=%v", fields, err)
	}
}

func TestFindNoteIDs(t *testing.T) {
	t.Parallel()
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "findNotes" {
			t.Fatalf("action = %q, want findNotes", got.Action)
		}
		params := decodeParams[struct {
			Query string `json:"query"`
		}](t, got.Params)
		if params.Query != `deck:"Japanese Core" Front:re:^猫$` {
			t.Fatalf("query = %q", params.Query)
		}
		return []int64{42}
	})

	got, err := client.FindNoteIDs(t.Context(), `deck:"Japanese Core" Front:re:^猫$`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []int64{42}) {
		t.Fatalf("unexpected IDs: %+v", got)
	}
}

func TestNotesInfo(t *testing.T) {
	t.Parallel()
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "notesInfo" {
			t.Fatalf("action = %q, want notesInfo", got.Action)
		}
		params := decodeParams[struct {
			Notes []int64 `json:"notes"`
		}](t, got.Params)
		if !slices.Equal(params.Notes, []int64{42}) {
			t.Fatalf("notes = %v", params.Notes)
		}
		return []Note{{ID: 42, ModelName: "Basic", Fields: map[string]Field{"Front": {Value: "猫", Order: 0}}}}
	})

	got, err := client.NotesInfo(t.Context(), []int64{42})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 42 || got[0].Fields["Front"].Value != "猫" {
		t.Fatalf("unexpected notes: %+v", got)
	}
}

func TestNotesInfoEmptySkipsRequest(t *testing.T) {
	t.Parallel()
	client := testClient(t, func(t *testing.T, got request) any {
		t.Fatalf("unexpected action %q", got.Action)
		return nil
	})
	got, err := client.NotesInfo(t.Context(), nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("NotesInfo() = %#v, %v", got, err)
	}
}

func TestUpdateNotes(t *testing.T) {
	t.Parallel()
	var ids []int64
	client := testClient(t, func(t *testing.T, got request) any {
		if got.Action != "updateNoteFields" {
			t.Fatalf("action = %q, want updateNoteFields", got.Action)
		}
		params := decodeParams[struct {
			Note struct {
				Fields map[string]string `json:"fields"`
				ID     int64             `json:"id"`
			} `json:"note"`
		}](t, got.Params)
		ids = append(ids, params.Note.ID)
		return nil
	})

	err := client.UpdateNotes(t.Context(), []NoteUpdate{
		{ID: 1, Fields: map[string]string{"Front": "one"}},
		{ID: 2, Fields: map[string]string{"Back": "two"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []int64{1, 2}) {
		t.Fatalf("updated IDs = %v", ids)
	}
}

func TestStoreMediaFile(t *testing.T) {
	t.Parallel()
	client := NewClient(WithHTTPClient(doerFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if req.ContentLength != int64(len(body)) || len(req.TransferEncoding) != 0 {
			t.Fatalf("ContentLength=%d body=%d TransferEncoding=%v", req.ContentLength, len(body), req.TransferEncoding)
		}
		var got struct {
			Params struct {
				Filename string `json:"filename"`
				Data     string `json:"data"`
			} `json:"params"`
			Action  string `json:"action"`
			Version int    `json:"version"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Action != "storeMediaFile" || got.Version != apiVersion || got.Params.Filename != "_anki-tts.mp3" || got.Params.Data != "YXVkaW8=" {
			t.Fatalf("request = %+v", got)
		}
		return jsonResponse(`{"result":"_anki-tts.mp3","error":null}`), nil
	})))

	filename, err := client.StoreMediaFile(t.Context(), "_anki-tts.mp3", []byte("audio"))
	if err != nil {
		t.Fatal(err)
	}
	if filename != "_anki-tts.mp3" {
		t.Fatalf("filename = %q", filename)
	}
}

func TestStoreMediaFileValidation(t *testing.T) {
	t.Parallel()
	client := NewClient()
	if _, err := client.StoreMediaFile(t.Context(), "../audio.mp3", []byte("audio")); err == nil {
		t.Fatal("expected invalid filename error")
	}
	if _, err := client.StoreMediaFile(t.Context(), "audio.mp3", nil); err == nil {
		t.Fatal("expected empty data error")
	}
}

func TestAnkiConnectError(t *testing.T) {
	t.Parallel()
	client := NewClient(WithHTTPClient(doerFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"result":null,"error":"collection unavailable"}`), nil
	})))
	_, err := client.ListDecks(t.Context())
	if err == nil || err.Error() != "list decks: collection unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func TestClientRetryClassification(t *testing.T) {
	t.Parallel()
	client := NewClient()
	tests := []struct {
		err  error
		name string
		want bool
	}{
		{name: "transport", err: errors.New("offline"), want: true},
		{name: "attempt deadline", err: context.DeadlineExceeded, want: true},
		{name: "attempt cancellation", err: context.Canceled},
		{name: "permanent", err: permanent(errors.New("invalid request"))},
		{name: "API error", err: &apiError{message: "invalid note"}},
		{name: "request timeout", err: &httpError{statusCode: http.StatusRequestTimeout}, want: true},
		{name: "too early", err: &httpError{statusCode: http.StatusTooEarly}, want: true},
		{name: "rate limited", err: &httpError{statusCode: http.StatusTooManyRequests}, want: true},
		{name: "server error", err: &httpError{statusCode: http.StatusServiceUnavailable}, want: true},
		{name: "nonstandard status", err: &httpError{statusCode: 600}},
		{name: "bad request", err: &httpError{statusCode: http.StatusBadRequest}},
		{name: "not found", err: &httpError{statusCode: http.StatusNotFound}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := client.ShouldRetry(test.err); got != test.want {
				t.Fatalf("ShouldRetry()=%v want=%v", got, test.want)
			}
		})
	}
}

func testClient(t *testing.T, handler func(*testing.T, request) any) *Client {
	t.Helper()
	httpClient := doerFunc(func(r *http.Request) (*http.Response, error) {
		var raw struct {
			Action  string          `json:"action"`
			Params  json.RawMessage `json:"params"`
			Version int             `json:"version"`
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
