package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/money"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentReservationsNeverExceedBudget(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	if err := tr.SetLimits(ctx, project, 100, 1000); err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := tr.Reserve(ctx, project, fmt.Sprintf("r-%d", i), 10, 100)
			if err == nil {
				admitted.Add(1)
			} else {
				var budget *BudgetError
				if !errors.As(err, &budget) || !budget.Exhausted {
					t.Errorf("reserve: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 10 {
		t.Fatalf("admitted %d", admitted.Load())
	}
	pending, err := tr.Pending(ctx, project)
	if err != nil || len(pending) != 10 {
		t.Fatalf("pending: %d %v", len(pending), err)
	}
}

func TestSettlementRetriesAndUnknownOutcome(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	r, err := tr.Reserve(ctx, project, "one", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := tr.Reserve(ctx, project, "one", 100, 1000)
	if err != nil || r != r2 {
		t.Fatalf("reserve retry: %+v %v", r2, err)
	}
	e := Entry{Kind: "chat", TokensIn: 10, TokensOut: 2, CostNanos: 100}
	if err := tr.Settle(ctx, r, e, true); err != nil {
		t.Fatal(err)
	}
	if err := tr.Settle(ctx, r, e, true); err != nil {
		t.Fatal(err)
	}
	e.CostNanos = 101
	if err := tr.Settle(ctx, r, e, true); err == nil {
		t.Fatal("conflicting event accepted")
	}
	got, err := tr.Get(ctx, project)
	if err != nil || got.Requests != 1 || got.TotalTokens != 12 {
		t.Fatalf("stats: %+v %v", got, err)
	}
	r, err = tr.Reserve(ctx, project, "unknown", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	e = Entry{Kind: "chat", TokensIn: 20, CostNanos: 200, Estimated: true}
	if err := tr.Settle(ctx, r, e, false); err != nil {
		t.Fatal(err)
	}
	pending, err := tr.Pending(ctx, project)
	if err != nil || len(pending) != 1 {
		t.Fatalf("unknown released: %v %v", pending, err)
	}
	actual := Entry{Kind: "chat", TokensIn: 30, TokensOut: 1, CostNanos: 350}
	if err := tr.Resolve(ctx, r, actual, "fixture-provider-usage"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Resolve(ctx, r, actual, "fixture-provider-usage"); err != nil {
		t.Fatal(err)
	}
	got, err = tr.Get(ctx, project)
	if err != nil || got.Requests != 2 || got.EstimatedRequests != 0 || got.TotalTokens != 43 || got.TotalCostUSD != money.USD(450) {
		t.Fatalf("resolved stats: %+v %v", got, err)
	}
	audit, err := tr.AuditTotals(ctx, project, false)
	if err != nil || !audit.Match || audit.Events != 3 {
		t.Fatalf("audit: %+v %v", audit, err)
	}
}

func TestNoPartialWritesOnCorruptLedger(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	for _, bad := range []string{"oops", "-1", "9007199254740990.5", "9007199254740992"} {
		cleanProject(tr.client, project)
		tr.client.HSet(ctx, prefix(project)+"totals", "requests", bad)
		err := tr.Record(ctx, Entry{APIKey: project, EventID: "bad", Kind: "chat", TokensIn: 1, CostNanos: 1})
		if err == nil {
			t.Fatalf("accepted corrupt counter %q", bad)
		}
		if n := tr.client.HLen(ctx, prefix(project)+"events").Val(); n != 0 {
			t.Fatalf("partial event written for %q", bad)
		}
		if _, err := tr.Get(ctx, project); err == nil {
			t.Fatalf("Get accepted corrupt counter %q", bad)
		}
	}
	cleanProject(tr.client, project)
	tr.client.Set(ctx, prefix(project)+"events", "wrong type", 0)
	if _, err := tr.Reserve(ctx, project, "bad", 10, 100); err == nil {
		t.Fatal("corrupt reservation accepted")
	}
	if n := tr.client.Exists(ctx, prefix(project)+"pending").Val(); n != 0 {
		t.Fatal("partial reservation written")
	}
}

func TestUTCWindowAndCancelledSettlement(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	now, err := tr.client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	tr.client.HSet(ctx, prefix(project)+"day:"+now.UTC().Add(-24*time.Hour).Format("2006-01-02"), "spent_tokens", DefaultTokenLimit, "spent_nanos", DefaultCostLimit)
	r, err := tr.Reserve(ctx, project, "today", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Day != now.UTC().Format("2006-01-02") {
		t.Fatalf("wrong UTC date %s", r.Day)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	settleCtx, done := context.WithTimeout(context.WithoutCancel(cancelled), time.Second)
	defer done()
	if err := tr.Settle(settleCtx, r, Entry{Kind: "chat", TokensIn: 1, CostNanos: 1}, true); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyMigrationAndAggregateRepair(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	legacy := "usage:" + project
	tr.client.HSet(ctx, legacy, "requests", 7, "tokens_in", 100, "tokens_out", 200, "cost_usd", "0.12345678900000001", "estimated_requests", 2)
	t.Cleanup(func() { tr.client.Del(ctx, legacy) })
	if err := tr.Migrate(ctx, tr.client, project); err != nil {
		t.Fatal(err)
	}
	if err := tr.Migrate(ctx, tr.client, project); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Get(ctx, project)
	if err != nil || got.Requests != 7 || got.TotalTokens != 300 || got.TotalCostUSD != money.USD(123456790) {
		t.Fatalf("baseline: %+v %v", got, err)
	}
	if got := tr.client.HGet(ctx, legacy, "cost_usd").Val(); got != "0.12345678900000001" {
		t.Fatalf("source modified: %s", got)
	}
	tr.client.HSet(ctx, prefix(project)+"totals", "requests", 999)
	audit, err := tr.AuditTotals(ctx, project, false)
	if err != nil || audit.Match {
		t.Fatalf("missed mismatch: %+v %v", audit, err)
	}
	audit, err = tr.AuditTotals(ctx, project, true)
	if err != nil || !audit.Match {
		t.Fatalf("repair: %+v %v", audit, err)
	}
	tr.client.HSet(ctx, legacy, "requests", 8)
	if err := tr.Migrate(ctx, tr.client, project); err == nil {
		t.Fatal("changed baseline accepted")
	}
}

func TestStrictCountersAndAdministrativeMetadata(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	for _, raw := range []string{"", "+1", "-0", "1e2"} {
		cleanProject(tr.client, project)
		if err := tr.client.HSet(ctx, prefix(project)+"totals", "requests", raw).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Get(ctx, project); err == nil {
			t.Fatalf("counter accepted %q", raw)
		}
		audit, err := tr.AuditTotals(ctx, project, false)
		if err != nil || audit.Match {
			t.Fatalf("corruption missed: %+v %v", audit, err)
		}
	}
	cleanProject(tr.client, project)
	for _, e := range []Entry{{Kind: "typo"}, {Kind: "chat", Baseline: map[string]int64{"requests": 1}}, {Kind: "chat", Replaces: "missing"}} {
		e.APIKey, e.EventID = project, "forged"
		if err := tr.Record(ctx, e); err == nil {
			t.Fatalf("forged metadata accepted: %+v", e)
		}
	}
}

func TestAuditRejectsResolutionCycleWithoutRepair(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	for id, target := range map[string]string{"a": "b", "b": "a"} {
		raw, _ := json.Marshal(Entry{APIKey: project, EventID: id, Kind: "chat", State: "uncertain", Replaces: target})
		if err := tr.client.HSet(ctx, prefix(project)+"events", id, raw).Err(); err != nil {
			t.Fatal(err)
		}
	}
	tr.client.HSet(ctx, prefix(project)+"totals", "requests", 2)
	if _, err := tr.AuditTotals(ctx, project, true); err == nil {
		t.Fatal("cyclic history repaired")
	}
	if got := tr.client.HGet(ctx, prefix(project)+"totals", "requests").Val(); got != "2" {
		t.Fatalf("totals modified: %s", got)
	}
}

func TestResolveCrashWithoutObservationAndRejectCorruptObservation(t *testing.T) {
	tr, project := tracker(t)
	ctx := context.Background()
	r, err := tr.Reserve(ctx, project, "crashed", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Resolve(ctx, r, Entry{Kind: "rag_import", TokensIn: 10, CostNanos: 100}, "provider-receipt"); err != nil {
		t.Fatal(err)
	}
	a, err := tr.AuditTotals(ctx, project, false)
	if err != nil || !a.Match || a.Expected["embedding_requests"] != 1 {
		t.Fatalf("crash audit: %+v %v", a, err)
	}
	r, err = tr.Reserve(ctx, project, "corrupt", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Settle(ctx, r, Entry{Kind: "chat", TokensIn: 10, CostNanos: 100}, false); err != nil {
		t.Fatal(err)
	}
	raw := tr.client.HGet(ctx, prefix(project)+"events", r.ID).Val()
	var old Entry
	json.Unmarshal([]byte(raw), &old)
	old.EventID = "wrong-id"
	rawBytes, _ := json.Marshal(old)
	tr.client.HSet(ctx, prefix(project)+"events", r.ID, rawBytes)
	if err := tr.Resolve(ctx, r, Entry{Kind: "chat", TokensIn: 10, CostNanos: 100}, "receipt"); err == nil {
		t.Fatal("corrupt observation resolved")
	}
	if got := tr.client.HLen(ctx, prefix(project)+"pending").Val(); got != 1 {
		t.Fatal("corrupt hold released")
	}
}
