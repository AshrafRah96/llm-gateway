// provider-smoke exercises the running gateway against the real provider. It is
// intentionally opt-in: the gateway must already be running with OPENAI_API_KEY,
// and the caller supplies a provisioned project key through SMOKE_API_KEY.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	maxResponseBytes = 1 << 20
)

type usageStats struct {
	Requests          int64 `json:"requests"`
	EstimatedRequests int64 `json:"estimated_requests"`
}

type check struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Details    string `json:"details"`
}

type smokeReport struct {
	RunID        string    `json:"run_id"`
	GeneratedAt  time.Time `json:"generated_at"`
	SourceCommit string    `json:"source_commit"`
	Gateway      string    `json:"gateway"`
	Runtime      string    `json:"runtime"`
	Checks       []check   `json:"checks"`
	Passed       bool      `json:"passed"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	gateway := flag.String("gateway", "http://localhost:8080", "running gateway base URL")
	prompt := flag.String("prompt", "In one sentence, what is the capital of France?", "prompt for the JSON and cache-hit checks")
	jsonPath := flag.String("json", "docs/smoke/provider-v1.json", "JSON output")
	markdownPath := flag.String("markdown", "docs/smoke/provider-v1.md", "Markdown output")
	flag.Parse()

	apiKey := os.Getenv("SMOKE_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("SMOKE_API_KEY is required; set a provisioned project key without committing it")
	}

	runID := time.Now().UTC().Format("20060102T150405.000000000Z")
	report := smokeReport{
		RunID:        runID,
		GeneratedAt:  time.Now().UTC(),
		SourceCommit: sourceCommit(),
		Gateway:      strings.TrimRight(*gateway, "/"),
		Runtime:      runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH,
	}
	client := &http.Client{Timeout: 2 * time.Minute}

	before, err := getUsage(client, report.Gateway, apiKey)
	if err != nil {
		report.Checks = append(report.Checks, check{Name: "usage baseline", Details: err.Error()})
		return writeReport(*jsonPath, *markdownPath, report, err)
	}

	basePrompt := fmt.Sprintf("%s [smoke run %s]", *prompt, runID)
	status, headers, body, err := postJSON(client, report.Gateway+"/chat", apiKey, basePrompt)
	report.Checks = append(report.Checks, check{
		Name:       "JSON completion",
		Passed:     err == nil && status == http.StatusOK && headers.Get("X-Cache") == "MISS" && json.Valid(body),
		HTTPStatus: status,
		Details:    describe(err, fmt.Sprintf("X-Cache=%s, valid JSON", headers.Get("X-Cache"))),
	})

	status, headers, _, err = postJSON(client, report.Gateway+"/chat", apiKey, basePrompt)
	report.Checks = append(report.Checks, check{
		Name:       "semantic cache hit",
		Passed:     err == nil && status == http.StatusOK && headers.Get("X-Cache") == "HIT",
		HTTPStatus: status,
		Details:    describe(err, fmt.Sprintf("X-Cache=%s", headers.Get("X-Cache"))),
	})

	completedPrompt := fmt.Sprintf("Write exactly one short sentence explaining why observability matters [completed stream %s]", runID)
	streamStatus, streamBody, streamHeaders, err := completeStream(client, report.Gateway+"/chat/stream", apiKey, completedPrompt)
	report.Checks = append(report.Checks, check{
		Name:       "completed stream",
		Passed:     err == nil && streamStatus == http.StatusOK && streamHeaders.Get("X-Cache") == "MISS" && strings.Contains(streamBody, "data: ") && strings.Contains(streamBody, "content") && strings.Contains(streamBody, "data: [DONE]"),
		HTTPStatus: streamStatus,
		Details:    describe(err, "terminal SSE event received"),
	})

	cancelPrompt := fmt.Sprintf("Write a long explanation of distributed systems cancellation [cancelled stream %s]", runID)
	status, received, err := cancelStream(client, report.Gateway+"/chat/stream", apiKey, cancelPrompt)
	report.Checks = append(report.Checks, check{
		Name:       "cancelled stream",
		Passed:     err == nil && status == http.StatusOK && received,
		HTTPStatus: status,
		Details:    describe(err, "received content before cancellation"),
	})

	after, err := waitForUsage(client, report.Gateway, apiKey, before)
	usagePassed := err == nil && after.Requests-before.Requests >= 3 && after.EstimatedRequests-before.EstimatedRequests >= 1
	report.Checks = append(report.Checks, check{
		Name:    "usage settlement",
		Passed:  usagePassed,
		Details: describe(err, fmt.Sprintf("requests +%d, estimated_requests +%d", after.Requests-before.Requests, after.EstimatedRequests-before.EstimatedRequests)),
	})

	report.Passed = true
	for _, result := range report.Checks {
		report.Passed = report.Passed && result.Passed
	}
	return writeReport(*jsonPath, *markdownPath, report, nil)
}

func postJSON(client *http.Client, url, apiKey, prompt string) (int, http.Header, []byte, error) {
	payload, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return resp.StatusCode, resp.Header, body, readErr
}

func completeStream(client *http.Client, url, apiKey, prompt string) (int, string, http.Header, error) {
	req, err := streamRequest(url, apiKey, prompt)
	if err != nil {
		return 0, "", nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return resp.StatusCode, string(body), resp.Header, err
}

func cancelStream(client *http.Client, url, apiKey, prompt string) (int, bool, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := streamRequestWithContext(ctx, url, apiKey, prompt)
	if err != nil {
		return 0, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), maxResponseBytes)
	received := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") && strings.TrimPrefix(scanner.Text(), "data: ") != "[DONE]" {
			received = true
			cancel()
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return resp.StatusCode, received, err
	}
	return resp.StatusCode, received, nil
}

func streamRequest(url, apiKey, prompt string) (*http.Request, error) {
	return streamRequestWithContext(context.Background(), url, apiKey, prompt)
}

func streamRequestWithContext(ctx context.Context, url, apiKey, prompt string) (*http.Request, error) {
	payload, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	return req, nil
}

func getUsage(client *http.Client, gateway, apiKey string) (usageStats, error) {
	req, err := http.NewRequest(http.MethodGet, gateway+"/usage", nil)
	if err != nil {
		return usageStats{}, err
	}
	req.Header.Set("X-API-Key", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return usageStats{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return usageStats{}, fmt.Errorf("usage returned HTTP %d", resp.StatusCode)
	}
	var stats usageStats
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&stats); err != nil {
		return usageStats{}, err
	}
	return stats, nil
}

func waitForUsage(client *http.Client, gateway, apiKey string, before usageStats) (usageStats, error) {
	// Settlement uses a detached five-second context, but the writes are normally
	// immediate. One delayed read keeps the smoke run below the gateway's request quota.
	time.Sleep(500 * time.Millisecond)
	latest, err := getUsage(client, gateway, apiKey)
	if err != nil {
		return usageStats{}, err
	}
	if latest.Requests-before.Requests < 3 || latest.EstimatedRequests-before.EstimatedRequests < 1 {
		return latest, fmt.Errorf("usage did not settle: requests +%d, estimated_requests +%d", latest.Requests-before.Requests, latest.EstimatedRequests-before.EstimatedRequests)
	}
	return latest, nil
}

func writeReport(jsonPath, markdownPath string, report smokeReport, runErr error) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(jsonPath, append(encoded, '\n')); err != nil {
		return err
	}
	if err := writeFile(markdownPath, []byte(markdown(report))); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	if !report.Passed {
		return fmt.Errorf("provider smoke checks failed; see %s", markdownPath)
	}
	return nil
}

func markdown(report smokeReport) string {
	var rows strings.Builder
	for _, result := range report.Checks {
		status := "FAIL"
		if result.Passed {
			status = "PASS"
		}
		fmt.Fprintf(&rows, "| %s | %s | %d | %s |\n", result.Name, status, result.HTTPStatus, result.Details)
	}
	status := "failed"
	if report.Passed {
		status = "passed"
	}
	return fmt.Sprintf(`# Real-provider smoke test

Status: **%s**

| Field | Value |
|---|---|
| Run ID | %s |
| Generated at | %s |
| Source commit | %s |
| Gateway | %s |
| Runtime | %s |

| Check | Result | HTTP | Details |
|---|---|---:|---|
%s`, status, report.RunID, report.GeneratedAt.UTC().Format(time.RFC3339), report.SourceCommit, report.Gateway, report.Runtime, rows.String())
}

func sourceCommit() string {
	output, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func describe(err error, success string) string {
	if err != nil {
		return err.Error()
	}
	return success
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
