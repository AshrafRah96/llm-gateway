package cache

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

const EmbeddingModel = "text-embedding-3-small"

const embeddingResponseLimit = 128 << 10

type EmbeddingClient struct {
	apiKey string
	url    string
	ledger *usage.Tracker
	kind   string
}

func NewMeteredEmbeddingClient(apiKey string, ledger *usage.Tracker, kind string) *EmbeddingClient {
	c := NewEmbeddingClient(apiKey)
	c.ledger, c.kind = ledger, kind
	return c
}

func NewEmbeddingClient(apiKey string) *EmbeddingClient {
	return &EmbeddingClient{
		apiKey: apiKey,
		url:    "https://api.openai.com/v1/embeddings",
	}
}

type embeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage *struct {
		PromptTokens *int `json:"prompt_tokens"`
	} `json:"usage"`
}

func (c *EmbeddingClient) Embed(ctx context.Context, text string) (vector []float32, err error) {
	var reservation usage.Reservation
	var charge usage.Entry
	known := false
	if c.ledger != nil {
		// UTF-8 bytes bound BPE input tokens; the provider caps embeddings at 8192 tokens.
		bound := min(len(text), 8192)
		reservation, err = c.ledger.Reserve(ctx, usage.Project(ctx), rand.Text(), int64(bound), int64(bound)*20)
		if err != nil {
			return nil, err
		}
		charge = usage.Entry{Kind: c.kind, Model: EmbeddingModel, TokensIn: bound, CostNanos: int64(bound) * 20, Estimated: true}
		defer func() {
			settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if settleErr := c.ledger.Settle(settleCtx, reservation, charge, known); settleErr != nil {
				vector = nil
				err = &usage.BudgetError{}
			}
		}()
	}
	reqBody, err := json.Marshal(embeddingRequest{
		Model: EmbeddingModel,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if reservation.ID != "" {
		req.Header.Set("X-Client-Request-Id", reservation.ID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	charge.ProviderRequestID = resp.Header.Get("x-request-id")
	if len(charge.ProviderRequestID) > 256 {
		charge.ProviderRequestID = charge.ProviderRequestID[:256]
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, embeddingResponseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(body) > embeddingResponseLimit {
		return nil, fmt.Errorf("embedding response exceeds %d bytes", embeddingResponseLimit)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error: embedding status %d", resp.StatusCode)
	}

	var embResp embeddingResponse
	if err := json.Unmarshal(body, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if c.ledger != nil {
		if embResp.Usage == nil || embResp.Usage.PromptTokens == nil {
			return nil, fmt.Errorf("embedding response missing prompt_tokens usage")
		}
		tokens := *embResp.Usage.PromptTokens
		if tokens < 0 || int64(tokens) > reservation.Tokens {
			return nil, fmt.Errorf("embedding usage exceeds reserved bounds")
		}
		charge.TokensIn, charge.CostNanos, charge.Estimated = tokens, int64(tokens)*20, false
		known = true
	}

	if len(embResp.Data) == 0 || len(embResp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return embResp.Data[0].Embedding, nil
}
