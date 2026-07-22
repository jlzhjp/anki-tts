package streamutil

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLimitedReaderWithinLimit(t *testing.T) {
	reader := NewLimitedReader(strings.NewReader("1234"), 4)
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "1234" {
		t.Fatalf("output = %q", output)
	}
	if reader.BytesRead() != 4 || reader.Limit() != 4 {
		t.Fatalf("bytes read = %d, limit = %d", reader.BytesRead(), reader.Limit())
	}
}

func TestLimitedReaderExceedsLimit(t *testing.T) {
	reader := NewLimitedReader(strings.NewReader("12345"), 4)
	_, err := io.ReadAll(reader)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("error = %v", err)
	}
	if reader.BytesRead() != 5 {
		t.Fatalf("bytes read = %d", reader.BytesRead())
	}
}
