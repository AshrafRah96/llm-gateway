package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
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

func (c *OpenAIClient) Call(prompt, model string) ([]byte, int, error) {
	payload := openAIRequest{
		Model:    model,
		Messages: []openAIMessage{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.APIURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

func (c *OpenAIClient) CallStream(prompt, model string) (io.ReadCloser, int, error) {
	payload := openAIRequest{
		Model:    model,
		Stream:   true,
		Messages: []openAIMessage{{Role: "user", Content: prompt}},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.APIURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do: %w", err)
	}

	return resp.Body, resp.StatusCode, nil
}
