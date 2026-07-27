package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/evaluation"
	"github.com/redis/go-redis/v9"
)

type output struct {
	CorpusVersion  string            `json:"corpus_version"`
	GeneratedAt    time.Time         `json:"generated_at"`
	RedisAddress   string            `json:"redis_address"`
	EmbeddingModel string            `json:"embedding_model"`
	CacheSchema    string            `json:"cache_schema"`
	Report         evaluation.Report `json:"report"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	datasetPath := flag.String("dataset", "docs/evaluation/cases-v1.json", "evaluation corpus")
	jsonPath := flag.String("json", "tmp/cache-eval-results.json", "JSON output")
	markdownPath := flag.String("markdown", "tmp/cache-eval-results.md", "Markdown output")
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

	file, err := os.Open(*datasetPath)
	if err != nil {
		return fmt.Errorf("open dataset: %w", err)
	}
	defer file.Close()
	corpus, err := evaluation.DecodeCorpus(file)
	if err != nil {
		return err
	}
	if err := evaluation.Validate(corpus.Cases); err != nil {
		return fmt.Errorf("validate corpus: %w", err)
	}

	client := redis.NewClient(&redis.Options{Addr: redisAddr, Protocol: 2})
	defer client.Close()
	semanticCache, err := cache.NewSemanticCache(
		context.Background(),
		client,
		cache.NewEmbeddingClient(apiKey),
		ttl,
	)
	if err != nil {
		return fmt.Errorf("create semantic cache: %w", err)
	}

	report, err := evaluation.Run(context.Background(), semanticCache, corpus.Cases)
	if err != nil {
		return err
	}
	result := output{
		CorpusVersion:  corpus.Version,
		GeneratedAt:    time.Now().UTC(),
		RedisAddress:   redisAddr,
		EmbeddingModel: "text-embedding-3-small",
		CacheSchema:    cache.SchemaVersion,
		Report:         report,
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON result: %w", err)
	}
	if err := writeFile(*jsonPath, append(encoded, '\n')); err != nil {
		return err
	}
	if err := writeFile(*markdownPath, []byte(report.Markdown())); err != nil {
		return err
	}

	fmt.Printf("wrote %s and %s\n", *jsonPath, *markdownPath)
	if !report.PrecisionTargetMet {
		return fmt.Errorf("precision %.1f%% did not meet the %.0f%% target", report.Precision*100, evaluation.PrecisionTarget*100)
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
