// rag-import ingests one operator-approved UTF-8 Markdown or text file into a project.
package main

import (
	"context"
	"flag"
	"github.com/ashrafrah96/llm-gateway/internal/redisconfig"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/rag"
	"github.com/redis/go-redis/v9"
)

func main() {
	project := flag.String("project", "", "project identifier")
	file := flag.String("file", "", "UTF-8 text or Markdown file")
	flag.Parse()
	if *project == "" || *file == "" {
		log.Fatal("-project and -file are required")
	}
	apiKey, err := redisconfig.Secret(os.Getenv, "OPENAI_API_KEY")
	if err != nil {
		log.Fatal(err)
	}
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY not set")
	}
	content, err := os.ReadFile(*file)
	if err != nil {
		log.Fatalf("read document: %v", err)
	}
	options, err := redisconfig.Options(os.Getenv, "REDIS_", "localhost:6379")
	if err != nil {
		log.Fatal(err)
	}
	ledgerOptions, err := redisconfig.Options(os.Getenv, "LEDGER_REDIS_", "localhost:6380")
	if err != nil {
		log.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	ledger := redis.NewClient(ledgerOptions)
	defer ledger.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ctx = usage.WithProject(ctx, *project)
	store, err := rag.New(ctx, client, cache.NewMeteredEmbeddingClient(apiKey, usage.NewTracker(ledger), "rag_import"))
	if err != nil {
		log.Fatalf("create RAG store: %v", err)
	}
	id, err := store.Upload(ctx, *project, filepath.Base(*file), string(content))
	if err != nil {
		log.Fatalf("upload document: %v", err)
	}
	log.Printf("indexed document %s for project %s", id, *project)
}
