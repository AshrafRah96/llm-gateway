# Deepen Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deepen the semantic-cache, provider-streaming, completion-lifecycle, and application-bootstrap modules in sequence, with a verified working repository after every stage.

**Architecture:** A request-local semantic-cache attempt retains one embedding across lookup and store. The OpenAI adapter translates SSE into provider-neutral events, a private completion lifecycle owns shared request settlement, and a concrete application module owns configuration and adapter composition while `main` retains process lifecycle.

**Tech Stack:** Go 1.25, `net/http`, Redis/Redis Search through `go-redis/v9`, table-driven Go tests, Markdown documentation.

## Global Constraints

- Use strict red-green-refactor: no production change before its failing test.
- Prefer table-driven tests whenever cases exercise the same behavior; do not create a separate test function per input.
- Run the affected package tests after green, then `go test ./...` before moving to the next task.
- Preserve ADR-0007 tenant/model/schema filtering and route-before-lookup.
- Preserve ADR-0006 bounded detached settlement, labelled estimates, and the rule that truncated streams are never cached.
- Do not add speculative constructor interfaces or factory seams.
- Rewrite documentation that describes superseded interfaces or ownership.
- Commit each verified task independently.

---

## File Structure

- `internal/cache/semantic.go`: request-local cache attempt; embedding, Redis search, and Redis store stay local.
- `internal/cache/semantic_test.go`: table-driven attempt validation and one-embedding regression.
- `internal/cache/semantic_integration_test.go`: Redis contract through cache attempts.
- `internal/completion/completion.go`: cache-attempt interface and private request lifecycle.
- `internal/completion/stream.go`: provider-neutral stream consumption and settlement.
- `internal/completion/completion_test.go`: shared fakes and table-driven completion cases.
- `internal/completion/stream_test.go`: table-driven stream event, settlement, and failure cases.
- `internal/provider/openai.go`: OpenAI SSE decoding adapter.
- `internal/provider/openai_test.go`: table-driven OpenAI wire-format cases.
- `internal/handler/stream.go`: provider-neutral completion chunks to HTTP SSE.
- `internal/handler/chat_test.go`: HTTP SSE contract tables.
- `internal/evaluation/evaluation.go`: evaluation through cache attempts.
- `internal/evaluation/evaluation_test.go`: cache-attempt fake.
- `internal/application/application.go`: concrete configuration, adapter graph, and owned resources.
- `internal/application/application_test.go`: table-driven configuration and startup validation.
- `main.go`: process signals, listen, shutdown, and application cleanup only.
- `README.md`, `docs/ARCHITECTURE.md`, relevant ADRs: rewrite outdated ownership and flow descriptions.

---

### Task 1: Reuse One Embedding Per Semantic-Cache Attempt

**Files:**
- Modify: `internal/cache/semantic.go`
- Modify: `internal/cache/semantic_test.go`
- Modify: `internal/cache/semantic_integration_test.go`
- Modify: `internal/completion/completion.go`
- Modify: `internal/completion/completion_test.go`
- Modify: `internal/completion/stream_test.go`
- Modify: `internal/handler/chat_test.go`
- Modify: `internal/evaluation/evaluation.go`
- Modify: `internal/evaluation/evaluation_test.go`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/adr/0002-cache-on-meaning-not-exact-text.md`

**Interfaces:**
- Produces:

```go
// internal/cache
type Attempt interface {
    Get(ctx context.Context) (*CacheEntry, error)
    Set(ctx context.Context, response []byte, status int) error
}

func (c *SemanticCache) Begin(
    ctx context.Context,
    ns Namespace,
    prompt string,
) (Attempt, error)
```

- `Begin` embeds and validates exactly once. Its request-local implementation retains only namespace, prompt, and encoded vector; it does not retain a context.
- Completion and evaluation consume `Begin`; direct `Get` and `Set` are removed rather than layered over the new interface.

- [ ] **Step 1: Write the failing one-embedding table test**

Add a counting embedder and a table covering a lookup-only attempt and a lookup-then-store attempt:

```go
type countingEmbedder struct {
    vector []float32
    calls  int
}

func (e *countingEmbedder) Embed(context.Context, string) ([]float32, error) {
    e.calls++
    return e.vector, nil
}

func TestSemanticCacheAttemptEmbedsOnce(t *testing.T) {
    tests := []struct {
        name  string
        store bool
    }{
        {name: "lookup only"},
        {name: "lookup then store", store: true},
    }
    // Use a Redis client pointed at 127.0.0.1:0; Begin succeeds before Redis.
    // Ignore the expected Get/Set Redis error and assert embedder.calls == 1.
}
```

This catches the realistic mutation “`Set` calls `Embed` again.”

- [ ] **Step 2: Run the test and verify red**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/cache -run TestSemanticCacheAttemptEmbedsOnce -count=1
```

Expected: compile failure because `SemanticCache.Begin` does not exist.

- [ ] **Step 3: Implement the minimal request-local attempt**

Move embedding and dimension validation into `Begin`. Move Redis search and result parsing into `semanticAttempt.Get`; move marshal/key/pipeline/TTL work into `semanticAttempt.Set`. Keep all namespace fields and query escaping unchanged.

- [ ] **Step 4: Migrate callers and fakes**

Use this caller-facing shape:

```go
type Cache interface {
    Begin(context.Context, cache.Namespace, string) (cache.Attempt, error)
}
```

Completion treats a `Begin` or `Get` failure as a miss and stores only when it has a valid attempt. Evaluation begins one attempt for seeding and a separate attempt for each query, because those prompts intentionally require different embeddings.

- [ ] **Step 5: Convert aligned cache tests to tables**

Combine wrong-dimension cases and namespace-isolation cases into tables. Preserve integration assertions for raw-key secrecy, TTL, malformed entries, and legacy-key isolation.

- [ ] **Step 6: Run affected tests and verify green**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/cache ./internal/completion ./internal/evaluation ./internal/handler -count=1
```

Expected: all affected packages pass.

- [ ] **Step 7: Rewrite outdated cache documentation**

Rewrite `docs/ARCHITECTURE.md` to say callers begin a cache attempt and the implementation reuses one embedding across lookup/store. Rewrite ADR-0002’s consequence that currently says every lookup costs an embedding so it also states a cacheable miss does not embed twice.

- [ ] **Step 8: Verify the full repository**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./... -count=1
```

Expected: all packages pass; Redis Search tests may skip only when Redis is unavailable.

- [ ] **Step 9: Commit**

```powershell
git add internal/cache internal/completion internal/evaluation internal/handler docs/ARCHITECTURE.md docs/adr/0002-cache-on-meaning-not-exact-text.md
git commit -m "refactor: reuse semantic cache embeddings"
```

---

### Task 2: Move OpenAI Streaming Wire Knowledge Into the Provider Adapter

**Files:**
- Modify: `internal/completion/completion.go`
- Modify: `internal/completion/stream.go`
- Modify: `internal/completion/completion_test.go`
- Modify: `internal/completion/stream_test.go`
- Modify: `internal/provider/openai.go`
- Modify: `internal/provider/openai_test.go`
- Modify: `internal/handler/stream.go`
- Modify: `internal/handler/chat_test.go`
- Modify: `internal/handler/abandoned_test.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/adr/0005-one-module-for-a-chat-request.md`

**Interfaces:**
- Produces:

```go
// internal/completion
type ProviderUsage struct {
    PromptTokens     int
    CompletionTokens int
}

type ProviderEvent struct {
    Content string
    Usage   *ProviderUsage
    Done    bool
}

type ProviderStream interface {
    Next() (ProviderEvent, bool)
    Err() error
    Close() error
}

type StreamChunk struct {
    Content string
    Done    bool
}
```

- `Provider.Stream` returns `ProviderStream`; OpenAI’s adapter imports the completion contracts and owns scanning `data:`, decoding its JSON, recognizing `[DONE]`, and extracting usage.
- `completion.Stream.Next` returns `StreamChunk`. The HTTP handler owns JSON/SSE encoding.

- [ ] **Step 1: Write failing table-driven provider decoding tests**

Add a table with literal SSE fixtures:

```go
tests := []struct {
    name       string
    body       string
    wantEvents []completion.ProviderEvent
    wantErr    bool
}{
    {
        name: "content usage and done",
        body: "data: {\"choices\":[{\"delta\":{\"content\":\"Paris\"}}]}\n\n" +
              "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\n" +
              "data: [DONE]\n\n",
        wantEvents: []completion.ProviderEvent{
            {Content: "Paris"},
            {Usage: &completion.ProviderUsage{PromptTokens: 10, CompletionTokens: 20}},
            {Done: true},
        },
    },
    {name: "ignores non data lines", body: ": keepalive\n\ndata: [DONE]\n\n", wantEvents: []completion.ProviderEvent{{Done: true}}},
    {name: "malformed json reports read error", body: "data: {broken}\n\n", wantErr: true},
}
```

This catches the mutation “raw OpenAI frames escape the provider adapter.”

- [ ] **Step 2: Run provider tests and verify red**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/provider -run TestStreamDecodesEvents -count=1
```

Expected: compile failure because provider-neutral stream contracts are not implemented.

- [ ] **Step 3: Implement the OpenAI stream adapter**

Add a private `openAIStream` with a buffered scanner, `Next`, `Err`, and `Close`. Keep the 1 MiB line limit. `Next` loops over non-data lines, returns content and usage as semantic fields, returns `{Done:true}` for `[DONE]`, and stores malformed JSON as its error.

- [ ] **Step 4: Replace raw-SSE completion parsing**

Delete OpenAI-specific `sseChunk`, scanner, and `absorb` parsing from completion. Consume `ProviderEvent`, accumulate `Content`, absorb `Usage`, and expose only `StreamChunk` to callers. Cache replay returns one content chunk plus a done chunk.

- [ ] **Step 5: Move outbound SSE encoding into the handler**

For each non-terminal `StreamChunk`, encode this stable gateway shape:

```go
map[string]any{
    "choices": []any{
        map[string]any{"delta": map[string]string{"content": chunk.Content}},
    },
}
```

Write `[DONE]` for terminal chunks. Never forward provider usage events.

- [ ] **Step 6: Convert aligned stream tests to tables**

Use tables for provider statuses, provider event sequences, and handler SSE chunks. Keep the real cancellation regression as an integration-style test because replacing it with a fake would hide context cancellation.

- [ ] **Step 7: Run affected tests and verify green**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/provider ./internal/completion ./internal/handler -count=1
```

Expected: all three packages pass.

- [ ] **Step 8: Rewrite outdated streaming documentation**

Rewrite README claims that the gateway “forwards OpenAI’s response.” State that the provider adapter parses OpenAI SSE and the handler emits the gateway’s stable content-delta SSE. Rewrite architecture/ADR text that places OpenAI JSON parsing inside completion.

- [ ] **Step 9: Verify the full repository**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./... -count=1
```

Expected: all packages pass.

- [ ] **Step 10: Commit**

```powershell
git add internal/provider internal/completion internal/handler README.md docs/ARCHITECTURE.md docs/adr/0005-one-module-for-a-chat-request.md
git commit -m "refactor: deepen provider stream adapter"
```

---

### Task 3: Concentrate One Completion Request Lifecycle

**Files:**
- Modify: `internal/completion/completion.go`
- Modify: `internal/completion/stream.go`
- Modify: `internal/completion/completion_test.go`
- Modify: `internal/completion/stream_test.go`
- Modify: `internal/handler/stream.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/adr/0005-one-module-for-a-chat-request.md`
- Modify: `docs/adr/0006-estimate-and-bill-abandoned-streams.md`

**Interfaces:**
- Keeps public `Completion.Complete`, `Completion.Stream`, `Stream.Next`, and `Stream.Close`.
- Produces a private lifecycle:

```go
type lifecycle struct {
    completion *Completion
    ctx        context.Context
    req        Request
    model      router.Model
    namespace  cache.Namespace
    attempt    cache.Attempt
    started    time.Time
    settled    bool
}
```

- `Next` settles exactly once when it observes provider completion or provider exhaustion/error. `Close` settles only if `Next` did not already do so, covering abandonment.

- [ ] **Step 1: Write the failing automatic-settlement table**

Add one table with complete, truncated, and cache-hit streams:

```go
tests := []struct {
    name          string
    events        []ProviderEvent
    cacheHit      bool
    wantRecords   int
    wantStores    int
    wantEstimated bool
}{
    {name: "complete provider stream settles on exhaustion", events: completeEvents, wantRecords: 1, wantStores: 1},
    {name: "truncated provider stream settles estimated", events: truncatedEvents, wantRecords: 1, wantEstimated: true},
    {name: "cache replay logs but does not bill or store", cacheHit: true},
}
```

Drain the stream without calling `Close`, then assert settlement results. This fails against the current close-only behavior.

- [ ] **Step 2: Run completion tests and verify red**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/completion -run TestStreamSettlesWhenExhausted -count=1
```

Expected: usage/store counts remain zero after drain.

- [ ] **Step 3: Introduce the private lifecycle**

Move route, namespace, cache attempt, lookup, logging, metering, and store helpers behind `lifecycle`. Use it from both `Complete` and `Stream`. Do not extract one-line pass-through modules.

- [ ] **Step 4: Make stream settlement automatic and idempotent**

When a done event is returned, settle after returning it on the next `Next` call; when the provider returns no event, settle immediately. Preserve the first provider read error as the result of `Close`. `Close` closes the provider stream and invokes the same guarded settlement path.

- [ ] **Step 5: Add delivery-mode parity tables**

Use a table over buffered/streaming modes for cache hit, successful miss, provider status, metering, and cache eligibility. Assert caller-visible outcomes rather than private lifecycle fields.

- [ ] **Step 6: Run affected tests and verify green**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/completion ./internal/handler -count=1
```

Expected: all tests pass, including cancellation and double-close regressions.

- [ ] **Step 7: Rewrite lifecycle documentation**

Rewrite README’s reviewer guide and architecture/ADR descriptions: provider adapter parses the wire; completion lifecycle accumulates semantic events and settles automatically on exhaustion; `Close` is the abandonment fallback.

- [ ] **Step 8: Verify the full repository**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./... -count=1
```

Expected: all packages pass.

- [ ] **Step 9: Commit**

```powershell
git add internal/completion internal/handler README.md docs/ARCHITECTURE.md docs/adr/0005-one-module-for-a-chat-request.md docs/adr/0006-estimate-and-bill-abandoned-streams.md
git commit -m "refactor: unify completion lifecycle"
```

---

### Task 4: Deepen Concrete Application Bootstrap

**Files:**
- Create: `internal/application/application.go`
- Create: `internal/application/application_test.go`
- Modify: `internal/cache/semantic.go`
- Modify: `internal/cache/semantic_test.go`
- Modify: `internal/cache/semantic_integration_test.go`
- Modify: `cmd/cache-eval/main.go`
- Modify: `main.go`
- Modify: `README.md`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**
- Produces:

```go
type Config struct {
    OpenAIAPIKey string
    RedisAddr    string
    CacheTTL     time.Duration
    ListenAddr   string
}

func LoadConfig(getenv func(string) string) (Config, error)

type Application struct {
    Server *http.Server
    redis  *redis.Client
}

func New(ctx context.Context, cfg Config) (*Application, error)
func (a *Application) Close() error
```

- Changes semantic-cache construction to accept startup context:

```go
func NewSemanticCache(
    ctx context.Context,
    client *redis.Client,
    embedder Embedder,
    ttl time.Duration,
) (*SemanticCache, error)
```

- `main` loads config, calls `application.New`, listens, handles signals, shuts down, and closes owned resources.

- [ ] **Step 1: Write failing table-driven configuration tests**

```go
tests := []struct {
    name    string
    env     map[string]string
    want    Config
    wantErr string
}{
    {
        name: "defaults",
        env: map[string]string{"OPENAI_API_KEY": "sk-test"},
        want: Config{OpenAIAPIKey: "sk-test", RedisAddr: "localhost:6379", CacheTTL: 24 * time.Hour, ListenAddr: ":8080"},
    },
    {name: "explicit values", env: map[string]string{"OPENAI_API_KEY": "sk-test", "REDIS_ADDR": "redis:6380", "CACHE_TTL": "90m"}, want: Config{OpenAIAPIKey: "sk-test", RedisAddr: "redis:6380", CacheTTL: 90 * time.Minute, ListenAddr: ":8080"}},
    {name: "missing key", env: map[string]string{}, wantErr: "OPENAI_API_KEY not set"},
    {name: "invalid ttl", env: map[string]string{"OPENAI_API_KEY": "sk-test", "CACHE_TTL": "zero"}, wantErr: "parse CACHE_TTL"},
}
```

Also add a table proving `New` rejects invalid zero-value configuration before dialing Redis.

- [ ] **Step 2: Run application tests and verify red**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/application -count=1
```

Expected: package or symbols do not exist.

- [ ] **Step 3: Implement configuration and concrete composition**

Move the exact adapter graph from `main` into `application.New`. Preserve Redis RESP2, Ping, cache TTL, one shared `Completion`, middleware order, and `:8080`. Return contextual errors; do not log or exit inside the application module.

- [ ] **Step 4: Make cache startup context-aware**

Pass `ctx` into `NewSemanticCache` and `createIndex`; remove `context.Background` from startup. Distinguish an absent index from an unexpected `FT.INFO` failure only where Redis exposes a reliable error; otherwise preserve the current create-and-return-contextual-error behavior.

- [ ] **Step 5: Reduce main to process lifecycle**

Use `LoadConfig(os.Getenv)`, a ten-second startup context for `application.New`,
signal-driven `Server.Shutdown`, `Server.ListenAndServe`, and deferred
`Application.Close`.

- [ ] **Step 6: Run affected tests and verify green**

Run:

```powershell
$env:GOCACHE="$PWD\tmp\go-build"; go test ./internal/application ./internal/cache ./cmd/cache-eval . -count=1
```

Expected: all affected packages compile and pass.

- [ ] **Step 7: Rewrite startup documentation**

Update README’s settings/layout/reviewer guidance and `docs/ARCHITECTURE.md` to describe the application bootstrap module, configuration defaults, owned Redis resource, and context-bounded startup.

- [ ] **Step 8: Verify formatting, tests, and build**

Run:

```powershell
gofmt -w main.go cmd/cache-eval/main.go internal/application internal/cache internal/completion internal/provider internal/handler internal/evaluation
$env:GOCACHE="$PWD\tmp\go-build"; go test ./... -count=1
$env:GOCACHE="$PWD\tmp\go-build"; go build ./...
```

Expected: formatting changes are stable, all tests pass, and all packages build.

- [ ] **Step 9: Documentation freshness check**

Run:

```powershell
rg -n "raw SSE|forwards OpenAI|Get or set within a namespace|usage should be recorded on Close|Close is what meters|main.go" README.md docs internal -g '*.md' -g '*.go'
```

Expected: no stale claims; remaining matches, if any, describe historical behavior explicitly.

- [ ] **Step 10: Commit**

```powershell
git add main.go cmd/cache-eval internal/application internal/cache README.md docs/ARCHITECTURE.md
git commit -m "refactor: deepen application bootstrap"
```

---

## Final Self-Review

- Spec coverage: all four architecture-report candidates have their own red/green/full-verification gate; table-driven testing and documentation rewrites are explicit global constraints and task steps.
- Placeholder scan: no TBD/TODO/“similar to” steps; every production change has an exact interface and verification command.
- Type consistency: `cache.Attempt`, `ProviderEvent`, `ProviderStream`, `StreamChunk`, `lifecycle`, `Config`, and `Application` are introduced before downstream use.
- Sequencing: cache attempts precede completion changes; provider-neutral streaming precedes lifecycle consolidation; bootstrap lands last and composes the final interfaces.
