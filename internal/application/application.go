package application

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/auth"
	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/handler"
	"github.com/ashrafrah96/llm-gateway/internal/middleware"
	"github.com/ashrafrah96/llm-gateway/internal/provider"
	"github.com/ashrafrah96/llm-gateway/internal/rag"
	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisAddr  = "localhost:6379"
	defaultListenAddr = ":8080"
)

type Config struct {
	OpenAIAPIKey            string
	RedisAddr               string
	CacheTTL                time.Duration
	APIKeyPepper            string
	RequestTimeout          time.Duration
	MaxRequestBodyBytes     int64
	MaxConcurrentRequests   int
	MaxConcurrentPerProject int
	Router                  router.Router
}

func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		OpenAIAPIKey:            getenv("OPENAI_API_KEY"),
		RedisAddr:               getenv("REDIS_ADDR"),
		APIKeyPepper:            getenv("API_KEY_PEPPER"),
		RequestTimeout:          60 * time.Second,
		MaxRequestBodyBytes:     64 << 10,
		MaxConcurrentRequests:   16,
		MaxConcurrentPerProject: 4,
		Router:                  configuredRouter(getenv),
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
	if value := getenv("REQUEST_TIMEOUT"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("parse REQUEST_TIMEOUT")
		}
		cfg.RequestTimeout = parsed
	}
	if value := getenv("MAX_REQUEST_BODY_BYTES"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("parse MAX_REQUEST_BODY_BYTES")
		}
		cfg.MaxRequestBodyBytes = parsed
	}
	if value := getenv("MAX_CONCURRENT_REQUESTS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("parse MAX_CONCURRENT_REQUESTS")
		}
		cfg.MaxConcurrentRequests = parsed
	}
	if value := getenv("MAX_CONCURRENT_PER_PROJECT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("parse MAX_CONCURRENT_PER_PROJECT")
		}
		cfg.MaxConcurrentPerProject = parsed
	}
	return cfg, nil
}

func configuredRouter(getenv func(string) string) router.Router {
	r := router.Default()
	if id := getenv("CHEAP_MODEL"); id != "" {
		r.Cheap.ID = id
	}
	if id := getenv("POWERFUL_MODEL"); id != "" {
		r.Powerful.ID = id
	}
	r.CheapFallback, r.PowerfulFallback = r.Cheap, r.Powerful
	if id := getenv("CHEAP_FALLBACK_MODEL"); id != "" {
		r.CheapFallback.ID = id
	}
	if id := getenv("POWERFUL_FALLBACK_MODEL"); id != "" {
		r.PowerfulFallback.ID = id
	}
	return r
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
	ragger, err := rag.New(ctx, redisClient, cache.NewEmbeddingClient(cfg.OpenAIAPIKey))
	if err != nil {
		redisClient.Close()
		return nil, fmt.Errorf("create RAG store: %w", err)
	}

	openAIClient := provider.NewOpenAIClient(cfg.OpenAIAPIKey)
	keyStore := auth.NewKeyStore(redisClient, cfg.APIKeyPepper)
	limiter := ratelimit.New(
		ratelimit.NewRedisStore(redisClient),
		ratelimit.DefaultConfig(),
	)
	usageTracker := usage.NewTracker(redisClient)
	completions := completion.NewWithRouting(openAIClient, semanticCache, usageTracker, cfg.Router, ragger)
	httpHandler := handler.NewServer(
		handler.New(completions, usageTracker, limiter),
		middleware.Auth(keyStore),
		middleware.RateLimit(limiter),
		middleware.RequestLimits(cfg.MaxRequestBodyBytes, cfg.RequestTimeout, cfg.MaxConcurrentRequests, cfg.MaxConcurrentPerProject),
	)

	return &Application{
		Server: &http.Server{
			Addr:    defaultListenAddr,
			Handler: httpHandler,
		},
		redis: redisClient,
	}, nil
}

func (a *Application) Close() error {
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
	case cfg.APIKeyPepper == "":
		return fmt.Errorf("API_KEY_PEPPER not set")
	case cfg.RequestTimeout <= 0:
		return fmt.Errorf("request timeout must be greater than zero")
	case cfg.MaxRequestBodyBytes <= 0 || cfg.MaxConcurrentRequests <= 0 || cfg.MaxConcurrentPerProject <= 0:
		return fmt.Errorf("request limits must be greater than zero")
	default:
		return nil
	}
}
