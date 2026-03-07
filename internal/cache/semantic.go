package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"

	"github.com/redis/go-redis/v9"
)

const (
	indexName           = "prompt_cache"
	vectorDim           = 1536 // text-embedding-3-small
	similarityThreshold = 0.95
)

type SemanticCache struct {
	client   *redis.Client
	embedder *EmbeddingClient
}

type CacheEntry struct {
	Prompt   string `json:"prompt"`
	Response []byte `json:"response"`
	Status   int    `json:"status"`
}

func NewSemanticCache(client *redis.Client, embedder *EmbeddingClient) (*SemanticCache, error) {
	cache := &SemanticCache{
		client:   client,
		embedder: embedder,
	}
	if err := cache.createIndex(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *SemanticCache) createIndex() error {
	ctx := context.Background()

	_, err := c.client.Do(ctx, "FT.INFO", indexName).Result()
	if err == nil {
		return nil
	}

	_, err = c.client.Do(ctx,
		"FT.CREATE", indexName,
		"ON", "HASH",
		"PREFIX", "1", "cache:",
		"SCHEMA",
		"embedding", "VECTOR", "FLAT", "6",
		"TYPE", "FLOAT32",
		"DIM", vectorDim,
		"DISTANCE_METRIC", "COSINE",
		"data", "TEXT",
	).Result()
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	return nil
}

func (c *SemanticCache) Get(ctx context.Context, prompt string) (*CacheEntry, error) {
	embedding, err := c.embedder.Embed(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}

	vectorBytes := float32ToBytes(embedding)
	results, err := c.client.Do(ctx,
		"FT.SEARCH", indexName,
		"*=>[KNN 1 @embedding $vec AS score]",
		"PARAMS", "2", "vec", vectorBytes,
		"SORTBY", "score",
		"RETURN", "2", "data", "score",
		"DIALECT", "2",
	).Result()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return c.parseSearchResult(results)
}

func (c *SemanticCache) Set(ctx context.Context, prompt string, response []byte, status int) error {
	embedding, err := c.embedder.Embed(ctx, prompt)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	entry := CacheEntry{
		Prompt:   prompt,
		Response: response,
		Status:   status,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	key := "cache:" + hashPrompt(prompt)
	vectorBytes := float32ToBytes(embedding)

	_, err = c.client.HSet(ctx, key,
		"embedding", vectorBytes,
		"data", data,
	).Result()
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func (c *SemanticCache) parseSearchResult(results interface{}) (*CacheEntry, error) {
	arr, ok := results.([]interface{})
	if !ok || len(arr) < 3 {
		return nil, nil
	}

	fields, ok := arr[2].([]interface{})
	if !ok || len(fields) < 4 {
		return nil, nil
	}

	var score float64
	var data string
	for i := 0; i < len(fields)-1; i += 2 {
		fieldName, _ := fields[i].(string)
		switch fieldName {
		case "score":
			if s, ok := fields[i+1].(string); ok {
				fmt.Sscanf(s, "%f", &score)
			}
		case "data":
			data, _ = fields[i+1].(string)
		}
	}

	// Cosine distance to similarity
	if 1-score < similarityThreshold {
		return nil, nil
	}

	var entry CacheEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return &entry, nil
}

func hashPrompt(prompt string) string {
	h := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(h[:16])
}

func float32ToBytes(floats []float32) []byte {
	buf := make([]byte, len(floats)*4)
	for i, f := range floats {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}
