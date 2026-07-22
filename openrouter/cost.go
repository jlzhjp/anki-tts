package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"unicode/utf8"
)

type costCalculator interface {
	Calculate(ctx context.Context, sentence, model string) (float64, error)
}

type openRouterCostCalculator struct {
	endpoint      string
	apiKey        string
	httpClient    HTTPClient
	mu            sync.Mutex
	pricesByModel map[string]float64
}

func newOpenRouterCostCalculator(endpoint, apiKey string, httpClient HTTPClient) costCalculator {
	return &openRouterCostCalculator{
		endpoint:      endpoint,
		apiKey:        apiKey,
		httpClient:    httpClient,
		pricesByModel: make(map[string]float64),
	}
}

func (c *openRouterCostCalculator) Calculate(ctx context.Context, sentence, model string) (float64, error) {
	price, err := c.loadPricePerCharacter(ctx, model)
	if err != nil {
		return 0, err
	}
	return float64(utf8.RuneCountInString(sentence)) * price, nil
}

func (c *openRouterCostCalculator) loadPricePerCharacter(ctx context.Context, modelName string) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if price, ok := c.pricesByModel[modelName]; ok {
		return price, nil
	}

	endpoint, err := url.Parse(c.endpoint)
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: parse endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("output_modalities", "speech")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, openRouterAPIError("load OpenRouter TTS pricing", resp, c.apiKey)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsResponseSize+1))
	if err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: read response: %w", err)
	}
	if len(body) > maxModelsResponseSize {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: response exceeds %d bytes", maxModelsResponseSize)
	}
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt string `json:"prompt"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("load OpenRouter TTS pricing: decode response: %w", err)
	}
	for _, model := range result.Data {
		if model.ID != modelName {
			continue
		}
		price, err := strconv.ParseFloat(model.Pricing.Prompt, 64)
		if err != nil || price < 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return 0, fmt.Errorf("load OpenRouter TTS pricing: model %q has invalid per-character price %q", modelName, model.Pricing.Prompt)
		}
		c.pricesByModel[modelName] = price
		return price, nil
	}
	return 0, fmt.Errorf("load OpenRouter TTS pricing: model %q was not found in speech models", modelName)
}

var _ costCalculator = (*openRouterCostCalculator)(nil)
