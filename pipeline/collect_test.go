package pipeline

import (
	"testing"
)

func TestCollectRejectsUninitializedStream(t *testing.T) {
	t.Parallel()
	if _, err := Collect(t.Context(), Stream[int]{}, nil); err == nil {
		t.Fatal("expected uninitialized stream error")
	}
}

func TestCollectSortsActualSparseResults(t *testing.T) {
	t.Parallel()
	stream := Stream[string]{start: func(*execution) <-chan entry[string] {
		output := make(chan entry[string], 2)
		output <- entry[string]{index: 2, value: "third"}
		output <- entry[string]{index: 0, value: "first"}
		close(output)
		return output
	}}
	results, err := Collect(t.Context(), stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 ||
		results[0].Index != 0 || results[0].Value != "first" ||
		results[1].Index != 2 || results[1].Value != "third" {
		t.Fatalf("results=%+v", results)
	}
}
