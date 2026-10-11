package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/evaluation"
	"github.com/ashrafrah96/llm-gateway/internal/evidence"
	"github.com/redis/go-redis/v9"
)

type output struct {
	evaluation.Metadata
	Report evaluation.Report `json:"report"`
}

func main() {
	if err := run(); err != nil {
		message := err.Error()
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			message = strings.ReplaceAll(message, key, "[redacted]")
		}
		fmt.Fprintln(os.Stderr, message)
		os.Exit(1)
	}
}

func run() error {
	datasetPath := flag.String("dataset", "docs/evaluation/cases-v1.json", "evaluation corpus")
	jsonPath := flag.String("json", "docs/evaluation/results-v1.json", "new JSON output")
	markdownPath := flag.String("markdown", "docs/evaluation/results-v1.md", "new Markdown output")
	environment := flag.String("environment", "unspecified", "machine, region and Redis deployment description (no secrets)")
	flag.Parse()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY is required")
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	ttl, err := cache.ParseTTL(os.Getenv("CACHE_TTL"))
	if err != nil {
		return err
	}
	corpusBytes, err := os.ReadFile(*datasetPath)
	if err != nil {
		return fmt.Errorf("read dataset: %w", err)
	}
	corpus, err := evaluation.DecodeCorpus(bytes.NewReader(corpusBytes))
	if err != nil {
		return err
	}
	if err := evaluation.Validate(corpus.Cases); err != nil {
		return fmt.Errorf("validate corpus: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: redisAddr, Protocol: 2})
	defer client.Close()
	provenance, err := evidence.Capture(ctx, client)
	if err != nil {
		return err
	}
	semanticCache, err := cache.NewSemanticCache(ctx, client, cache.NewEmbeddingClient(apiKey), ttl)
	if err != nil {
		return fmt.Errorf("create semantic cache: %w", err)
	}
	result := output{Metadata: evaluation.Metadata{
		Provenance:    provenance,
		CorpusVersion: corpus.Version, CorpusSHA256: fmt.Sprintf("%x", sha256.Sum256(corpusBytes)),
		RunID: rand.Text(), RedisAddress: redisAddr, EmbeddingModel: cache.EmbeddingModel,
		CacheSchema: cache.SchemaVersion, CacheTTL: ttl.String(), SimilarityThreshold: cache.SimilarityThreshold,
		Environment: *environment,
	}}
	result.Report, err = evaluation.RunWithID(ctx, semanticCache, corpus.Cases, result.RunID)
	if err != nil {
		return err
	}
	result.GeneratedAt = time.Now().UTC()
	if err := evidence.Write(*jsonPath, *markdownPath, result, result.Report.MarkdownWithMetadata(result.Metadata)); err != nil {
		return err
	}
	fmt.Printf("wrote %s and %s\n", *jsonPath, *markdownPath)
	if !result.Report.PrecisionTargetMet || !result.Report.RecallTargetMet {
		return fmt.Errorf("evaluation targets not met: precision %.1f%% (target %.0f%%), recall %.1f%% (target %.0f%%)", result.Report.Precision*100, evaluation.PrecisionTarget*100, result.Report.Recall*100, evaluation.RecallTarget*100)
	}
	return nil
}
