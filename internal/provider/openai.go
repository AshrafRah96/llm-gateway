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
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

const (
	maxJSONResponse = 1 << 20
	maxSSELine      = 1 << 20
	maxSSEBody      = 4 << 20
)

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIRequest struct {
	Model         string               `json:"model"`
	Messages      []completion.Message `json:"messages"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *streamOptions       `json:"stream_options,omitempty"`
	MaxTokens     int                  `json:"max_tokens,omitempty"`
}

type OpenAIClient struct {
	APIKey string
	APIURL string
}

type streamUsage struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
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
func (c *OpenAIClient) Complete(ctx context.Context, input completion.ProviderInput, m router.Model) ([]byte, int, error) {
	body, status, err := c.do(ctx, openAIRequest{
		Model:     m.ID,
		MaxTokens: m.MaxOutputTokens,
		Messages:  input.Messages(),
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
func (c *OpenAIClient) Stream(ctx context.Context, input completion.ProviderInput, m router.Model) (completion.ProviderStream, int, error) {
	body, status, err := c.do(ctx, openAIRequest{
		Model:         m.ID,
		MaxTokens:     m.MaxOutputTokens,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		Messages:      input.Messages(),
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

	// A buffered Done must not hide a body-limit error from the same read.
	for s.scanner.Scan() && s.scanner.Err() == nil {
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
			if chunk.Usage.PromptTokens == nil || chunk.Usage.CompletionTokens == nil || *chunk.Usage.PromptTokens < 0 || *chunk.Usage.CompletionTokens < 0 {
				s.err = fmt.Errorf("invalid stream usage")
				s.body.Close()
				return completion.ProviderEvent{}, false
			}
			event.Usage = &completion.ProviderUsage{
				PromptTokens:     *chunk.Usage.PromptTokens,
				CompletionTokens: *chunk.Usage.CompletionTokens,
			}
		}
		return event, true
	}

	s.err = s.scanner.Err()
	if s.err != nil {
		s.body.Close()
	}
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
	if id := usage.ClientRequestID(ctx); id != "" {
		req.Header.Set("X-Client-Request-Id", id)
	}

	// http.DefaultClient has no total timeout; cancellation is via ctx only.
	// A per-request deadline belongs on the caller's context, not here — a long stream
	// and a short completion want different budgets.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("do: %w", err)
	}

	usage.ProviderRequestID(ctx, resp.Header.Get("x-request-id"))

	limit := int64(maxJSONResponse)
	if payload.Stream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		limit = maxSSEBody
	}
	return http.MaxBytesReader(nil, resp.Body, limit), resp.StatusCode, nil
}
