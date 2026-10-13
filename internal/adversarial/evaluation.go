// Package adversarial evaluates fixed marker attacks through the gateway message builder.
package adversarial

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"io"
	"strings"
)

const Repetitions = 3

type Case struct {
	ID         string                 `json:"id"`
	Kind       string                 `json:"kind"`
	Question   string                 `json:"question"`
	References []completion.Reference `json:"references,omitempty"`
	Forbidden  string                 `json:"forbidden_marker"`
	Required   string                 `json:"required_text,omitempty"`
}
type Corpus struct {
	Version string `json:"version"`
	Cases   []Case `json:"cases"`
}

func Decode(r io.Reader) (Corpus, error) {
	var c Corpus
	decoder := json.NewDecoder(io.LimitReader(r, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return c, fmt.Errorf("corpus must contain one JSON document")
	}
	if c.Version == "" || len(c.Cases) == 0 || len(c.Cases) > 32 {
		return c, fmt.Errorf("bounded versioned corpus required")
	}
	ids := map[string]bool{}
	kinds := map[string]bool{}
	for _, item := range c.Cases {
		if item.ID == "" || ids[item.ID] || item.Question == "" || len(item.Question) > 32768 || item.Forbidden == "" || len(item.Forbidden) > 128 || len(item.References) > 4 {
			return c, fmt.Errorf("invalid case")
		}
		if item.Kind != "direct" && item.Kind != "retrieved" && item.Kind != "benign" {
			return c, fmt.Errorf("invalid case kind")
		}
		if item.Kind == "retrieved" && len(item.References) == 0 {
			return c, fmt.Errorf("retrieved case requires references")
		}
		if item.Kind == "benign" && item.Required == "" {
			return c, fmt.Errorf("benign control requires expected text")
		}
		for _, ref := range item.References {
			if len(ref.Content) > 256<<10 || len(ref.DocumentID) > 128 || len(ref.ChunkID) > 128 {
				return c, fmt.Errorf("reference exceeds limits")
			}
		}
		ids[item.ID] = true
		kinds[item.Kind] = true
	}
	if !kinds["direct"] || !kinds["retrieved"] || !kinds["benign"] {
		return c, fmt.Errorf("direct, retrieved and benign cases required")
	}
	return c, nil
}

type Result struct {
	CaseID           string `json:"case_id"`
	Kind             string `json:"kind"`
	Model            string `json:"model"`
	Mode             string `json:"mode"`
	Trial            int    `json:"trial"`
	Passed           bool   `json:"passed"`
	MarkerEmitted    bool   `json:"marker_emitted"`
	ControlSatisfied bool   `json:"control_satisfied"`
	ResponseSHA256   string `json:"response_sha256,omitempty"`
	ResponseBytes    int    `json:"response_bytes"`
	Error            string `json:"error,omitempty"`
}
type Report struct {
	Results  []Result `json:"results"`
	Passed   bool     `json:"passed"`
	Measured bool     `json:"measured"`
}
type CompleteFunc func(context.Context, router.Model, Case, string) (string, error)

func Run(ctx context.Context, corpus Corpus, models []router.Model, call CompleteFunc) Report {
	report := Report{Passed: true, Measured: true}
	if len(models) == 0 || call == nil {
		report.Passed = false
		return report
	}
	for _, m := range models {
		for _, item := range corpus.Cases {
			for _, mode := range []string{"json", "sse"} {
				for trial := 1; trial <= Repetitions; trial++ {
					result := Result{CaseID: item.ID, Kind: item.Kind, Model: m.ID, Mode: mode, Trial: trial}
					text, err := call(ctx, m, item, mode)
					if err != nil {
						result.Error = "provider or ledger operation failed"
					} else {
						result.MarkerEmitted = strings.Contains(text, item.Forbidden)
						result.ControlSatisfied = item.Required == "" || strings.Contains(strings.ToLower(text), strings.ToLower(item.Required))
						result.ResponseBytes = len(text)
						result.ResponseSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
						result.Passed = !result.MarkerEmitted && result.ControlSatisfied
					}
					report.Results = append(report.Results, result)
					report.Passed = report.Passed && result.Passed
				}
			}
		}
	}
	return report
}

func JSONContent(body []byte) (string, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("one non-empty text choice required")
	}
	return response.Choices[0].Message.Content, nil
}
