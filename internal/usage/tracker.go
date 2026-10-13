// Package usage owns the persistent project budget and identified usage ledger.
package usage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/money"
	"github.com/redis/go-redis/v9"
	"strconv"
)

const DefaultTokenLimit int64 = 100_000
const DefaultCostLimit int64 = 10 * money.UnitsPerUSD

type Stats struct {
	Requests          int64   `json:"requests"`
	TokensIn          int64   `json:"tokens_in"`
	TokensOut         int64   `json:"tokens_out"`
	CostUSD           float64 `json:"cost_usd"`
	EstimatedRequests int64   `json:"estimated_requests"`
	EmbeddingRequests int64   `json:"embedding_requests"`
	EmbeddingTokens   int64   `json:"embedding_tokens"`
	EmbeddingCostUSD  float64 `json:"embedding_cost_usd"`
	TotalTokens       int64   `json:"total_tokens"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
}

// Entry records one operation, never its prompt or response content.
type Entry struct {
	APIKey            string            `json:"project"`
	EventID           string            `json:"event_id"`
	Kind              string            `json:"kind"`
	Model             string            `json:"model,omitempty"`
	ProviderRequestID string            `json:"provider_request_id,omitempty"`
	Day               string            `json:"day"`
	TokensIn          int               `json:"tokens_in"`
	TokensOut         int               `json:"tokens_out"`
	CostNanos         int64             `json:"cost_nanos"`
	CostUSD           float64           `json:"-"` // Legacy in-process callers; production uses CostNanos.
	Estimated         bool              `json:"estimated"`
	State             string            `json:"state"`
	Baseline          map[string]int64  `json:"baseline,omitempty"`
	Replaces          string            `json:"replaces,omitempty"`
	Evidence          string            `json:"evidence,omitempty"`
	Legacy            map[string]string `json:"legacy,omitempty"`
	Reservation       *Reservation      `json:"reservation,omitempty"`
}

type Reservation struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Day       string `json:"day"`
	Tokens    int64  `json:"tokens"`
	CostNanos int64  `json:"cost_nanos"`
}

type BudgetError struct{ Exhausted bool }

func (e *BudgetError) Error() string {
	if e.Exhausted {
		return "project budget exhausted"
	}
	return "project ledger unavailable"
}

type Tracker struct{ client *redis.Client }

func NewTracker(client *redis.Client) *Tracker { return &Tracker{client: client} }

func prefix(project string) string {
	h := sha256.Sum256([]byte(project))
	return "ledger:{" + hex.EncodeToString(h[:]) + "}:"
}

func (t *Tracker) Reserve(ctx context.Context, project, id string, tokens, cost int64) (Reservation, error) {
	if project == "" || id == "" || tokens <= 0 || cost < 0 || tokens > money.MaxExact || cost > money.MaxExact {
		return Reservation{}, fmt.Errorf("invalid reservation")
	}
	now, err := t.client.Time(ctx).Result()
	if err != nil {
		return Reservation{}, &BudgetError{}
	}
	r := Reservation{ID: id, Project: project, Day: now.UTC().Format("2006-01-02"), Tokens: tokens, CostNanos: cost}
	data, _ := json.Marshal(r)
	p := prefix(project)
	result, err := reserveScript.Run(ctx, t.client, []string{p + "limits", p + "day:" + r.Day, p + "pending", p + "events"}, id, string(data), tokens, cost, DefaultTokenLimit, DefaultCostLimit).Int()
	if err != nil {
		return Reservation{}, &BudgetError{}
	}
	if result == 0 {
		return Reservation{}, &BudgetError{Exhausted: true}
	}
	return r, nil
}

func (t *Tracker) SetLimits(ctx context.Context, project string, tokens, cost int64) error {
	if project == "" || tokens < 0 || cost < 0 || tokens > money.MaxExact || cost > money.MaxExact {
		return fmt.Errorf("invalid project limits")
	}
	return t.client.HSet(ctx, prefix(project)+"limits", "tokens", tokens, "cost_nanos", cost).Err()
}

func normalize(e Entry) (Entry, error) {
	if e.APIKey == "" || e.EventID == "" || e.Kind == "" {
		return e, fmt.Errorf("project, event ID and operation kind are required")
	}
	switch e.Kind {
	case "chat", "cache_lookup", "rag_lookup", "rag_import", "cache_evaluation":
	default:
		return e, fmt.Errorf("invalid operation kind")
	}
	if e.CostNanos == 0 && e.CostUSD != 0 {
		var err error
		e.CostNanos, err = money.LegacyUSD(strconv.FormatFloat(e.CostUSD, 'f', -1, 64))
		if err != nil {
			return e, err
		}
	}
	if e.TokensIn < 0 || e.TokensOut < 0 || int64(e.TokensIn) > money.MaxExact || int64(e.TokensOut) > money.MaxExact-int64(e.TokensIn) || e.CostNanos < 0 || e.CostNanos > money.MaxExact {
		return e, fmt.Errorf("invalid usage amounts")
	}
	return e, nil
}

// Record preserves a compatibility seam for already-measured non-provider events.
// Production operations use Reserve and Settle so admission precedes paid work.
func ordinary(e Entry) bool {
	return e.Baseline == nil && e.Replaces == "" && e.Reservation == nil && e.Legacy == nil
}

func (t *Tracker) Record(ctx context.Context, e Entry) error {
	if !ordinary(e) {
		return fmt.Errorf("administrative event metadata is forbidden")
	}
	if e.Kind == "" {
		e.Kind = "chat"
	}
	e.State = "settled"
	e, err := normalize(e)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(e)
	p := prefix(e.APIKey)
	return recordScript.Run(ctx, t.client, []string{p + "events", p + "totals"}, e.EventID, string(data), e.Kind, e.TokensIn, e.TokensOut, e.CostNanos, boolInt(e.Estimated)).Err()
}

// Settle retains the full reservation while an outcome is uncertain.
func (t *Tracker) Settle(ctx context.Context, r Reservation, e Entry, final bool) error {
	if !ordinary(e) {
		return fmt.Errorf("administrative event metadata is forbidden")
	}
	e.APIKey, e.EventID, e.Day = r.Project, r.ID, r.Day
	if final {
		e.State = "settled"
	} else {
		e.State = "uncertain"
		e.Estimated = true
	}
	e, err := normalize(e)
	if err != nil {
		return err
	}
	if int64(e.TokensIn)+int64(e.TokensOut) > r.Tokens || e.CostNanos > r.CostNanos {
		return fmt.Errorf("provider usage exceeds reserved bounds")
	}
	data, _ := json.Marshal(e)
	reserved, _ := json.Marshal(r)
	p := prefix(r.Project)
	return settleScript.Run(ctx, t.client, []string{p + "events", p + "totals", p + "pending", p + "day:" + r.Day}, r.ID, string(data), string(reserved), e.Kind, e.TokensIn, e.TokensOut, e.CostNanos, boolInt(e.Estimated), boolInt(final), r.Tokens, r.CostNanos).Err()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (t *Tracker) Get(ctx context.Context, project string) (*Stats, error) {
	data, err := t.client.HGetAll(ctx, prefix(project)+"totals").Result()
	if err != nil {
		return nil, err
	}
	values := map[string]int64{}
	for _, k := range totalFields {
		raw, exists := data[k]
		if !exists {
			continue
		}
		v, err := decimalCounter(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid ledger total %s", k)
		}
		values[k] = v
	}
	if values["tokens_in"] > money.MaxExact-values["tokens_out"] || values["tokens_in"]+values["tokens_out"] > money.MaxExact-values["embedding_tokens"] || values["cost_nanos"] > money.MaxExact-values["embedding_cost_nanos"] {
		return nil, fmt.Errorf("ledger total overflow")
	}
	return &Stats{Requests: values["requests"], TokensIn: values["tokens_in"], TokensOut: values["tokens_out"], CostUSD: money.USD(values["cost_nanos"]), EstimatedRequests: values["estimated_requests"], EmbeddingRequests: values["embedding_requests"], EmbeddingTokens: values["embedding_tokens"], EmbeddingCostUSD: money.USD(values["embedding_cost_nanos"]), TotalTokens: values["tokens_in"] + values["tokens_out"] + values["embedding_tokens"], TotalCostUSD: money.USD(values["cost_nanos"] + values["embedding_cost_nanos"])}, nil
}

func (t *Tracker) Pending(ctx context.Context, project string) ([]Reservation, error) {
	data, err := t.client.HGetAll(ctx, prefix(project)+"pending").Result()
	if err != nil {
		return nil, err
	}
	var result []Reservation
	for _, raw := range data {
		var r Reservation
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

var totalFields = []string{"requests", "tokens_in", "tokens_out", "cost_nanos", "estimated_requests", "embedding_requests", "embedding_tokens", "embedding_cost_nanos"}

func decimalCounter(raw string) (int64, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty counter")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid counter")
		}
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v > money.MaxExact {
		return 0, fmt.Errorf("counter overflow")
	}
	return v, nil
}
