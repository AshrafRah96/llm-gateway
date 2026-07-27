package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/router"
)

const maxSSELine = 1 << 20

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

type streamUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *streamUsage `json:"usage"`
}

type openAIStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	err     error
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

// Stream translates OpenAI's SSE wire format into provider-neutral events.
// include_usage is required or the terminal usage event never arrives and the
// completion module cannot meter the stream.
func (c *OpenAIClient) Stream(ctx context.Context, prompt string, m router.Model) (completion.ProviderStream, int, error) {
	body, status, err := c.do(ctx, openAIRequest{
		Model:         m.ID,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Messages:      []openAIMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return nil, 0, err
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	return &openAIStream{body: body, scanner: scanner}, status, nil
}

func (s *openAIStream) Next() (completion.ProviderEvent, bool) {
	if s.err != nil {
		return completion.ProviderEvent{}, false
	}

	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return completion.ProviderEvent{Done: true}, true
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			s.err = fmt.Errorf("decode stream event: %w", err)
			return completion.ProviderEvent{}, false
		}

		var content strings.Builder
		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
		}
		event := completion.ProviderEvent{Content: content.String()}
		if chunk.Usage != nil {
			event.Usage = &completion.ProviderUsage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
			}
		}
		return event, true
	}

	s.err = s.scanner.Err()
	return completion.ProviderEvent{}, false
}

func (s *openAIStream) Err() error {
	return s.err
}

func (s *openAIStream) Close() error {
	return s.body.Close()
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
