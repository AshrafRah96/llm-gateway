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
	results, err := c.client.Do(ctx,
		"FT.SEARCH", indexName,
		"(@tenant:{$tenant} @model:{$model} @version:{$version})=>[KNN 1 @embedding $vec AS score]",
		"PARAMS", "8",
		"tenant", ns.Tenant,
		"model", escapeTagValue(ns.Model),
		"version", ns.Version,
		"vec", vectorBytes,
		"SORTBY", "score",
		"RETURN", "2", "data", "score",
		"DIALECT", "2",
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

func (c *SemanticCache) parseSearchResult(results interface{}) (*CacheEntry, error) {
	data, score, found, err := searchResultFields(results)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
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

func searchResultFields(results interface{}) (data string, score float64, found bool, err error) {
	switch response := results.(type) {
	case []interface{}:
		if len(response) < 3 {
			return "", 0, false, nil
		}
		fields, ok := response[2].([]interface{})
		if !ok {
			return "", 0, false, fmt.Errorf("search result fields have type %T", response[2])
		}
		return fieldPairs(fields)

	case map[interface{}]interface{}, map[string]interface{}:
		rawResults, ok := mapField(response, "results")
		if !ok {
			return "", 0, false, fmt.Errorf("RESP3 search result has no results field")
		}
		documents, ok := rawResults.([]interface{})
		if !ok {
			return "", 0, false, fmt.Errorf("RESP3 results have type %T", rawResults)
		}
		if len(documents) == 0 {
			return "", 0, false, nil
		}
		rawAttributes, ok := mapField(documents[0], "extra_attributes")
		if !ok {
			return "", 0, false, fmt.Errorf("RESP3 result has no extra_attributes field")
		}
		if mapLength(rawAttributes) == 0 {
			// Redis Search can briefly retain an index result after the backing hash
			// expires. With no stored fields left, it is a miss rather than corrupt data.
			return "", 0, false, nil
		}
		dataValue, dataOK := mapField(rawAttributes, "data")
		scoreValue, scoreOK := mapField(rawAttributes, "score")
		if !dataOK || !scoreOK {
			return "", 0, false, fmt.Errorf("RESP3 result is missing data or score")
		}
		data, ok = stringValue(dataValue)
		if !ok {
			return "", 0, false, fmt.Errorf("RESP3 data has type %T", dataValue)
		}
		score, err = numberValue(scoreValue)
		if err != nil {
			return "", 0, false, err
		}
		return data, score, true, nil

	default:
		return "", 0, false, fmt.Errorf("search response has type %T", results)
	}
}

func fieldPairs(fields []interface{}) (data string, score float64, found bool, err error) {
	var dataFound, scoreFound bool
	for i := 0; i+1 < len(fields); i += 2 {
		fieldName, _ := stringValue(fields[i])
		switch fieldName {
		case "data":
			data, dataFound = stringValue(fields[i+1])
		case "score":
			score, err = numberValue(fields[i+1])
			scoreFound = err == nil
		}
	}
	if err != nil {
		return "", 0, false, err
	}
	if !dataFound || !scoreFound {
		return "", 0, false, fmt.Errorf("search result is missing data or score")
	}
	return data, score, true, nil
}

func mapField(value interface{}, key string) (interface{}, bool) {
	switch fields := value.(type) {
	case map[interface{}]interface{}:
		result, ok := fields[key]
		return result, ok
	case map[string]interface{}:
		result, ok := fields[key]
		return result, ok
	default:
		return nil, false
	}
}

func mapLength(value interface{}) int {
	switch fields := value.(type) {
	case map[interface{}]interface{}:
		return len(fields)
	case map[string]interface{}:
		return len(fields)
	default:
		return -1
	}
}

func stringValue(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	default:
		return "", false
	}
}

func numberValue(value interface{}) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case string:
		number, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, fmt.Errorf("parse search score: %w", err)
		}
		return number, nil
	case []byte:
		return numberValue(string(typed))
	default:
		return 0, fmt.Errorf("search score has type %T", value)
	}
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
