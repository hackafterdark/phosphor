// Package embeddings provides an EmbeddingClient that wraps an OpenAI-compatible
// embeddings API. It supports batch requests for efficient codebase indexing.
package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// EmbeddingClient wraps the OpenAI-compatible /v1/embeddings API.
type EmbeddingClient struct {
	baseURL    string
	model      string
	apiKey     string
	httpClient *http.Client
}

// NewEmbeddingClient creates a client for the configured embedding model.
// The base URL is normalized to avoid duplicate "/v1" segments.
func NewEmbeddingClient(baseURL, model, apiKey string) *EmbeddingClient {
	url := strings.TrimRight(baseURL, "/")
	url = strings.TrimSuffix(url, "/v1")
	return &EmbeddingClient{
		baseURL:    url,
		model:      model,
		apiKey:     apiKey,
		httpClient: http.DefaultClient,
	}
}

// EmbeddingRequest is the JSON body for the /v1/embeddings endpoint.
type EmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// EmbeddingResponse is the JSON body returned by /v1/embeddings.
type EmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// Embed sends a single text string and returns its embedding vector.
func (c *EmbeddingClient) Embed(text string) ([]float32, error) {
	vectors, err := c.EmbedBatch([]string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

// EmbedBatch sends multiple texts and returns their embedding vectors.
// Implements retry logic: for 400 (bad request, typically content too long),
// shrinks each text by 10% and retries (up to maxRetries).
// For 404 or timeouts, retries once. All other errors fail immediately.
func (c *EmbeddingClient) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("empty text list")
	}

	const maxRetries = 3

	for attempt := 0; attempt <= maxRetries; attempt++ {
		vectors, statusCode, err := c.doEmbedBatch(texts)
		if err != nil {
			return nil, err
		}

		// 200 OK — return successfully.
		if statusCode == http.StatusOK {
			return vectors, nil
		}

		// 400 Bad Request — likely content too long. Shrink texts and retry.
		if statusCode == http.StatusBadRequest {
			texts = shrinkTexts(texts, 0.9)
			continue
		}

		// 404 Not Found — endpoint doesn't exist or model unknown.
		// Retry once more in case of a transient DNS/routing issue.
		if statusCode == http.StatusNotFound && attempt < 1 {
			continue
		}

		// Any other error code — fail immediately.
		return nil, fmt.Errorf("API returned status %d", statusCode)
	}

	return nil, fmt.Errorf("embedding API returned 400 after %d retries (content shrinking did not resolve the issue)", maxRetries+1)
}

// doEmbedBatch sends the actual HTTP request and returns vectors, status code, and error.
// The body of the response is fully consumed before returning.
func (c *EmbeddingClient) doEmbedBatch(texts []string) (vectors [][]float32, statusCode int, err error) {
	req := EmbeddingRequest{
		Input: texts,
		Model: c.model,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	vectors = make([][]float32, len(texts))
	if resp.StatusCode == http.StatusOK {
		var embeddingResp EmbeddingResponse
		if err := json.NewDecoder(resp.Body).Decode(&embeddingResp); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
		for _, data := range embeddingResp.Data {
			if data.Index >= 0 && data.Index < len(texts) {
				vectors[data.Index] = data.Embedding
			}
		}
	} else {
		// Discard non-200 response body so the connection can be reused.
		io.Copy(io.Discard, resp.Body)
	}

	return vectors, resp.StatusCode, nil
}

// shrinkTexts reduces the length of each text by the given factor (e.g., 0.9 = 90%).
func shrinkTexts(texts []string, factor float64) []string {
	shrunk := make([]string, len(texts))
	for i, t := range texts {
		newLen := int(float64(len(t)) * factor)
		if newLen < 1 {
			newLen = 1
		}
		shrunk[i] = t[:newLen]
	}
	return shrunk
}
