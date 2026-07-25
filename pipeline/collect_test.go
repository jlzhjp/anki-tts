package pipeline

import (
	"context"
	"testing"
)

func TestCollectRejectsUninitializedStream(t *testing.T) {
	if _, err := Collect(context.Background(), Stream[int]{}, nil); err == nil {
		t.Fatal("expected uninitialized stream error")
	}
}
