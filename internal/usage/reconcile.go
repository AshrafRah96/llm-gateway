package usage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/money"
	"github.com/redis/go-redis/v9"
	"strconv"
)

func contribution(e Entry) (map[string]int64, error) {
	v := map[string]int64{}
	if e.Baseline != nil {
		if e.Kind != "legacy_baseline" || e.EventID != "legacy-baseline" || e.State != "settled" || e.Estimated || e.Replaces != "" || e.Reservation != nil || len(e.Legacy) == 0 || e.Evidence != legacyDigest(e.Legacy) {
			return nil, fmt.Errorf("invalid baseline identity")
		}
		allowed := map[string]bool{}
		for _, f := range totalFields {
			allowed[f] = true
		}
		for f := range e.Baseline {
			if !allowed[f] {
				return nil, fmt.Errorf("unknown baseline field")
			}
		}
		if e.Baseline["estimated_requests"] > e.Baseline["requests"] || e.Baseline["embedding_requests"] != 0 || e.Baseline["embedding_tokens"] != 0 || e.Baseline["embedding_cost_nanos"] != 0 {
			return nil, fmt.Errorf("invalid legacy counts")
		}
		for _, f := range []string{"requests", "tokens_in", "tokens_out", "estimated_requests"} {
			raw, ok := e.Legacy[f]
			var n int64
			if ok {
				var err error
				n, err = decimalCounter(raw)
				if err != nil {
					return nil, err
				}
			}
			if n != e.Baseline[f] {
				return nil, fmt.Errorf("baseline differs from legacy")
			}
		}
		cost := e.Legacy["cost_usd"]
		if cost == "" {
			cost = "0"
		}
		n, err := money.LegacyUSD(cost)
		if err != nil || n != e.Baseline["cost_nanos"] {
			return nil, fmt.Errorf("baseline cost differs from legacy")
		}
		for _, f := range totalFields {
			n := e.Baseline[f]
			if n < 0 || n > money.MaxExact {
				return nil, fmt.Errorf("invalid baseline")
			}
			v[f] = n
		}
	} else {
		var err error
		e, err = normalize(e)
		if err != nil {
			return nil, err
		}
		if e.Kind == "chat" {
			v["requests"] = 1
			v["tokens_in"] = int64(e.TokensIn)
			v["tokens_out"] = int64(e.TokensOut)
			v["cost_nanos"] = e.CostNanos
			if e.Estimated {
				v["estimated_requests"] = 1
			}
		} else {
			v["embedding_requests"] = 1
			v["embedding_tokens"] = int64(e.TokensIn + e.TokensOut)
			v["embedding_cost_nanos"] = e.CostNanos
		}
	}
	if v["tokens_in"] > money.MaxExact-v["tokens_out"] || v["tokens_in"]+v["tokens_out"] > money.MaxExact-v["embedding_tokens"] || v["cost_nanos"] > money.MaxExact-v["embedding_cost_nanos"] {
		return nil, fmt.Errorf("contribution overflow")
	}
	return v, nil
}

// Resolve appends measured evidence; the uncertain observation remains immutable.
func (t *Tracker) Resolve(ctx context.Context, r Reservation, e Entry, evidence string) error {
	if evidence == "" || len(evidence) > 256 {
		return fmt.Errorf("bounded evidence reference is required")
	}
	p := prefix(r.Project)
	oldRaw, err := t.client.HGet(ctx, p+"events", r.ID).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	oldValues := map[string]int64{}
	if err == nil {
		var old Entry
		if err = json.Unmarshal([]byte(oldRaw), &old); err != nil {
			return err
		}
		if old.State != "uncertain" || old.APIKey != r.Project || old.EventID != r.ID || old.Day != r.Day || !ordinary(old) || int64(old.TokensIn)+int64(old.TokensOut) > r.Tokens || old.CostNanos > r.CostNanos {
			return fmt.Errorf("only uncertain events can be reconciled")
		}
		if e.Kind != "" && e.Kind != old.Kind {
			return fmt.Errorf("operation kind cannot change during reconciliation")
		}
		e.Kind, e.Model, e.ProviderRequestID = old.Kind, old.Model, old.ProviderRequestID
		oldValues, err = contribution(old)
		if err != nil {
			return err
		}
	}
	e.APIKey, e.EventID, e.Day, e.State, e.Estimated = r.Project, r.ID+":resolution", r.Day, "settled", false
	if !ordinary(e) {
		return fmt.Errorf("administrative event metadata is forbidden")
	}
	e.Replaces, e.Evidence, e.Reservation = r.ID, evidence, &r
	e, err = normalize(e)
	if err != nil {
		return err
	}
	if int64(e.TokensIn)+int64(e.TokensOut) > r.Tokens || e.CostNanos > r.CostNanos {
		return fmt.Errorf("reconciled usage exceeds reservation; investigate provider policy")
	}
	newValues, err := contribution(e)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(e)
	reservation, _ := json.Marshal(r)
	oldJSON, _ := json.Marshal(oldValues)
	newJSON, _ := json.Marshal(newValues)
	return transformScript.Run(ctx, t.client, []string{p + "events", p + "totals", p + "pending", p + "day:" + r.Day, p + "migration"}, "resolve", e.EventID, string(raw), r.ID, oldRaw, string(reservation), string(oldJSON), string(newJSON), r.Tokens, r.CostNanos, int64(e.TokensIn+e.TokensOut), e.CostNanos, evidence).Err()
}

func legacyDigest(data map[string]string) string {
	raw, _ := json.Marshal(data)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Migrate preserves legacy totals and their original decimal representation in an audit event.
// Stop old writers first. Historical usage conservatively consumes the opening day's budget.
func (t *Tracker) Migrate(ctx context.Context, source *redis.Client, project string) error {
	if project == "" {
		return fmt.Errorf("project required")
	}
	data, err := source.HGetAll(ctx, "usage:"+project).Result()
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("no legacy usage for project")
	}
	baseline := map[string]int64{}
	for _, f := range []string{"requests", "tokens_in", "tokens_out", "estimated_requests"} {
		raw, exists := data[f]
		if !exists {
			continue
		}
		v, err := decimalCounter(raw)
		if err != nil {
			return fmt.Errorf("invalid legacy %s", f)
		}
		baseline[f] = v
	}
	if baseline["estimated_requests"] > baseline["requests"] {
		return fmt.Errorf("invalid legacy estimated request count")
	}
	value := data["cost_usd"]
	if value == "" {
		value = "0"
	}
	cost, err := money.LegacyUSD(value)
	if err != nil {
		return err
	}
	baseline["cost_nanos"] = cost
	now, err := t.client.Time(ctx).Result()
	if err != nil {
		return err
	}
	day := now.UTC().Format("2006-01-02")
	e := Entry{APIKey: project, EventID: "legacy-baseline", Kind: "legacy_baseline", Day: day, State: "settled", Baseline: baseline, Evidence: legacyDigest(data), Legacy: data}
	// Replays must preserve the first migration's day and bytes rather than manufacture a new event.
	p := prefix(project)
	existing, err := t.client.HGet(ctx, p+"events", e.EventID).Result()
	if err == nil {
		var previous Entry
		if json.Unmarshal([]byte(existing), &previous) != nil || previous.Evidence != e.Evidence {
			return fmt.Errorf("legacy baseline changed")
		}
		return nil
	}
	if err != redis.Nil {
		return err
	}
	values, err := contribution(e)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(e)
	newJSON, _ := json.Marshal(values)
	return transformScript.Run(ctx, t.client, []string{p + "events", p + "totals", p + "pending", p + "day:" + day, p + "migration"}, "migrate", e.EventID, string(raw), "", "", "", "{}", string(newJSON), 0, 0, values["tokens_in"]+values["tokens_out"], cost, e.Evidence).Err()
}

// CheckLegacy makes an unmigrated historical balance a startup error, never a zero balance.
func (t *Tracker) CheckLegacy(ctx context.Context, source *redis.Client) error {
	iter := source.Scan(ctx, 0, "usage:*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		project := key[len("usage:"):]
		data, err := source.HGetAll(ctx, key).Result()
		if err != nil {
			return err
		}
		digest, err := t.client.HGet(ctx, prefix(project)+"migration", "source_digest").Result()
		if err != nil || digest != legacyDigest(data) {
			return fmt.Errorf("legacy usage for project requires audited migration before startup")
		}
	}
	return iter.Err()
}

type Audit struct {
	Expected map[string]int64  `json:"expected"`
	Actual   map[string]string `json:"actual"`
	Match    bool              `json:"match"`
	Events   int               `json:"events"`
}

// AuditTotals replays immutable events. Repair compares every snapshot entry atomically.
func (t *Tracker) AuditTotals(ctx context.Context, project string, repair bool) (Audit, error) {
	p := prefix(project)
	snapshot, err := t.client.HGetAll(ctx, p+"events").Result()
	if err != nil {
		return Audit{}, err
	}
	events := map[string]Entry{}
	replaced := map[string]bool{}
	for id, raw := range snapshot {
		var e Entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return Audit{}, err
		}
		if e.EventID != id || e.APIKey != project {
			return Audit{}, fmt.Errorf("invalid event identity")
		}
		events[id] = e
		if e.Replaces != "" {
			if replaced[e.Replaces] {
				return Audit{}, fmt.Errorf("duplicate event resolution")
			}
			replaced[e.Replaces] = true
		}
	}
	expected := map[string]int64{}
	for _, f := range totalFields {
		expected[f] = 0
	}
	// Validate every node before filtering replaced observations; cycles cannot erase totals.
	valuesByID := map[string]map[string]int64{}
	for id, e := range events {
		if e.State != "settled" && e.State != "uncertain" {
			return Audit{}, fmt.Errorf("invalid event state")
		}
		values, err := contribution(e)
		if err != nil {
			return Audit{}, err
		}
		valuesByID[id] = values
		if e.Replaces == "" {
			if e.Reservation != nil {
				return Audit{}, fmt.Errorf("unexpected reservation metadata")
			}
			continue
		}
		r := e.Reservation
		if r == nil || e.State != "settled" || e.Estimated || e.Baseline != nil || id != e.Replaces+":resolution" || r.ID != e.Replaces || r.Project != project || r.Day != e.Day || r.Tokens <= 0 || r.Tokens > money.MaxExact || r.CostNanos < 0 || r.CostNanos > money.MaxExact || int64(e.TokensIn)+int64(e.TokensOut) > r.Tokens || e.CostNanos > r.CostNanos || e.Evidence == "" || len(e.Evidence) > 256 || replaced[id] {
			return Audit{}, fmt.Errorf("invalid resolution reference")
		}
		if original, ok := events[e.Replaces]; ok {
			if original.State != "uncertain" || !ordinary(original) || original.Day != r.Day || original.Kind != e.Kind || int64(original.TokensIn)+int64(original.TokensOut) > r.Tokens || original.CostNanos > r.CostNanos {
				return Audit{}, fmt.Errorf("invalid original observation")
			}
		}
	}
	for id, values := range valuesByID {
		if replaced[id] {
			continue
		}
		for _, f := range totalFields {
			if values[f] > money.MaxExact-expected[f] {
				return Audit{}, fmt.Errorf("audit overflow")
			}
			expected[f] += values[f]
		}
	}
	actual, err := t.client.HGetAll(ctx, p+"totals").Result()
	if err != nil {
		return Audit{}, err
	}
	match := true
	for _, f := range totalFields {
		value, exists := actual[f]
		if !exists {
			value = "0"
		}
		parsed, parseErr := decimalCounter(value)
		if parseErr != nil || parsed != expected[f] {
			match = false
		}
	}
	if repair {
		snap, _ := json.Marshal(snapshot)
		values, _ := json.Marshal(expected)
		if err := rebuildScript.Run(ctx, t.client, []string{p + "events", p + "totals"}, string(snap), string(values)).Err(); err != nil {
			return Audit{}, err
		}
		actual = map[string]string{}
		for _, f := range totalFields {
			actual[f] = strconv.FormatInt(expected[f], 10)
		}
		match = true
	}
	return Audit{Expected: expected, Actual: actual, Match: match, Events: len(events)}, nil
}

var transformScript = redis.NewScript(luaChecks + `
for _,k in ipairs(KEYS) do hash(k) end
local prior=redis.call('HGET',KEYS[1],ARGV[2])
if prior then if prior~=ARGV[3] then error('conflicting evidence event') end;return 0 end
local before=cjson.decode(ARGV[7]);local after=cjson.decode(ARGV[8]);local v={}
for _,f in ipairs(fields) do
  local old=integer(before[f] or 0);local new=integer(after[f] or 0);local current=get(KEYS[2],f)
  if current<old then error('invalid aggregate before reconciliation') end
  v[f]=add(current-old,new)
end
add(add(v.tokens_in,v.tokens_out),v.embedding_tokens);add(v.cost_nanos,v.embedding_cost_nanos)
local st=get(KEYS[4],'spent_tokens');local sc=get(KEYS[4],'spent_nanos')
local rt=get(KEYS[4],'reserved_tokens');local rc=get(KEYS[4],'reserved_nanos')
local nt=add(st,integer(ARGV[11]));local nc=add(sc,integer(ARGV[12]))
if ARGV[1]=='resolve' then
  if redis.call('HGET',KEYS[3],ARGV[4])~=ARGV[6] then error('reservation changed') end
  local original=redis.call('HGET',KEYS[1],ARGV[4]) or ''
  if original~=ARGV[5] then error('observation changed') end
  local reservedT=integer(ARGV[9]);local reservedC=integer(ARGV[10])
  if rt<reservedT or rc<reservedC then error('invalid reservation totals') end
  rt=rt-reservedT;rc=rc-reservedC
elseif ARGV[1]~='migrate' then error('invalid transformation') end
redis.call('HSET',KEYS[1],ARGV[2],ARGV[3]);write_totals(KEYS[2],v)
redis.call('HSET',KEYS[4],'reserved_tokens',string.format('%.0f',rt),'reserved_nanos',string.format('%.0f',rc),'spent_tokens',string.format('%.0f',nt),'spent_nanos',string.format('%.0f',nc))
if ARGV[1]=='resolve' then redis.call('HDEL',KEYS[3],ARGV[4]) else redis.call('HSET',KEYS[5],'source_digest',ARGV[13]) end
return 1
`)

var rebuildScript = redis.NewScript(luaChecks + `
hash(KEYS[1]);hash(KEYS[2])
local snapshot=cjson.decode(ARGV[1]);local expected=cjson.decode(ARGV[2]);local count=0
for id,raw in pairs(snapshot) do count=count+1;if redis.call('HGET',KEYS[1],id)~=raw then error('ledger changed during audit') end end
if redis.call('HLEN',KEYS[1])~=count then error('ledger changed during audit') end
local v={};for _,f in ipairs(fields) do v[f]=integer(expected[f] or 0) end
add(add(v.tokens_in,v.tokens_out),v.embedding_tokens);add(v.cost_nanos,v.embedding_cost_nanos)
write_totals(KEYS[2],v);return 1
`)
