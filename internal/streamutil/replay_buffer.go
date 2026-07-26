package streamutil

import (
	"bytes"
	"errors"
	"io"
)

// ReplayBuffer eagerly caches a stream so it can be read repeatedly.
type ReplayBuffer struct {
	data []byte
}

// NewReplayBuffer reads reader into memory while enforcing limit.
func NewReplayBuffer(reader io.Reader, limit int64) (*ReplayBuffer, error) {
	if reader == nil {
		return nil, errors.New("replay buffer reader is required")
	}
	if limit <= 0 {
		return nil, errors.New("replay buffer limit must be positive")
	}
	data, err := io.ReadAll(NewLimitedReader(reader, limit))
	if err != nil {
		return nil, err
	}
	return &ReplayBuffer{data: data}, nil
}

// Reader returns an independent reader over the cached data.
func (b *ReplayBuffer) Reader() *bytes.Reader {
	return bytes.NewReader(b.data)
}

// Bytes returns the cached data. Callers must not modify it.
func (b *ReplayBuffer) Bytes() []byte {
	return b.data
}
