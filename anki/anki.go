// Package anki provides a client for the AnkiConnect HTTP API.
package anki

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultEndpoint = "http://localhost:8765"
	apiVersion      = 5
	maxResponseSize = 16 << 20 // 16 MiB
)

// HTTPClient is implemented by *http.Client. It is exposed to make Client easy
// to use with custom transports and in tests.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Client communicates with AnkiConnect.
type Client struct {
	httpClient HTTPClient
	endpoint   string
}

// Option configures a Client.
type Option func(*Client)

// WithEndpoint changes the AnkiConnect endpoint. The default is
// http://localhost:8765.
func WithEndpoint(endpoint string) Option {
	return func(c *Client) {
		c.endpoint = strings.TrimRight(endpoint, "/")
	}
}

// WithHTTPClient changes the HTTP client used to call AnkiConnect.
func WithHTTPClient(client HTTPClient) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// NewClient creates an AnkiConnect client.
func NewClient(options ...Option) *Client {
	c := &Client{
		endpoint: defaultEndpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, option := range options {
		if option != nil {
			option(c)
		}
	}

	return c
}

// Field is a field returned by AnkiConnect.
type Field struct {
	Value string `json:"value"`
	Order int    `json:"order"`
}

// Note contains the information AnkiConnect returns for a note.
type Note struct {
	Fields    map[string]Field `json:"fields"`
	ModelName string           `json:"modelName"`
	Tags      []string         `json:"tags"`
	Cards     []int64          `json:"cards"`
	ID        int64            `json:"noteId"`
}

// NoteUpdate describes the fields to replace on an existing note. Fields not
// present in Fields are left unchanged by AnkiConnect.
type NoteUpdate struct {
	Fields map[string]string
	ID     int64
}

type request struct {
	Params  any    `json:"params,omitempty"`
	Action  string `json:"action"`
	Version int    `json:"version"`
}

type response[T any] struct {
	Result T       `json:"result"`
	Error  *string `json:"error"`
}

type httpError struct {
	message    string
	statusCode int
}

func (e *httpError) Error() string { return e.message }

type apiError struct{ message string }

func (e *apiError) Error() string { return e.message }

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func permanent(err error) error {
	return &permanentError{err: err}
}

// ListDecks returns all deck names in the current Anki collection.
func (c *Client) ListDecks(ctx context.Context) ([]string, error) {
	var decks []string
	if err := c.invoke(ctx, "deckNames", nil, &decks); err != nil {
		return nil, fmt.Errorf("list decks: %w", err)
	}
	return decks, nil
}

// ListNoteTemplates returns all note type (model) names in the collection.
func (c *Client) ListNoteTemplates(ctx context.Context) ([]string, error) {
	var templates []string
	if err := c.invoke(ctx, "modelNames", nil, &templates); err != nil {
		return nil, fmt.Errorf("list note templates: %w", err)
	}
	return templates, nil
}

// ListTemplateFields returns the ordered field names for a note type.
func (c *Client) ListTemplateFields(ctx context.Context, template string) ([]string, error) {
	if strings.TrimSpace(template) == "" {
		return nil, errors.New("list template fields: note template is required")
	}
	var fields []string
	if err := c.invoke(ctx, "modelFieldNames", struct {
		ModelName string `json:"modelName"`
	}{ModelName: template}, &fields); err != nil {
		return nil, fmt.Errorf("list template fields: %w", err)
	}
	return fields, nil
}

// FindNoteIDs returns note IDs matching Anki's native search syntax.
func (c *Client) FindNoteIDs(ctx context.Context, query string) ([]int64, error) {
	var ids []int64
	if err := c.invoke(ctx, "findNotes", struct {
		Query string `json:"query"`
	}{Query: query}, &ids); err != nil {
		return nil, fmt.Errorf("find notes: %w", err)
	}
	return ids, nil
}

// NotesInfo returns complete note information for the supplied IDs.
func (c *Client) NotesInfo(ctx context.Context, ids []int64) ([]Note, error) {
	if len(ids) == 0 {
		return []Note{}, nil
	}

	var notes []Note
	if err := c.invoke(ctx, "notesInfo", struct {
		Notes []int64 `json:"notes"`
	}{Notes: ids}, &notes); err != nil {
		return nil, fmt.Errorf("get note information: %w", err)
	}
	return notes, nil
}

// UpdateNote updates the supplied fields of one note.
func (c *Client) UpdateNote(ctx context.Context, update NoteUpdate) error {
	if update.ID <= 0 {
		return permanent(errors.New("update note: note ID must be positive"))
	}
	if len(update.Fields) == 0 {
		return permanent(errors.New("update note: at least one field is required"))
	}

	params := struct {
		Note struct {
			Fields map[string]string `json:"fields"`
			ID     int64             `json:"id"`
		} `json:"note"`
	}{}
	params.Note.ID = update.ID
	params.Note.Fields = update.Fields

	var result json.RawMessage
	if err := c.invoke(ctx, "updateNoteFields", params, &result); err != nil {
		return fmt.Errorf("update note %d: %w", update.ID, err)
	}
	return nil
}

// UpdateNotes updates notes in order. It stops and returns an error on the
// first failed update; earlier updates may already have been applied.
func (c *Client) UpdateNotes(ctx context.Context, updates []NoteUpdate) error {
	for _, update := range updates {
		if err := c.UpdateNote(ctx, update); err != nil {
			return err
		}
	}
	return nil
}

// StoreMediaFile stores data in Anki's media collection and returns the
// filename accepted by AnkiConnect. Base64 is encoded directly into the HTTP
// request body instead of allocating a second encoded copy of data.
func (c *Client) StoreMediaFile(ctx context.Context, filename string, data []byte) (string, error) {
	if strings.TrimSpace(filename) == "" {
		return "", permanent(errors.New("store media file: filename is required"))
	}
	if filepath.Base(filename) != filename || strings.ContainsAny(filename, `/\`) {
		return "", permanent(errors.New("store media file: filename must not contain path separators"))
	}
	if len(data) == 0 {
		return "", permanent(errors.New("store media file: data is required"))
	}

	encodedFilename, err := json.Marshal(filename)
	if err != nil {
		return "", permanent(fmt.Errorf("store media file %q: encode filename: %w", filename, err))
	}
	prefix := fmt.Appendf(nil, `{"action":"storeMediaFile","version":%d,"params":{"filename":%s,"data":"`, apiVersion, encodedFilename)
	suffix := []byte(`"}}`)
	contentLength := int64(len(prefix) + base64.StdEncoding.EncodedLen(len(data)) + len(suffix))

	reader, writer := io.Pipe()
	writeResult := make(chan error, 1)
	go func() {
		_, err := writer.Write(prefix)
		if err == nil {
			encoder := base64.NewEncoder(base64.StdEncoding, writer)
			_, err = io.Copy(encoder, bytes.NewReader(data))
			if closeErr := encoder.Close(); err == nil {
				err = closeErr
			}
		}
		if err == nil {
			_, err = writer.Write(suffix)
		}
		_ = writer.CloseWithError(err)
		writeResult <- err
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, reader)
	if err != nil {
		_ = reader.CloseWithError(err)
		<-writeResult
		return "", permanent(fmt.Errorf("store media file %q: create request: %w", filename, err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = contentLength
	resp, requestErr := c.httpClient.Do(req)
	if requestErr != nil {
		_ = reader.CloseWithError(requestErr)
	}
	streamErr := <-writeResult
	if requestErr != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return "", fmt.Errorf("store media file %q: send request: %w", filename, requestErr)
	}
	if streamErr != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return "", permanent(
			fmt.Errorf("store media file %q: encode request: %w", filename, streamErr),
		)
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("store media file %q: read response: %w", filename, err)
	}
	if len(responseBody) > maxResponseSize {
		return "", permanent(
			fmt.Errorf("store media file %q: response exceeds %d bytes", filename, maxResponseSize),
		)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", &httpError{
			statusCode: resp.StatusCode,
			message: fmt.Sprintf(
				"store media file %q: unexpected HTTP status %s: %s",
				filename,
				resp.Status,
				strings.TrimSpace(string(responseBody)),
			),
		}
	}
	var wrapper response[string]
	if err := json.Unmarshal(responseBody, &wrapper); err != nil {
		return "", permanent(
			fmt.Errorf("store media file %q: decode response: %w", filename, err),
		)
	}
	if wrapper.Error != nil {
		return "", &apiError{
			message: fmt.Sprintf("store media file %q: %s", filename, *wrapper.Error),
		}
	}
	if wrapper.Result == "" {
		wrapper.Result = filename
	}
	return wrapper.Result, nil
}

func (c *Client) invoke(ctx context.Context, action string, params, result any) error {
	body, err := json.Marshal(request{Action: action, Version: apiVersion, Params: params})
	if err != nil {
		return permanent(fmt.Errorf("encode request: %w", err))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return permanent(fmt.Errorf("create request: %w", err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limitedBody := io.LimitReader(resp.Body, maxResponseSize+1)
	responseBody, err := io.ReadAll(limitedBody)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(responseBody) > maxResponseSize {
		return permanent(fmt.Errorf("response exceeds %d bytes", maxResponseSize))
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &httpError{
			statusCode: resp.StatusCode,
			message: fmt.Sprintf(
				"unexpected HTTP status %s: %s",
				resp.Status,
				strings.TrimSpace(string(responseBody)),
			),
		}
	}

	wrapper := response[json.RawMessage]{}
	if err := json.Unmarshal(responseBody, &wrapper); err != nil {
		return permanent(fmt.Errorf("decode response: %w", err))
	}
	if wrapper.Error != nil {
		return &apiError{message: *wrapper.Error}
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(wrapper.Result, result); err != nil {
		return permanent(fmt.Errorf("decode result: %w", err))
	}
	return nil
}

// ShouldRetry classifies errors returned by Anki operations.
func (*Client) ShouldRetry(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var permanentErr *permanentError
	if errors.As(err, &permanentErr) {
		return false
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return false
	}
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return retryableHTTPStatus(httpErr.statusCode)
	}
	return true
}

func retryableHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError &&
			status < 600
}
