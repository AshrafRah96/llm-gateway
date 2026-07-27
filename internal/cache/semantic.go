package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	indexName           = "prompt_cache_v2"
	keyPrefix           = "cache:v2:"
	vectorDim           = 1536 // text-embedding-3-small
	similarityThreshold = 0.95
	DefaultTTL          = 24 * time.Hour
)

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type SemanticCache struct {
	client   *redis.Client
	embedder Embedder
	ttl      time.Duration
}

type CacheEntry struct {
	Prompt   string `json:"prompt"`
	Response []byte `json:"response"`
	Status   int    `json:"status"`
}

func ParseTTL(value string) (time.Duration, error) {
	if value == "" {
		return DefaultTTL, nil
	}
	ttl, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse CACHE_TTL: %w", err)
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("CACHE_TTL must be greater than zero")
	}
	return ttl, nil
}

func NewSemanticCache(client *redis.Client, embedder Embedder, ttl time.Duration) (*SemanticCache, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("cache TTL must be greater than zero")
	}
	cache := &SemanticCache{
		client:   client,
		embedder: embedder,
		ttl:      ttl,
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
		"PREFIX", "1", keyPrefix,
		"SCHEMA",
		"tenant", "TAG",
		"model", "TAG",
		"version", "TAG",
		"created_at", "NUMERIC",
		"embedding", "VECTOR", "FLAT", "6",
		"TYPE", "FLOAT32",
		"DIM", vectorDim,
		"DISTANCE_METRIC", "COSINE",
	).Result()
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	return nil
}

func (c *SemanticCache) Get(ctx context.Context, ns Namespace, prompt string) (*CacheEntry, error) {
	embedding, err := c.embedder.Embed(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(embedding) != vectorDim {
		return nil, fmt.Errorf("embedding dimension = %d, want %d", len(embedding), vectorDim)
	}

	vectorBytes := float32ToBytes(embedding)
	results, err := c.client.FTSearchWithArgs(
		ctx,
		indexName,
		"(@tenant:{$tenant} @model:{$model} @version:{$version})=>[KNN 1 @embedding $vec AS score]",
		&redis.FTSearchOptions{
			Params: map[string]interface{}{
				"tenant":  ns.Tenant,
				"model":   escapeTagValue(ns.Model),
				"version": ns.Version,
				"vec":     vectorBytes,
			},
			SortBy: []redis.FTSearchSortBy{
				{FieldName: "score", Asc: true},
			},
			Return: []redis.FTSearchReturn{
				{FieldName: "data"},
				{FieldName: "score"},
			},
			Limit:          1,
			DialectVersion: 2,
		},
	).Result()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return c.parseSearchResult(results)
}

func (c *SemanticCache) Set(ctx context.Context, ns Namespace, prompt string, response []byte, status int) error {
	embedding, err := c.embedder.Embed(ctx, prompt)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(embedding) != vectorDim {
		return fmt.Errorf("embedding dimension = %d, want %d", len(embedding), vectorDim)
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

	key := cacheKey(ns, prompt)
	vectorBytes := float32ToBytes(embedding)

	_, err = c.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key,
			"tenant", ns.Tenant,
			"model", ns.Model,
			"version", ns.Version,
			"created_at", time.Now().Unix(),
			"embedding", vectorBytes,
			"data", data,
		)
		pipe.PExpire(ctx, key, c.ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func (c *SemanticCache) parseSearchResult(results redis.FTSearchResult) (*CacheEntry, error) {
	if len(results.Docs) == 0 {
		return nil, nil
	}

	fields := results.Docs[0].Fields
	data, ok := fields["data"]
	if !ok {
		// Redis Search may briefly retain an index result after the backing hash
		// expires. A result without stored data is therefore a cache miss.
		return nil, nil
	}

	scoreValue, ok := fields["score"]
	if !ok {
		return nil, fmt.Errorf("search result is missing score")
	}
	score, err := strconv.ParseFloat(scoreValue, 64)
	if err != nil {
		return nil, fmt.Errorf("parse search score: %w", err)
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

func cacheKey(ns Namespace, prompt string) string {
	namespace := ns.Tenant + "\x00" + ns.Model + "\x00" + ns.Version
	return keyPrefix + hashValue(namespace) + ":" + hashValue(prompt)
}

func hashValue(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func escapeTagValue(value string) string {
	var escaped strings.Builder
	for _, r := range value {
		switch r {
		case '$', '{', '}', '\\', '|':
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}

func float32ToBytes(floats []float32) []byte {
	buf := make([]byte, len(floats)*4)
	for i, f := range floats {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}
