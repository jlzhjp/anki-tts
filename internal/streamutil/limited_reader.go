// Package streamutil provides reusable stream primitives.
package streamutil

import (
	"errors"
	"io"
)

// ErrLimitExceeded indicates that a reader produced more than its configured limit.
var ErrLimitExceeded = errors.New("stream size limit exceeded")

// LimitedReader reads from an underlying reader while enforcing a byte limit.
type LimitedReader struct {
	reader    io.Reader
	limit     int64
	bytesRead int64
}

// NewLimitedReader wraps reader with the provided byte limit.
func NewLimitedReader(reader io.Reader, limit int64) *LimitedReader {
	return &LimitedReader{reader: reader, limit: limit}
}

func (r *LimitedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	remaining := r.limit - r.bytesRead
	if remaining < 0 {
		return 0, ErrLimitExceeded
	}
	buffer := p
	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining+1]
	}
	n, err := r.reader.Read(buffer)
	r.bytesRead += int64(n)
	if int64(n) > remaining {
		return 0, ErrLimitExceeded
	}
	return n, err
}

// BytesRead reports how many bytes were read from the underlying reader.
func (r *LimitedReader) BytesRead() int64 { return r.bytesRead }

// Limit reports the configured byte limit.
func (r *LimitedReader) Limit() int64 { return r.limit }
