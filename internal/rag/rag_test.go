package rag

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/redis/go-redis/v9"
)

type fixedEmbedder struct{}

func (fixedEmbedder) Embed(context.Context, string) ([]float32, error) {
	vector := make([]float32, cache.EmbeddingDimension)
	vector[0] = 1
	return vector, nil
}

func redisSearch(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	t.Cleanup(func() { client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis is not reachable: %v", err)
	}
	if _, err := client.Do(ctx, "FT._LIST").Result(); err != nil {
		t.Skipf("Redis Search is not available: %v", err)
	}
	return client
}

func TestRetrieveNeverCrossesProjects(t *testing.T) {
	client := redisSearch(t)
	store, err := New(context.Background(), client, fixedEmbedder{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	projectA, projectB := "rag-a-"+t.Name(), "rag-b-"+t.Name()
	if _, err := store.Upload(context.Background(), projectA, "private.md", "Only project A may see this sentence."); err != nil {
		t.Fatalf("Upload A: %v", err)
	}

	got, err := store.Retrieve(context.Background(), projectB, "What sentence is stored?")
	if err != nil {
		t.Fatalf("Retrieve B: %v", err)
	}
	if len(got.Sources) != 0 || got.Prompt != "" {
		t.Fatalf("project B received %+v", got)
	}

	got, err = store.Retrieve(context.Background(), projectA, "What sentence is stored?")
	if err != nil {
		t.Fatalf("Retrieve A: %v", err)
	}
	if len(got.Sources) != 1 || got.Prompt == "" {
		t.Fatalf("project A retrieval = %+v", got)
	}
	if got.CorpusVersion == "0" {
		t.Fatal("upload must advance the corpus revision")
	}
}

func TestUploadRejectsOversizedAndInvalidDocuments(t *testing.T) {
	store := &Store{embedder: fixedEmbedder{}}
	for _, content := range []string{"", string(make([]byte, maxDocumentBytes+1))} {
		if _, err := store.Upload(context.Background(), "project", fmt.Sprintf("%d.md", len(content)), content); err == nil {
			t.Errorf("Upload accepted document of %d bytes", len(content))
		}
	}
}
