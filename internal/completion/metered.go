package completion

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

type meteredProvider struct {
	provider Provider
	ledger   *usage.Tracker
}

func (p *meteredProvider) reserve(ctx context.Context, input ProviderInput, m router.Model) (usage.Reservation, *usage.Operation, context.Context, error) {
	// UTF-8 bytes plus per-message framing bound BPE input; output is provider-capped.
	data, _ := json.Marshal(input.Messages())
	in := len(data) + 256*len(input.Messages())
	cost, err := m.CostNanos(in, m.MaxOutputTokens)
	if err != nil {
		return usage.Reservation{}, nil, ctx, err
	}
	r, err := p.ledger.Reserve(ctx, usage.Project(ctx), rand.Text(), int64(in+m.MaxOutputTokens), cost)
	op := &usage.Operation{ClientID: r.ID}
	return r, op, usage.WithOperation(ctx, op), err
}

func (p *meteredProvider) settle(ctx context.Context, r usage.Reservation, e usage.Entry, final bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	if err := p.ledger.Settle(ctx, r, e, final); err != nil {
		return &usage.BudgetError{}
	}
	return nil
}

type providerUsageJSON struct {
	PromptTokens     *int `json:"prompt_tokens"`
	CompletionTokens *int `json:"completion_tokens"`
}

func (p *meteredProvider) Complete(ctx context.Context, input ProviderInput, m router.Model) ([]byte, int, error) {
	r, op, callCtx, err := p.reserve(ctx, input, m)
	if err != nil {
		return nil, 0, err
	}
	body, status, callErr := p.provider.Complete(callCtx, input, m)
	e := usage.Entry{Kind: "chat", Model: m.ID, ProviderRequestID: op.RequestID, Estimated: true}
	known := false
	if callErr == nil && status == http.StatusOK {
		var decoded struct {
			Usage *providerUsageJSON `json:"usage"`
		}
		if json.Unmarshal(body, &decoded) == nil && decoded.Usage != nil && decoded.Usage.PromptTokens != nil && decoded.Usage.CompletionTokens != nil {
			e.TokensIn, e.TokensOut = *decoded.Usage.PromptTokens, *decoded.Usage.CompletionTokens
			e.CostNanos, err = m.CostNanos(e.TokensIn, e.TokensOut)
			known = err == nil && e.TokensOut <= m.MaxOutputTokens
			e.Estimated = !known
		}
	}
	if !known {
		e.TokensIn, e.TokensOut = input.estimateTokens(), 0
		e.CostNanos, _ = m.CostNanos(e.TokensIn, 0)
	}
	if err := p.settle(ctx, r, e, known); err != nil {
		return nil, 0, err
	}
	if callErr == nil && status == http.StatusOK && !known {
		return nil, 0, fmt.Errorf("missing or invalid provider usage")
	}
	return body, status, callErr
}

func (p *meteredProvider) Stream(ctx context.Context, input ProviderInput, m router.Model) (ProviderStream, int, error) {
	r, op, callCtx, err := p.reserve(ctx, input, m)
	if err != nil {
		return nil, 0, err
	}
	body, status, err := p.provider.Stream(callCtx, input, m)
	if err != nil || status != http.StatusOK {
		if body != nil {
			_ = body.Close()
		}
		entry := usage.Entry{Kind: "chat", Model: m.ID, ProviderRequestID: op.RequestID, TokensIn: input.estimateTokens(), Estimated: true}
		entry.CostNanos, _ = m.CostNanos(entry.TokensIn, 0)
		if settleErr := p.settle(ctx, r, entry, false); settleErr != nil {
			return nil, 0, settleErr
		}
		return nil, status, err
	}
	return &meteredStream{body: body, p: p, ctx: ctx, res: r, op: op, model: m, input: input}, status, nil
}

type meteredStream struct {
	body                    ProviderStream
	p                       *meteredProvider
	ctx                     context.Context
	res                     usage.Reservation
	op                      *usage.Operation
	model                   router.Model
	input                   ProviderInput
	tokens                  *ProviderUsage
	outputBytes             int
	done, terminal, settled bool
	err                     error
}

func (s *meteredStream) Next() (ProviderEvent, bool) {
	if s.done {
		return ProviderEvent{}, false
	}
	e, ok := s.body.Next()
	if !ok {
		s.err = s.body.Err()
		s.done = true
		_ = s.Close()
		return ProviderEvent{}, false
	}
	if e.Usage != nil {
		s.tokens = e.Usage
	}
	s.outputBytes += len(e.Content)
	if e.Done {
		s.done, s.terminal = true, true
		if err := s.Close(); err != nil {
			return ProviderEvent{}, false
		}
	}
	return e, true
}
func (s *meteredStream) Err() error { return s.err }
func (s *meteredStream) Close() error {
	if s.settled {
		return s.err
	}
	s.settled = true
	s.err = errors.Join(s.err, s.body.Close())
	e := usage.Entry{Kind: "chat", Model: s.model.ID, ProviderRequestID: s.op.RequestID, TokensIn: s.input.estimateTokens(), TokensOut: min((s.outputBytes+3)/4, s.model.MaxOutputTokens), Estimated: true}
	known := s.tokens != nil
	if known {
		e.TokensIn, e.TokensOut = s.tokens.PromptTokens, s.tokens.CompletionTokens
		e.Estimated = false
		if e.TokensIn < 0 || e.TokensOut < 0 || e.TokensOut > s.model.MaxOutputTokens {
			known = false
		}
	}
	if !known {
		e.TokensIn = s.input.estimateTokens()
		e.TokensOut = min((s.outputBytes+3)/4, s.model.MaxOutputTokens)
		e.Estimated = true
	}
	var err error
	e.CostNanos, err = s.model.CostNanos(e.TokensIn, e.TokensOut)
	if err == nil {
		err = s.p.settle(s.ctx, s.res, e, known)
	}
	if s.terminal && !known {
		err = errors.Join(err, errors.New("missing or invalid terminal provider usage"))
	}
	if err != nil {
		s.err = errors.Join(s.err, err)
	}
	return s.err
}
