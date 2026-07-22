package streamutil

import (
	"bytes"
	"sync"
)

// BoundedBuffer retains at most its configured number of bytes while
// reporting every write as fully consumed.
type BoundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

// NewBoundedBuffer constructs a buffer that retains at most limit bytes.
func NewBoundedBuffer(limit int) *BoundedBuffer {
	return &BoundedBuffer{limit: limit}
}

func (b *BoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = b.buffer.Write(chunk)
	}
	if len(p) > remaining {
		b.truncated = true
	}
	return len(p), nil
}

func (b *BoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	message := b.buffer.String()
	if b.truncated {
		message += " [truncated]"
	}
	return message
}
