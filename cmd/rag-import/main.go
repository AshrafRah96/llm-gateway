// rag-import ingests one operator-approved UTF-8 Markdown or text file into a project.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"

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
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY not set")
	}
	content, err := os.ReadFile(*file)
	if err != nil {
		log.Fatalf("read document: %v", err)
	}
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	defer client.Close()
	store, err := rag.New(context.Background(), client, cache.NewEmbeddingClient(apiKey))
	if err != nil {
		log.Fatalf("create RAG store: %v", err)
	}
	id, err := store.Upload(context.Background(), *project, filepath.Base(*file), string(content))
	if err != nil {
		log.Fatalf("upload document: %v", err)
	}
	log.Printf("indexed document %s for project %s", id, *project)
}
