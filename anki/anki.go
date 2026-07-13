// Package anki provides a client for the AnkiConnect HTTP API.
package anki

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	endpoint   string
	httpClient HTTPClient
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
	ID        int64            `json:"noteId"`
	ModelName string           `json:"modelName"`
	Tags      []string         `json:"tags"`
	Fields    map[string]Field `json:"fields"`
	Cards     []int64          `json:"cards"`
}

// NoteUpdate describes the fields to replace on an existing note. Fields not
// present in Fields are left unchanged by AnkiConnect.
type NoteUpdate struct {
	ID     int64
	Fields map[string]string
}

type request struct {
	Action  string `json:"action"`
	Version int    `json:"version"`
	Params  any    `json:"params,omitempty"`
}

type response[T any] struct {
	Result T       `json:"result"`
	Error  *string `json:"error"`
}

// ListDecks returns all deck names in the current Anki collection.
func (c *Client) ListDecks(ctx context.Context) ([]string, error) {
	var decks []string
	if err := c.invoke(ctx, "deckNames", nil, &decks); err != nil {
		return nil, fmt.Errorf("list decks: %w", err)
	}
	return decks, nil
}

// ListNotes returns all notes in deck. Notes in child decks are included,
// matching Anki's deck search semantics.
func (c *Client) ListNotes(ctx context.Context, deck string) ([]Note, error) {
	if strings.TrimSpace(deck) == "" {
		return nil, errors.New("list notes: deck name is required")
	}

	query := `deck:"` + escapeSearchValue(deck) + `"`
	var ids []int64
	if err := c.invoke(ctx, "findNotes", struct {
		Query string `json:"query"`
	}{Query: query}, &ids); err != nil {
		return nil, fmt.Errorf("list notes: find notes: %w", err)
	}
	if len(ids) == 0 {
		return []Note{}, nil
	}

	var notes []Note
	if err := c.invoke(ctx, "notesInfo", struct {
		Notes []int64 `json:"notes"`
	}{Notes: ids}, &notes); err != nil {
		return nil, fmt.Errorf("list notes: get note information: %w", err)
	}
	return notes, nil
}

// UpdateNote updates the supplied fields of one note.
func (c *Client) UpdateNote(ctx context.Context, update NoteUpdate) error {
	if update.ID <= 0 {
		return errors.New("update note: note ID must be positive")
	}
	if len(update.Fields) == 0 {
		return errors.New("update note: at least one field is required")
	}

	params := struct {
		Note struct {
			ID     int64             `json:"id"`
			Fields map[string]string `json:"fields"`
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

func (c *Client) invoke(ctx context.Context, action string, params, result any) error {
	body, err := json.Marshal(request{Action: action, Version: apiVersion, Params: params})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	limitedBody := io.LimitReader(resp.Body, maxResponseSize+1)
	responseBody, err := io.ReadAll(limitedBody)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(responseBody) > maxResponseSize {
		return fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected HTTP status %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	wrapper := response[json.RawMessage]{}
	if err := json.Unmarshal(responseBody, &wrapper); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if wrapper.Error != nil {
		return errors.New(*wrapper.Error)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(wrapper.Result, result); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	return nil
}

func escapeSearchValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
