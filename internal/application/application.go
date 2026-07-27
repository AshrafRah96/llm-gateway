package application

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/auth"
	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/handler"
	"github.com/ashrafrah96/llm-gateway/internal/middleware"
	"github.com/ashrafrah96/llm-gateway/internal/provider"
	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisAddr  = "localhost:6379"
	defaultListenAddr = ":8080"
)

type Config struct {
	OpenAIAPIKey string
	RedisAddr    string
	CacheTTL     time.Duration
	ListenAddr   string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		OpenAIAPIKey: getenv("OPENAI_API_KEY"),
		RedisAddr:    getenv("REDIS_ADDR"),
		ListenAddr:   defaultListenAddr,
	}
	if cfg.OpenAIAPIKey == "" {
		return Config{}, fmt.Errorf("OPENAI_API_KEY not set")
	}
	if cfg.RedisAddr == "" {
		cfg.RedisAddr = defaultRedisAddr
	}

	ttl, err := cache.ParseTTL(getenv("CACHE_TTL"))
	if err != nil {
		return Config{}, err
	}
	cfg.CacheTTL = ttl
	return cfg, nil
}

type Application struct {
	Server *http.Server
	redis  *redis.Client
}

func New(ctx context.Context, cfg Config) (*Application, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	// RediSearch's typed responses are stable under RESP2; go-redis still marks
	// the RESP3 search response format as unstable.
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Protocol: 2,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("ping Redis: %w", err)
	}

	semanticCache, err := cache.NewSemanticCache(
		ctx,
		redisClient,
		cache.NewEmbeddingClient(cfg.OpenAIAPIKey),
		cfg.CacheTTL,
	)
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("create semantic cache: %w", err)
	}

	openAIClient := provider.NewOpenAIClient(cfg.OpenAIAPIKey)
	keyStore := auth.NewKeyStore(redisClient)
	limiter := ratelimit.New(
		ratelimit.NewRedisStore(redisClient),
		ratelimit.DefaultConfig(),
	)
	usageTracker := usage.NewTracker(redisClient)
	completions := completion.New(openAIClient, semanticCache, usageTracker)
	httpHandler := handler.NewServer(
		handler.New(completions, usageTracker, limiter),
		middleware.Auth(keyStore),
		middleware.RateLimit(limiter),
	)

	return &Application{
		Server: &http.Server{
			Addr:    cfg.ListenAddr,
			Handler: httpHandler,
		},
		redis: redisClient,
	}, nil
}

func (a *Application) Close() error {
	if a == nil || a.redis == nil {
		return nil
	}
	return a.redis.Close()
}

func validateConfig(cfg Config) error {
	switch {
	case cfg.OpenAIAPIKey == "":
		return fmt.Errorf("OPENAI_API_KEY not set")
	case cfg.RedisAddr == "":
		return fmt.Errorf("Redis address is required")
	case cfg.CacheTTL <= 0:
		return fmt.Errorf("cache TTL must be greater than zero")
	case cfg.ListenAddr == "":
		return fmt.Errorf("listen address is required")
	default:
		return nil
	}
}
