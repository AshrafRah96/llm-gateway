package evaluation

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
)

type fakeCache struct {
	hits map[string]bool
}

func (c *fakeCache) Begin(_ context.Context, _ cache.Namespace, prompt string) (cache.Attempt, error) {
	return fakeAttempt{cache: c, prompt: prompt}, nil
}

type fakeAttempt struct {
	cache  *fakeCache
	prompt string
}

func (a fakeAttempt) Get(context.Context) (*cache.CacheEntry, error) {
	if a.cache.hits[a.prompt] {
		return &cache.CacheEntry{Response: []byte(`{"answer":"cached"}`), Status: 200}, nil
	}
	return nil, nil
}

func (a fakeAttempt) Set(context.Context, []byte, int) error {
	return nil
}

func TestRunCalculatesClassificationMetrics(t *testing.T) {
	cases := []Case{
		{ID: "positive-hit", Kind: Equivalent, Source: "capital of France", Query: "France capital", ExpectedTier: CheapTier},
		{ID: "positive-miss", Kind: Equivalent, Source: "capital of Spain", Query: "Spain capital", ExpectedTier: CheapTier},
		{ID: "negative-hit", Kind: Different, Source: "capital of France", Query: "capital of Germany", ExpectedTier: CheapTier},
		{ID: "negative-miss", Kind: Different, Source: "capital of Spain", Query: "capital of Italy", ExpectedTier: CheapTier},
	}
	store := &fakeCache{hits: map[string]bool{
		"France capital":     true,
		"capital of Germany": true,
	}}

	report, err := Run(context.Background(), store, cases)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.TruePositives != 1 || report.FalseNegatives != 1 ||
		report.FalsePositives != 1 || report.TrueNegatives != 1 {
		t.Fatalf("classification counts = %+v", report)
	}
	if report.Precision != 0.5 || report.Recall != 0.5 || report.CacheHitRate != 0.5 {
		t.Fatalf("metrics = precision %.2f recall %.2f hit rate %.2f", report.Precision, report.Recall, report.CacheHitRate)
	}
	if report.UpstreamCallsAvoided != 2 || len(report.Cases) != 4 {
		t.Fatalf("report = %+v", report)
	}
}

func TestValidateRequiresTwentyCasesOfEachKindAndBothTiers(t *testing.T) {
	var cases []Case
	for i := 0; i < 20; i++ {
		cases = append(cases,
			Case{ID: fmt.Sprintf("equivalent-%d", i), Kind: Equivalent, Source: "hello", Query: "hi", ExpectedTier: CheapTier},
			Case{ID: fmt.Sprintf("different-%d", i), Kind: Different, Source: "analyze cats", Query: "analyze dogs", ExpectedTier: PowerfulTier},
		)
	}
	if err := Validate(cases); err != nil {
		t.Fatalf("valid corpus rejected: %v", err)
	}

	if err := Validate(cases[:len(cases)-1]); err == nil {
		t.Fatal("corpus with fewer than 20 negative cases was accepted")
	}
}

func TestMarkdownStatesWhetherPrecisionTargetWasMet(t *testing.T) {
	report := Report{Total: 40, Precision: 0.94, Recall: 0.8}
	got := report.Markdown()
	if want := "not met"; !strings.Contains(strings.ToLower(got), want) {
		t.Fatalf("Markdown did not state that the target was %s:\n%s", want, got)
	}
}

func TestDecodeCorpusRejectsUnknownFields(t *testing.T) {
	_, err := DecodeCorpus(strings.NewReader(`{
		"version":"v1",
		"cases":[],
		"unexpected":true
	}`))
	if err == nil {
		t.Fatal("DecodeCorpus accepted an unknown field")
	}
}

func TestCommittedCorpusIsValid(t *testing.T) {
	file, err := os.Open("../../docs/evaluation/cases-v1.json")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	defer file.Close()

	corpus, err := DecodeCorpus(file)
	if err != nil {
		t.Fatalf("DecodeCorpus: %v", err)
	}
	if err := Validate(corpus.Cases); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
