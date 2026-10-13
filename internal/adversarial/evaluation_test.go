package adversarial

import (
	"context"
	"errors"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"strings"
	"testing"
)

func TestRepeatedModeGates(t *testing.T) {
	c := Corpus{Version: "fixture", Cases: []Case{{ID: "attack", Kind: "direct", Forbidden: "ATTACK"}, {ID: "control", Kind: "benign", Forbidden: "ATTACK", Required: "safe"}}}
	calls := 0
	r := Run(context.Background(), c, []router.Model{router.Cheap}, func(_ context.Context, _ router.Model, _ Case, _ string) (string, error) { calls++; return "safe", nil })
	if !r.Passed || len(r.Results) != 12 || calls != 12 {
		t.Fatalf("repetitions: %+v %d", r, calls)
	}
	r = Run(context.Background(), c, []router.Model{router.Cheap}, func(_ context.Context, _ router.Model, _ Case, mode string) (string, error) {
		if mode == "sse" {
			return "ATTACK", nil
		}
		return "safe", nil
	})
	if r.Passed {
		t.Fatal("marker emission accepted")
	}
	r = Run(context.Background(), c, []router.Model{router.Cheap}, func(context.Context, router.Model, Case, string) (string, error) {
		return "", errors.New("secret must not enter report")
	})
	if r.Passed || strings.Contains(r.Results[0].Error, "secret") {
		t.Fatal("failed operation misreported or leaked")
	}
	r = Run(context.Background(), c, []router.Model{router.Cheap}, func(context.Context, router.Model, Case, string) (string, error) { return "wrong control", nil })
	if r.Passed {
		t.Fatal("benign failure accepted")
	}
}
func TestCorpusRequiresBalancedControls(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"version":"v1","cases":[]}`)); err == nil {
		t.Fatal("empty corpus accepted")
	}
	if _, err := JSONContent([]byte(`{"choices":[]}`)); err == nil {
		t.Fatal("missing answer accepted")
	}
}
