package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/router"
)

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIRequest struct {
	Model         string          `json:"model"`
	Messages      []openAIMessage `json:"messages"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions *streamOptions  `json:"stream_options,omitempty"`
}

type OpenAIClient struct {
	APIKey string
	APIURL string
}

func NewOpenAIClient(apiKey string) *OpenAIClient {
	return &OpenAIClient{
		APIKey: apiKey,
		APIURL: "https://api.openai.com/v1/chat/completions",
	}
}

// Complete returns the full response body. Callers own nothing afterwards.
func (c *OpenAIClient) Complete(ctx context.Context, prompt string, m router.Model) ([]byte, int, error) {
	body, status, err := c.do(ctx, openAIRequest{
		Model:    m.ID,
		Messages: []openAIMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil, 0, err
	}
	defer body.Close()

	respBody, err := io.ReadAll(body)
	if err != nil {
		return nil, 0, fmt.Errorf("read: %w", err)
	}
	return respBody, status, nil
}

// Stream returns the raw SSE body. The caller must Close it.
// include_usage is required or the terminal usage chunk never arrives and the
// completion module cannot meter the stream.
func (c *OpenAIClient) Stream(ctx context.Context, prompt string, m router.Model) (io.ReadCloser, int, error) {
	return c.do(ctx, openAIRequest{
		Model:         m.ID,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Messages:      []openAIMessage{{Role: "user", Content: prompt}},
	})
}

func (c *OpenAIClient) do(ctx context.Context, payload openAIRequest) (io.ReadCloser, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	// http.DefaultClient has no total timeout; cancellation is via ctx only.
	// A per-request deadline belongs on the caller's context, not here — a long stream
	// and a short completion want different budgets.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do: %w", err)
	}

	return resp.Body, resp.StatusCode, nil
}
