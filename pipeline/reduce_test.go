package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestReduceFoldsResultsInEmissionOrder(t *testing.T) {
	t.Parallel()
	failure := errors.New("failed")
	stream := Stream[string]{start: func(*execution) <-chan entry[string] {
		output := make(chan entry[string], 2)
		output <- entry[string]{index: 2, value: "third"}
		output <- entry[string]{index: 0, failure: failure, stage: "work"}
		close(output)
		return output
	}}
	results, err := Reduce(
		t.Context(),
		stream,
		nil,
		[]Result[string](nil),
		func(results []Result[string], result Result[string]) []Result[string] {
			return append(results, result)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 ||
		results[0].Index != 2 || results[0].Value != "third" ||
		results[1].Index != 0 || results[1].Stage != "work" ||
		!errors.Is(results[1].Err, failure) {
		t.Fatalf("results=%+v", results)
	}
}

func TestReduceReturnsPartialAccumulatorOnCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	stream := Stream[int]{start: func(*execution) <-chan entry[int] {
		output := make(chan entry[int], 1)
		output <- entry[int]{index: 0, value: 4}
		close(output)
		cancel()
		return output
	}}
	values, err := Reduce(
		ctx,
		stream,
		nil,
		[]int(nil),
		func(values []int, result Result[int]) []int {
			return append(values, result.Value)
		},
	)
	if !errors.Is(err, context.Canceled) || !slices.Equal(values, []int{4}) {
		t.Fatalf("values=%v error=%v", values, err)
	}
}

func TestReduceHandlesEmptyStream(t *testing.T) {
	t.Parallel()
	total, err := Reduce(
		t.Context(),
		FromSlice([]int(nil)),
		nil,
		7,
		func(total int, result Result[int]) int { return total + result.Value },
	)
	if err != nil || total != 7 {
		t.Fatalf("total=%d error=%v", total, err)
	}
}

func TestReduceValidatesArguments(t *testing.T) {
	t.Parallel()
	reducer := func(total int, result Result[int]) int { return total + result.Value }
	if _, err := Reduce(t.Context(), Stream[int]{}, nil, 0, reducer); err == nil {
		t.Fatal("expected uninitialized stream error")
	}
	if _, err := Reduce[int](t.Context(), FromSlice([]int{1}), nil, 0, nil); err == nil {
		t.Fatal("expected nil reducer error")
	}
}
