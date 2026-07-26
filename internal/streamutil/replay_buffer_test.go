package streamutil

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReplayBufferReadersAreIndependent(t *testing.T) {
	t.Parallel()
	source := &countingReader{Reader: strings.NewReader("audio")}
	buffer, err := NewReplayBuffer(source, 5)
	if err != nil {
		t.Fatal(err)
	}
	if source.reads == 0 {
		t.Fatal("source was not read")
	}
	readsAfterConstruction := source.reads

	first := buffer.Reader()
	prefix := make([]byte, 2)
	if _, err := io.ReadFull(first, prefix); err != nil {
		t.Fatal(err)
	}
	second, err := io.ReadAll(buffer.Reader())
	if err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(first)
	if err != nil {
		t.Fatal(err)
	}
	if string(prefix) != "au" || string(rest) != "dio" || string(second) != "audio" {
		t.Fatalf("prefix=%q rest=%q second=%q", prefix, rest, second)
	}
	if source.reads != readsAfterConstruction {
		t.Fatalf("source read again: before=%d after=%d", readsAfterConstruction, source.reads)
	}
	if string(buffer.Bytes()) != "audio" {
		t.Fatalf("bytes=%q", buffer.Bytes())
	}
}

func TestReplayBufferLimit(t *testing.T) {
	t.Parallel()
	buffer, err := NewReplayBuffer(strings.NewReader("audio"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer.Bytes()) != "audio" {
		t.Fatalf("bytes=%q", buffer.Bytes())
	}
	_, err = NewReplayBuffer(strings.NewReader("audio!"), 5)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("error=%v", err)
	}
}

func TestReplayBufferRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reader io.Reader
		name   string
		limit  int64
	}{
		{name: "nil reader", limit: 1},
		{name: "zero limit", reader: strings.NewReader("audio")},
		{name: "negative limit", reader: strings.NewReader("audio"), limit: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewReplayBuffer(test.reader, test.limit); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

type countingReader struct {
	io.Reader
	reads int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}
