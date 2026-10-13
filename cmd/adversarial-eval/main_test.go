package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPreparesWithoutReadingProviderCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY_FILE", "/nonexistent/must-not-read-provider-key")
	t.Setenv("LEDGER_REDIS_ADDR", "unreachable.invalid:1")
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "report.json")
	mdPath := filepath.Join(dir, "report.md")
	oldFlags, oldArgs := flag.CommandLine, os.Args
	defer func() { flag.CommandLine = oldFlags; os.Args = oldArgs }()
	flag.CommandLine = flag.NewFlagSet("offline-eval", flag.ContinueOnError)
	os.Args = []string{"offline-eval", "-dataset", "../../docs/evaluation/adversarial-cases-v1.json", "-json", jsonPath, "-markdown", mdPath}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var report output
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "prepared-unmeasured" || report.Report.Measured || report.Report.Passed || len(report.Report.Results) != 0 || report.Repetitions != 3 || len(report.CorpusSHA256) != 64 || report.SourceCommit == "" || report.PolicyVersion == "" {
		t.Fatalf("dishonest prepared evidence: %+v", report)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatal(err)
	}
}
