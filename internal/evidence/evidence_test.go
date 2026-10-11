package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreatesAndRefreshesArtifacts(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "nested", "report.json")
	markdownPath := filepath.Join(dir, "nested", "report.md")

	if err := Write(jsonPath, markdownPath, map[string]string{"status": "pass"}, "# first\n"); err != nil {
		t.Fatalf("write first report: %v", err)
	}
	if err := Write(jsonPath, markdownPath, map[string]string{"status": "updated"}, "# second\n"); err != nil {
		t.Fatalf("refresh report: %v", err)
	}

	jsonBytes, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read JSON report: %v", err)
	}
	if !strings.Contains(string(jsonBytes), `"status": "updated"`) {
		t.Fatalf("JSON report was not refreshed: %s", jsonBytes)
	}
	markdownBytes, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("read Markdown report: %v", err)
	}
	if string(markdownBytes) != "# second\n" {
		t.Fatalf("Markdown report = %q, want refreshed content", markdownBytes)
	}
}
