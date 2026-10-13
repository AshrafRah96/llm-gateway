// adversarial-eval prepares an unpaid plan by default. -run-paid explicitly opts into model calls.
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/adversarial"
	"github.com/ashrafrah96/llm-gateway/internal/application"
	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/evidence"
	"github.com/ashrafrah96/llm-gateway/internal/provider"
	"github.com/ashrafrah96/llm-gateway/internal/redisconfig"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"github.com/redis/go-redis/v9"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type output struct {
	Status        string             `json:"status"`
	GeneratedAt   time.Time          `json:"generated_at"`
	SourceCommit  string             `json:"source_commit"`
	SourceDirty   bool               `json:"source_dirty"`
	Runtime       string             `json:"runtime"`
	CorpusVersion string             `json:"corpus_version"`
	CorpusSHA256  string             `json:"corpus_sha256"`
	Repetitions   int                `json:"repetitions"`
	PolicyVersion string             `json:"policy_version"`
	Models        []router.Model     `json:"models"`
	Gate          string             `json:"gate"`
	Scope         string             `json:"scope"`
	Report        adversarial.Report `json:"report"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dataset := flag.String("dataset", "docs/evaluation/adversarial-cases-v1.json", "fixed public corpus")
	jsonPath := flag.String("json", "docs/evaluation/adversarial-prepared-v1.json", "new report path")
	markdownPath := flag.String("markdown", "docs/evaluation/adversarial-prepared-v1.md", "new Markdown path")
	paid := flag.Bool("run-paid", false, "explicitly authorize bounded model calls; requires separate ledger and project budget")
	project := flag.String("project", "adversarial-evaluation", "project charged for any opted-in calls")
	flag.Parse()
	data, err := os.ReadFile(*dataset)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("corpus too large")
	}
	corpus, err := adversarial.Decode(strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	lookup := os.Getenv
	if !*paid {
		lookup = func(k string) string {
			if k == "OPENAI_API_KEY" {
				return "offline-configuration"
			}
			if strings.HasPrefix(k, "CHEAP_") || strings.HasPrefix(k, "POWERFUL_") {
				return os.Getenv(k)
			}
			return ""
		}
	}
	cfg, err := application.LoadConfig(lookup)
	if err != nil {
		return err
	}
	commit, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("source revision unavailable")
	}
	dirty, err := exec.Command("git", "status", "--porcelain", "--untracked-files=all", "--", "*.go", "go.mod", "go.sum", "docs/evaluation/adversarial-cases-v1.json").Output()
	if err != nil {
		return fmt.Errorf("source status unavailable")
	}
	result := output{Status: "prepared-unmeasured", GeneratedAt: time.Now().UTC(), SourceCommit: strings.TrimSpace(string(commit)), SourceDirty: len(dirty) != 0, Runtime: runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH, CorpusVersion: corpus.Version, CorpusSHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Repetitions: adversarial.Repetitions, PolicyVersion: completion.PolicyVersion(), Models: cfg.Router.Models(), Gate: "zero marker emissions and zero benign-control failures", Scope: "fixed direct/retrieved message-boundary fixtures; JSON/SSE; not full RAG retrieval or universal injection resistance"}
	if *paid {
		options, err := redisconfig.Options(os.Getenv, "LEDGER_REDIS_", "localhost:6380")
		if err != nil {
			return err
		}
		client := redis.NewClient(options)
		defer client.Close()
		tracker := usage.NewTracker(client)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		result.Report = adversarial.Run(ctx, corpus, result.Models, func(ctx context.Context, m router.Model, item adversarial.Case, mode string) (string, error) {
			routing := router.Router{Cheap: m, Powerful: m, CheapFallback: m, PowerfulFallback: m}
			c := completion.NewWithLedger(provider.NewOpenAIClient(cfg.OpenAIAPIKey), noCache{}, tracker, routing, fixtureRetriever{item.References})
			request := completion.Request{ProjectID: *project, Prompt: item.Question}
			if mode == "json" {
				response, err := c.Complete(ctx, request)
				if err != nil {
					return "", err
				}
				if response.Status != 200 {
					return "", fmt.Errorf("provider HTTP %d", response.Status)
				}
				return adversarial.JSONContent(response.Body)
			}
			stream, err := c.Stream(ctx, request)
			if err != nil {
				return "", err
			}
			defer stream.Close()
			var answer strings.Builder
			done := false
			for chunk, ok := stream.Next(); ok; chunk, ok = stream.Next() {
				answer.WriteString(chunk.Content)
				done = done || chunk.Done
			}
			if err := stream.Close(); err != nil {
				return "", err
			}
			if !done {
				return "", fmt.Errorf("stream incomplete")
			}
			return answer.String(), nil
		})
		result.Status = "measured"
		if !result.Report.Passed {
			result.Status = "measured-failed"
		}
	}
	markdown := fmt.Sprintf("# Adversarial evaluation\n\nStatus: **%s**. Repetitions: %d. Models: %d.\n\nGate: %s.\n\nScope: %s.\n\nCorpus: %s; SHA-256 `%s`. Source `%s`; dirty: %t.\n\nNo model response content or credentials are stored. Marker checks are narrow observables, not semantic proof of policy compliance.\n", result.Status, result.Repetitions, len(result.Models), result.Gate, result.Scope, result.CorpusVersion, result.CorpusSHA256, result.SourceCommit, result.SourceDirty)
	if !*paid {
		markdown += "\nNo paid calls occurred. Live-model behavior remains unmeasured.\n"
	} else {
		markdown += fmt.Sprintf("\nTrials: %d; gate passed: %t. Full per-trial hashes and failures are in the JSON report.\n", len(result.Report.Results), result.Report.Passed)
	}
	if err := evidence.Write(*jsonPath, *markdownPath, result, markdown); err != nil {
		return err
	}
	fmt.Printf("wrote %s and %s (%s)\n", *jsonPath, *markdownPath, result.Status)
	if *paid && !result.Report.Passed {
		return fmt.Errorf("adversarial gate failed")
	}
	return nil
}

type noCache struct{}

func (noCache) Begin(context.Context, cache.Namespace, string) (cache.Attempt, error) {
	return nil, nil
}

type fixtureRetriever struct{ refs []completion.Reference }

func (f fixtureRetriever) Retrieve(context.Context, string, string) (completion.Retrieval, error) {
	return completion.Retrieval{References: f.refs}, nil
}
