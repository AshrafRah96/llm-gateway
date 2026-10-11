package evaluation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/evidence"
	"github.com/ashrafrah96/llm-gateway/internal/router"
)

const (
	PrecisionTarget = 0.95
	RecallTarget    = 0.80
)

type Kind string

const (
	Equivalent Kind = "equivalent"
	Different  Kind = "different"
)

type Tier string

const (
	CheapTier    Tier = "cheap"
	PowerfulTier Tier = "powerful"
)

type Case struct {
	ID           string `json:"id"`
	Kind         Kind   `json:"kind"`
	Source       string `json:"source"`
	Query        string `json:"query"`
	ExpectedTier Tier   `json:"expected_tier"`
}

type Corpus struct {
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}

func DecodeCorpus(r io.Reader) (Corpus, error) {
	var corpus Corpus
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode corpus: %w", err)
	}
	if corpus.Version == "" {
		return Corpus{}, fmt.Errorf("corpus version is required")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Corpus{}, fmt.Errorf("corpus must contain one JSON document")
	}
	return corpus, nil
}

type Cache interface {
	Begin(ctx context.Context, ns cache.Namespace, prompt string) (cache.Attempt, error)
}

type CaseResult struct {
	ID              string  `json:"id"`
	Kind            Kind    `json:"kind"`
	ExpectedTier    Tier    `json:"expected_tier"`
	Model           string  `json:"model"`
	Hit             bool    `json:"hit"`
	Correct         bool    `json:"correct"`
	LookupLatencyMs float64 `json:"lookup_latency_ms"`
}

type Report struct {
	Total                int          `json:"total"`
	TruePositives        int          `json:"true_positives"`
	TrueNegatives        int          `json:"true_negatives"`
	FalsePositives       int          `json:"false_positives"`
	FalseNegatives       int          `json:"false_negatives"`
	Precision            float64      `json:"precision"`
	Recall               float64      `json:"recall"`
	CacheHitRate         float64      `json:"cache_hit_rate"`
	UpstreamCallsAvoided int          `json:"upstream_calls_avoided"`
	LookupP50Ms          float64      `json:"lookup_p50_ms"`
	LookupP95Ms          float64      `json:"lookup_p95_ms"`
	PrecisionTarget      float64      `json:"precision_target"`
	PrecisionTargetMet   bool         `json:"precision_target_met"`
	RecallTarget         float64      `json:"recall_target"`
	RecallTargetMet      bool         `json:"recall_target_met"`
	Cases                []CaseResult `json:"cases"`
}

func Validate(cases []Case) error {
	counts := map[Kind]int{}
	tiers := map[Tier]bool{}
	ids := map[string]bool{}

	for i, c := range cases {
		if c.ID == "" || c.Source == "" || c.Query == "" {
			return fmt.Errorf("case %d requires id, source, and query", i)
		}
		if ids[c.ID] {
			return fmt.Errorf("duplicate case id %q", c.ID)
		}
		ids[c.ID] = true
		if c.Kind != Equivalent && c.Kind != Different {
			return fmt.Errorf("case %q has unknown kind %q", c.ID, c.Kind)
		}
		if c.ExpectedTier != CheapTier && c.ExpectedTier != PowerfulTier {
			return fmt.Errorf("case %q has unknown expected tier %q", c.ID, c.ExpectedTier)
		}
		sourceTier := tierForModel(router.Route(c.Source))
		queryTier := tierForModel(router.Route(c.Query))
		if sourceTier != c.ExpectedTier || queryTier != c.ExpectedTier {
			return fmt.Errorf("case %q routes as %s/%s, want %s", c.ID, sourceTier, queryTier, c.ExpectedTier)
		}
		counts[c.Kind]++
		tiers[c.ExpectedTier] = true
	}
	if counts[Equivalent] < 20 || counts[Different] < 20 {
		return fmt.Errorf("corpus needs at least 20 equivalent and 20 different cases; got %d and %d", counts[Equivalent], counts[Different])
	}
	if !tiers[CheapTier] || !tiers[PowerfulTier] {
		return fmt.Errorf("corpus must exercise both routing tiers")
	}
	return nil
}

func Run(ctx context.Context, store Cache, cases []Case) (Report, error) {
	return RunWithID(ctx, store, cases, rand.Text())
}

// RunWithID runs the evaluator inside a unique cache namespace. The ID makes a
// report reproducible against a disposable Redis instance and prevents an old
// seed from surviving into a later run when a case is edited.
func RunWithID(ctx context.Context, store Cache, cases []Case, runID string) (Report, error) {
	if runID == "" {
		return Report{}, fmt.Errorf("evaluation run ID is required")
	}
	report := Report{
		Total:           len(cases),
		PrecisionTarget: PrecisionTarget,
		RecallTarget:    RecallTarget,
		Cases:           make([]CaseResult, 0, len(cases)),
	}
	latencies := make([]float64, 0, len(cases))

	for _, c := range cases {
		model := router.Route(c.Source)
		ns := cache.NewNamespace("semantic-evaluation:"+runID+":"+c.ID, model.ID)
		seed, err := store.Begin(ctx, ns, c.Source)
		if err != nil {
			return Report{}, fmt.Errorf("case %q begin cache seed: %w", c.ID, err)
		}
		if err := seed.Set(ctx, []byte(`{"evaluation":"cached"}`), 200); err != nil {
			return Report{}, fmt.Errorf("case %q seed cache: %w", c.ID, err)
		}

		start := time.Now()
		lookup, err := store.Begin(ctx, ns, c.Query)
		if err != nil {
			return Report{}, fmt.Errorf("case %q begin lookup: %w", c.ID, err)
		}
		entry, err := lookup.Get(ctx)
		latency := float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			return Report{}, fmt.Errorf("case %q lookup: %w", c.ID, err)
		}
		hit := entry != nil
		correct := hit == (c.Kind == Equivalent)
		report.Cases = append(report.Cases, CaseResult{
			ID:              c.ID,
			Kind:            c.Kind,
			ExpectedTier:    c.ExpectedTier,
			Model:           model.ID,
			Hit:             hit,
			Correct:         correct,
			LookupLatencyMs: latency,
		})
		latencies = append(latencies, latency)

		switch {
		case c.Kind == Equivalent && hit:
			report.TruePositives++
		case c.Kind == Equivalent && !hit:
			report.FalseNegatives++
		case c.Kind == Different && hit:
			report.FalsePositives++
		default:
			report.TrueNegatives++
		}
		if hit {
			report.UpstreamCallsAvoided++
		}
	}

	report.Precision = ratio(report.TruePositives, report.TruePositives+report.FalsePositives)
	report.Recall = ratio(report.TruePositives, report.TruePositives+report.FalseNegatives)
	report.CacheHitRate = ratio(report.UpstreamCallsAvoided, report.Total)
	report.PrecisionTargetMet = report.Precision >= PrecisionTarget
	report.RecallTargetMet = report.Recall >= RecallTarget
	sort.Float64s(latencies)
	report.LookupP50Ms = percentile(latencies, 0.50)
	report.LookupP95Ms = percentile(latencies, 0.95)
	return report, nil
}

func (r Report) Markdown() string {
	return r.MarkdownWithMetadata(Metadata{})
}

type Metadata struct {
	evidence.Provenance
	CorpusVersion       string    `json:"corpus_version"`
	CorpusSHA256        string    `json:"corpus_sha256"`
	GeneratedAt         time.Time `json:"generated_at"`
	RunID               string    `json:"run_id"`
	RedisAddress        string    `json:"redis_address"`
	EmbeddingModel      string    `json:"embedding_model"`
	CacheSchema         string    `json:"cache_schema"`
	CacheTTL            string    `json:"cache_ttl"`
	SimilarityThreshold float64   `json:"similarity_threshold"`
	Environment         string    `json:"environment"`
}

func (r Report) MarkdownWithMetadata(metadata Metadata) string {
	precisionTarget := "met"
	if !r.PrecisionTargetMet {
		precisionTarget = "not met"
	}
	recallTarget := "met"
	if !r.RecallTargetMet {
		recallTarget = "not met"
	}

	var cases strings.Builder
	if len(r.Cases) > 0 {
		cases.WriteString("\n## Cases\n\n| ID | Kind | Tier | Model | Hit | Correct | Lookup latency |\n|---|---|---|---|---:|---:|---:|\n")
		for _, c := range r.Cases {
			fmt.Fprintf(&cases, "| %s | %s | %s | %s | %t | %t | %.2f ms |\n", c.ID, c.Kind, c.ExpectedTier, c.Model, c.Hit, c.Correct, c.LookupLatencyMs)
		}
	}

	metadataRows := ""
	if metadata.RunID != "" {
		metadataRows = fmt.Sprintf(`
## Run metadata

| Field | Value |
|---|---|
| Corpus version | %s |
| Corpus SHA-256 | %s |
| Run ID | %s |
| Generated at | %s |
| Embedding model | %s |
| Similarity threshold | %.2f |
| Cache schema | %s |
| Redis address | %s |
| Cache TTL | %s |
| Environment | %s |
%s
`, metadata.CorpusVersion, metadata.CorpusSHA256, metadata.RunID, metadata.GeneratedAt.UTC().Format(time.RFC3339), metadata.EmbeddingModel, metadata.SimilarityThreshold, metadata.CacheSchema, metadata.RedisAddress, metadata.CacheTTL, metadata.Environment, metadata.Provenance.Markdown())
	}
	return fmt.Sprintf(`# Semantic cache evaluation

%s

| Metric | Result |
|---|---:|
| Cases | %d |
| Precision | %.1f%% |
| Recall | %.1f%% |
| Cache hit rate | %.1f%% |
| Upstream calls avoided | %d |
| Lookup latency p50 | %.2f ms |
| Lookup latency p95 | %.2f ms |
| Precision target | %.0f%% (%s) |
| Recall target | %.0f%% (%s) |

These numbers describe this committed corpus and the environment recorded above; they
are not production claims.
%s
`, metadataRows, r.Total, r.Precision*100, r.Recall*100, r.CacheHitRate*100,
		r.UpstreamCallsAvoided, r.LookupP50Ms, r.LookupP95Ms, r.PrecisionTarget*100,
		precisionTarget, r.RecallTarget*100, recallTarget, cases.String())
}

func tierForModel(model router.Model) Tier {
	if model.ID == router.Powerful.ID {
		return PowerfulTier
	}
	return CheapTier
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1)*p + 0.5)
	return sorted[index]
}
