package openrouter

import (
	"context"
	"net/http"
	"testing"
)

func TestOpenRouterCostCalculator(t *testing.T) {
	const modelsEndpoint = "https://example.test/models"
	modelsCalls := 0
	calculator := newOpenRouterCostCalculator(modelsEndpoint, "secret", doerFunc(func(req *http.Request) (*http.Response, error) {
		modelsCalls++
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s", req.Method)
		}
		if req.URL.Path != "/models" || req.URL.Query().Get("output_modalities") != "speech" {
			t.Fatalf("pricing request URL = %s", req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q", got)
		}
		return response(http.StatusOK, "application/json", []byte(`{"data":[{"id":"model","pricing":{"prompt":"0.000625","completion":"0"}}]}`)), nil
	}))

	cost, err := calculator.Calculate(context.Background(), "猫a", "model")
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0.00125 {
		t.Fatalf("cost = %v", cost)
	}
	if _, err := calculator.Calculate(context.Background(), "another sentence", "model"); err != nil {
		t.Fatal(err)
	}
	if modelsCalls != 1 {
		t.Fatalf("models API calls = %d, want 1", modelsCalls)
	}
}
