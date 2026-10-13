// usage-admin manages budget policy and reconciles the persistent ledger.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/auth"
	"github.com/ashrafrah96/llm-gateway/internal/money"
	"github.com/ashrafrah96/llm-gateway/internal/redisconfig"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"github.com/redis/go-redis/v9"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	action := flag.String("action", "status", "status, limits, pending, audit, reconcile, migrate or migrate-keys")
	project := flag.String("project", "", "project identifier")
	tokens := flag.Int64("tokens", usage.DefaultTokenLimit, "daily token limit")
	usd := flag.String("usd", "10", "daily USD limit")
	event := flag.String("event", "", "unresolved reservation ID")
	in, out := flag.Int("input-tokens", -1, "measured input tokens"), flag.Int("output-tokens", -1, "measured output tokens")
	cost := flag.String("cost-usd", "", "measured USD charge")
	kind := flag.String("kind", "", "operation kind if no observation survived")
	evidence := flag.String("evidence", "", "non-secret provider usage/invoice evidence reference")
	repair := flag.Bool("repair", false, "repair aggregate totals during audit")
	flag.Parse()
	if *project == "" {
		return fmt.Errorf("-project is required")
	}
	options, err := redisconfig.Options(os.Getenv, "LEDGER_REDIS_", "localhost:6380")
	if err != nil {
		return err
	}
	client := redis.NewClient(options)
	defer client.Close()
	tracker := usage.NewTracker(client)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result any
	switch *action {
	case "limits":
		amount, err := money.ParseUSD(*usd)
		if err != nil {
			return err
		}
		if err := tracker.SetLimits(ctx, *project, *tokens, amount); err != nil {
			return err
		}
		result = map[string]any{"tokens": *tokens, "usd": *usd}
	case "status":
		result, err = tracker.Get(ctx, *project)
	case "pending":
		result, err = tracker.Pending(ctx, *project)
	case "audit":
		var audit usage.Audit
		audit, err = tracker.AuditTotals(ctx, *project, *repair)
		result = audit
		if err == nil && !audit.Match {
			err = fmt.Errorf("ledger aggregate mismatch")
		}
	case "migrate", "migrate-keys":
		sourceOptions, e := redisconfig.Options(os.Getenv, "REDIS_", "localhost:6379")
		if e != nil {
			return e
		}
		source := redis.NewClient(sourceOptions)
		defer source.Close()
		if *action == "migrate-keys" {
			pepper, e := redisconfig.Secret(os.Getenv, "API_KEY_PEPPER")
			if e != nil {
				return e
			}
			if pepper == "" {
				return fmt.Errorf("API_KEY_PEPPER is required")
			}
			err = auth.NewKeyStore(client, pepper).MigrateLegacy(ctx, source)
		} else {
			err = tracker.Migrate(ctx, source, *project)
		}
		result = map[string]string{"migration": "historical baseline preserved"}
	case "reconcile":
		if *event == "" || *in < 0 || *out < 0 || *cost == "" || *evidence == "" {
			return fmt.Errorf("event, measured tokens, cost and evidence are required")
		}
		amount, e := money.ParseUSD(*cost)
		if e != nil {
			return e
		}
		pending, e := tracker.Pending(ctx, *project)
		if e != nil {
			return e
		}
		found := false
		for _, r := range pending {
			if r.ID == *event {
				found = true
				err = tracker.Resolve(ctx, r, usage.Entry{Kind: *kind, TokensIn: *in, TokensOut: *out, CostNanos: amount}, *evidence)
				break
			}
		}
		if !found {
			return fmt.Errorf("pending reservation not found")
		}
		result = map[string]string{"event": *event, "reconciliation": "recorded"}
	default:
		return fmt.Errorf("unknown action")
	}
	if result != nil {
		if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
			return encodeErr
		}
	}
	return err
}
