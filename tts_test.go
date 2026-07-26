package ankitts

import (
	"context"
	"errors"
	"testing"
)

func TestCombineCostLoaders(t *testing.T) {
	t.Parallel()
	var order []int
	loader := CombineCostLoaders(
		func(context.Context) (float64, error) {
			order = append(order, 1)
			return 1.25, nil
		},
		func(context.Context) (float64, error) {
			order = append(order, 2)
			return 0.75, nil
		},
	)
	cost, err := loader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cost != 2 || len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("cost=%v order=%v", cost, order)
	}
}

func TestCombineCostLoadersStopsOnFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("cost failed")
	called := false
	loader := CombineCostLoaders(
		func(context.Context) (float64, error) { return 0, want },
		func(context.Context) (float64, error) {
			called = true
			return 1, nil
		},
	)
	_, err := loader(t.Context())
	if !errors.Is(err, want) || called {
		t.Fatalf("error=%v called=%v", err, called)
	}
}

func TestCombineCostLoadersRejectsMissingLoader(t *testing.T) {
	t.Parallel()
	_, err := CombineCostLoaders(nil)(t.Context())
	if !errors.Is(err, ErrCostUnavailable) {
		t.Fatalf("error=%v", err)
	}
}
