package pipeline

import "testing"

func TestCollectRejectsUninitializedStream(t *testing.T) {
	t.Parallel()
	if _, err := Collect(t.Context(), Stream[int]{}, nil); err == nil {
		t.Fatal("expected uninitialized stream error")
	}
}
