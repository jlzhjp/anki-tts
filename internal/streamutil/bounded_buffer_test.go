package streamutil

import "testing"

func TestBoundedBufferWithinLimit(t *testing.T) {
	buffer := NewBoundedBuffer(5)
	n, err := buffer.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if got := buffer.String(); got != "hello" {
		t.Fatalf("buffer = %q", got)
	}
}

func TestBoundedBufferTruncatesWithoutShortWrite(t *testing.T) {
	buffer := NewBoundedBuffer(4)
	n, err := buffer.Write([]byte("12345"))
	if err != nil || n != 5 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if got := buffer.String(); got != "1234 [truncated]" {
		t.Fatalf("buffer = %q", got)
	}
}
