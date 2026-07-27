# Ponytail Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove verified dead artifacts, unused data, and fixed-value configuration without changing gateway behavior.

**Architecture:** Keep the deep cache, provider, completion, and application seams introduced by the architecture refactor. Cut only state and interfaces with no runtime variation or caller, and use the existing behavioral suite rather than adding source-shape tests that would merely prevent future redesign.

**Tech Stack:** Go 1.25, `net/http`, Redis/Redis Search through `go-redis/v9`, Markdown documentation.

## Global Constraints

- Preserve all HTTP routes, headers, status propagation, cache isolation, billing, and shutdown behavior.
- Keep `go-redis/v9`; it is the only direct dependency and has no standard-library replacement for Redis Search.
- Do not add tests that grep source or assert that a symbol is absent.
- Use table-driven tests when multiple inputs exercise one behavior.
- For behavior-preserving deletion steps, run the existing relevant test before and after the cut; absence itself is not a behavior worth testing.
- Run `go test ./... -count=1` and `go build ./...` before completion.
- Commit each independently reviewable simplification.

---

## Audit Decisions

Implement:

1. Delete the completed 571-line architecture execution plan.
2. Remove the unused cached `Prompt` field.
3. Remove unused exported `evaluation.ParseTier`.
4. Remove the unused `fakeProvider.sse` test field.
5. Remove fixed `ListenAddr` from application configuration.
6. Remove impossible nil defense from `Application.Close`.
7. Keep the completion lifecycle namespace local rather than stored.

Retain:

- `ratelimit.MemoryStore`: it is used by limiter, middleware, and handler tests. It also exercises concurrency and sliding-window behavior that a trivial fake would hide. Moving it into a shared “test support” package would preserve almost all lines while making test dependencies less local; duplicating it across three packages would add lines.
- All module interfaces backed by external adapters: cache, provider, recorder, authentication, usage, and rate-limit seams have deterministic test adapters and isolate real network/storage side effects.

---

## File Structure

- Delete `docs/superpowers/plans/2026-07-27-deepen-architecture.md`: completed execution artifact already preserved by Git history and PR #4.
- Modify `internal/cache/semantic.go`: cache response/status only; stop storing unused prompt text.
- Modify `internal/cache/semantic_test.go`: keep search-result fixtures compatible with old entries while asserting current behavior.
- Modify `internal/evaluation/evaluation.go`: remove unused `ParseTier` and its sole `strings` import.
- Modify `internal/completion/completion_test.go`: remove dead fake state.
- Modify `internal/application/application.go`: keep listen address internal and simplify owned-resource close.
- Modify `internal/application/application_test.go`: configuration tables cover only configurable values.
- Modify `internal/completion/completion.go`: construct namespace as a local in lifecycle startup.
- Modify `README.md` and `docs/ARCHITECTURE.md` only if they claim the listen address is configurable or cached prompts are retained.

---

### Task 1: Remove Dead Artifacts, Data, and Symbols

**Files:**
- Delete: `docs/superpowers/plans/2026-07-27-deepen-architecture.md`
- Modify: `internal/cache/semantic.go`
- Modify: `internal/cache/semantic_test.go`
- Modify: `internal/evaluation/evaluation.go`
- Modify: `internal/completion/completion_test.go`

**Interfaces:**
- `cache.CacheEntry` becomes:

```go
type CacheEntry struct {
    Response []byte `json:"response"`
    Status   int    `json:"status"`
}
```

- Existing Redis entries containing `"prompt"` remain readable because `encoding/json` ignores unknown object fields.
- `evaluation.ParseTier(string) Tier` is removed; it has no callers.

- [ ] **Step 1: Run the current behavioral baseline**

Run:

```powershell
go test ./internal/cache ./internal/evaluation ./internal/completion -count=1
```

Expected: all three packages pass.

- [ ] **Step 2: Remove unused cached prompt data**

Change `CacheEntry` and cache storage from:

```go
type CacheEntry struct {
    Prompt   string `json:"prompt"`
    Response []byte `json:"response"`
    Status   int    `json:"status"`
}

entry := CacheEntry{
    Prompt:   a.prompt,
    Response: response,
    Status:   status,
}
```

to:

```go
type CacheEntry struct {
    Response []byte `json:"response"`
    Status   int    `json:"status"`
}

entry := CacheEntry{
    Response: response,
    Status:   status,
}
```

Keep the existing parse fixture containing `"prompt":"p"` in
`TestParseSearchResult`; its passing result proves old cache JSON remains compatible.

- [ ] **Step 3: Remove the unused evaluation conversion**

Delete:

```go
func ParseTier(value string) Tier {
    return Tier(strings.ToLower(value))
}
```

Then remove `"strings"` from `internal/evaluation/evaluation.go` imports. Do not replace
it with another conversion helper because every current tier originates from typed
corpus JSON or `tierForModel`.

- [ ] **Step 4: Remove dead fake state**

Delete this unused field from `fakeProvider`:

```go
sse string
```

Keep `events []ProviderEvent`; it is the semantic stream fixture used by tests.

- [ ] **Step 5: Delete the completed execution plan**

Delete:

```text
docs/superpowers/plans/2026-07-27-deepen-architecture.md
```

Do not delete this Ponytail plan; it remains the active execution record until the
simplification work is complete.

- [ ] **Step 6: Run focused verification**

Run:

```powershell
gofmt -w internal/cache/semantic.go internal/cache/semantic_test.go internal/evaluation/evaluation.go internal/completion/completion_test.go
go test ./internal/cache ./internal/evaluation ./internal/completion -count=1
```

Expected: all packages pass, including `TestParseSearchResult` with legacy prompt data.

- [ ] **Step 7: Commit**

```powershell
git add docs/superpowers/plans internal/cache/semantic.go internal/cache/semantic_test.go internal/evaluation/evaluation.go internal/completion/completion_test.go
git commit -m "chore: remove dead architecture artifacts"
```

---

### Task 2: Remove Fixed Listen Configuration

**Files:**
- Modify: `internal/application/application.go`
- Modify: `internal/application/application_test.go`
- Inspect: `README.md`
- Inspect: `docs/ARCHITECTURE.md`

**Interfaces:**
- `application.Config` becomes:

```go
type Config struct {
    OpenAIAPIKey string
    RedisAddr    string
    CacheTTL     time.Duration
}
```

- `Application.Server.Addr` remains exactly `":8080"` through the private
  `defaultListenAddr` constant.
- Environment behavior remains unchanged: only `OPENAI_API_KEY`, `REDIS_ADDR`, and
  `CACHE_TTL` are configurable.

- [ ] **Step 1: Rewrite the configuration table to the desired interface**

Remove `ListenAddr: ":8080"` from the `defaults`, `explicit values`, and `valid`
fixtures. Remove the `"missing listen address"` invalid-configuration row.

The desired defaults fixture is:

```go
want: Config{
    OpenAIAPIKey: "sk-test",
    RedisAddr:    "localhost:6379",
    CacheTTL:     24 * time.Hour,
},
```

- [ ] **Step 2: Run the table and verify red**

Run:

```powershell
go test ./internal/application -run TestLoadConfig -count=1
```

Expected: FAIL because `LoadConfig` still returns `ListenAddr: ":8080"` while the
desired fixture leaves the field empty.

- [ ] **Step 3: Remove the fixed field**

Remove `ListenAddr` from `Config`, remove its initialization in `LoadConfig`, and change
server construction from:

```go
Addr: cfg.ListenAddr,
```

to:

```go
Addr: defaultListenAddr,
```

Delete this unreachable validation branch:

```go
case cfg.ListenAddr == "":
    return fmt.Errorf("listen address is required")
```

- [ ] **Step 4: Simplify resource close**

Replace:

```go
func (a *Application) Close() error {
    if a == nil || a.redis == nil {
        return nil
    }
    return a.redis.Close()
}
```

with:

```go
func (a *Application) Close() error {
    return a.redis.Close()
}
```

`Application` is constructed only after `redisClient` exists, and `main` calls `Close`
only after `New` succeeds.

- [ ] **Step 5: Run focused verification**

Run:

```powershell
gofmt -w internal/application/application.go internal/application/application_test.go
go test ./internal/application . -count=1
```

Expected: application tests and the root package pass.

- [ ] **Step 6: Check documentation**

Run:

```powershell
rg -n "listen address|ListenAddr|PORT|:8080" README.md docs -g "*.md"
```

Expected: `:8080` may remain documented as the fixed listen address. No document should
claim it is configurable. If a configurable claim exists, rewrite it to say the gateway
listens on fixed port 8080.

- [ ] **Step 7: Commit**

```powershell
git add internal/application README.md docs/ARCHITECTURE.md
git commit -m "refactor: keep listen address internal"
```

---

### Task 3: Remove Ephemeral Lifecycle State

**Files:**
- Modify: `internal/completion/completion.go`
- Test: `internal/completion/completion_test.go`
- Test: `internal/completion/stream_test.go`

**Interfaces:**
- No exported interface changes.
- `lifecycle` no longer stores `namespace cache.Namespace`; namespace construction stays
  inside `Completion.begin`.

- [ ] **Step 1: Run lifecycle behavior before the structural cut**

Run:

```powershell
go test ./internal/completion -count=1
```

Expected: all completion tests pass.

- [ ] **Step 2: Keep namespace local**

Change:

```go
type lifecycle struct {
    completion *Completion
    ctx        context.Context
    req        Request
    model      router.Model
    namespace  cache.Namespace
    attempt    cache.Attempt
    started    time.Time
}
```

to:

```go
type lifecycle struct {
    completion *Completion
    ctx        context.Context
    req        Request
    model      router.Model
    attempt    cache.Attempt
    started    time.Time
}
```

Then construct the attempt with a local:

```go
namespace := cache.NewNamespace(req.APIKey, model.ID)
attempt, err := c.cache.Begin(ctx, namespace, req.Prompt)
```

- [ ] **Step 3: Run focused verification**

Run:

```powershell
gofmt -w internal/completion/completion.go
go test ./internal/completion ./internal/handler -count=1
```

Expected: completion and HTTP adapter tests pass.

- [ ] **Step 4: Commit**

```powershell
git add internal/completion/completion.go
git commit -m "refactor: drop ephemeral lifecycle state"
```

---

### Task 4: Full Verification and Updated Ponytail Accounting

**Files:**
- Inspect: all files changed by Tasks 1-3
- Modify only if stale: `README.md`, `docs/ARCHITECTURE.md`

**Interfaces:**
- No additional interfaces.
- Final accounting reports actual diff lines rather than the audit estimate.

- [ ] **Step 1: Format all changed Go files**

Run:

```powershell
gofmt -w internal/application internal/cache internal/completion internal/evaluation
```

- [ ] **Step 2: Run the full test suite**

Run:

```powershell
go test ./... -count=1
```

Expected: every project package passes.

- [ ] **Step 3: Build every package**

Run:

```powershell
go build ./...
```

Expected: exit code 0 with no compiler errors.

- [ ] **Step 4: Check the diff**

Run:

```powershell
git diff --check
git diff --stat origin/main...HEAD
git status --short
```

Expected: no whitespace errors and no unrelated files.

- [ ] **Step 5: Report actual reduction**

Run:

```powershell
git diff --numstat HEAD~3..HEAD
```

Sum additions and deletions for the three simplification commits. Report the net line
change and confirm dependency count remains unchanged.

---

## Self-Review

- Spec coverage: seven safe Ponytail cuts are scheduled; dependency removal is rejected
  with evidence; `MemoryStore` retention is explicit rather than forgotten.
- Test strategy: configuration changes get a real red/green cycle; deletion-only changes
  use existing behavioral tests and do not add source-shape assertions.
- Type consistency: `Config`, `CacheEntry`, and `lifecycle` desired shapes are defined
  once and match every downstream step.
- Documentation: the completed architecture plan is deleted; user-facing docs are
  changed only if the audit finds a stale behavioral claim.
