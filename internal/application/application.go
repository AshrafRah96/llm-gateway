package application

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/auth"
	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/handler"
	"github.com/ashrafrah96/llm-gateway/internal/middleware"
	"github.com/ashrafrah96/llm-gateway/internal/money"
	"github.com/ashrafrah96/llm-gateway/internal/provider"
	"github.com/ashrafrah96/llm-gateway/internal/rag"
	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
	"github.com/ashrafrah96/llm-gateway/internal/redisconfig"
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
	LedgerAddr              string
	RedisOptions            *redis.Options
	LedgerOptions           *redis.Options
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
		Router:                  router.Default(),
	}
	var err error
	cfg.OpenAIAPIKey, err = redisconfig.Secret(getenv, "OPENAI_API_KEY")
	if err != nil {
		return Config{}, err
	}
	cfg.APIKeyPepper, err = redisconfig.Secret(getenv, "API_KEY_PEPPER")
	if err != nil {
		return Config{}, err
	}
	cfg.Router, err = configuredRouter(getenv)
	if err != nil {
		return Config{}, err
	}
	if cfg.OpenAIAPIKey == "" {
		return Config{}, fmt.Errorf("OPENAI_API_KEY not set")
	}
	if cfg.RedisAddr == "" {
		cfg.RedisAddr = defaultRedisAddr
	}

	cacheOptions, err := redisconfig.Options(getenv, "REDIS_", defaultRedisAddr)
	if err != nil {
		return Config{}, err
	}
	ledgerOptions, err := redisconfig.Options(getenv, "LEDGER_REDIS_", "localhost:6380")
	if err != nil {
		return Config{}, err
	}
	cfg.LedgerAddr = ledgerOptions.Addr
	if cacheOptions.Username != "" || cacheOptions.Password != "" || cacheOptions.TLSConfig != nil {
		cfg.RedisOptions = cacheOptions
	}
	if ledgerOptions.Username != "" || ledgerOptions.Password != "" || ledgerOptions.TLSConfig != nil {
		cfg.LedgerOptions = ledgerOptions
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

func configuredRouter(getenv func(string) string) (router.Router, error) {
	r := router.Default()
	known := router.All()
	var err error
	r.Cheap, err = configuredModel(getenv, "CHEAP_", r.Cheap, known)
	if err != nil {
		return r, err
	}
	r.Powerful, err = configuredModel(getenv, "POWERFUL_", r.Powerful, known)
	if err != nil {
		return r, err
	}
	known = append(known, r.Cheap, r.Powerful)
	r.CheapFallback, err = configuredModel(getenv, "CHEAP_FALLBACK_", r.Cheap, known)
	if err != nil {
		return r, err
	}
	r.PowerfulFallback, err = configuredModel(getenv, "POWERFUL_FALLBACK_", r.Powerful, known)
	if err != nil {
		return r, err
	}
	for _, a := range r.Models() {
		if a.MaxOutputTokens <= 0 || a.MaxOutputTokens > 32768 {
			return r, fmt.Errorf("invalid model output cap")
		}
		if _, _, err := a.Rates(); err != nil {
			return r, err
		}
	}
	list := []router.Model{r.Cheap, r.Powerful, r.CheapFallback, r.PowerfulFallback}
	for _, a := range list {
		for _, b := range list {
			if a.ID == b.ID && (a.PriceIn != b.PriceIn || a.PriceOut != b.PriceOut || a.MaxOutputTokens != b.MaxOutputTokens) {
				return r, fmt.Errorf("conflicting prices or output caps for model %s", a.ID)
			}
		}
	}
	return r, nil
}

func configuredModel(getenv func(string) string, prefix string, m router.Model, known []router.Model) (router.Model, error) {
	id := getenv(prefix + "MODEL")
	in, out, cap := getenv(prefix+"PRICE_IN"), getenv(prefix+"PRICE_OUT"), getenv(prefix+"MAX_OUTPUT_TOKENS")
	if id != "" && id != m.ID {
		found := false
		for _, candidate := range known {
			if candidate.ID == id {
				m = candidate
				found = true
				break
			}
		}
		if !found && (in == "" || out == "" || cap == "") {
			return m, fmt.Errorf("%sMODEL override requires PRICE_IN, PRICE_OUT and MAX_OUTPUT_TOKENS", prefix)
		}
		m.ID = id
	}
	if (in == "") != (out == "") {
		return m, fmt.Errorf("set both %sPRICE_IN and PRICE_OUT", prefix)
	}
	if in != "" {
		if _, err := money.ParseUSD(in); err != nil {
			return m, err
		}
		if _, err := money.ParseUSD(out); err != nil {
			return m, err
		}
		var err error
		m.PriceIn, err = strconv.ParseFloat(in, 64)
		if err != nil {
			return m, fmt.Errorf("invalid model input price")
		}
		m.PriceOut, err = strconv.ParseFloat(out, 64)
		if err != nil {
			return m, fmt.Errorf("invalid model output price")
		}
	}
	if cap != "" {
		var err error
		m.MaxOutputTokens, err = strconv.Atoi(cap)
		if err != nil {
			return m, fmt.Errorf("invalid model output cap")
		}
	}
	return m, nil
}

type Application struct {
	Server *http.Server
	redis  *redis.Client
	ledger *redis.Client
}

func New(ctx context.Context, cfg Config) (*Application, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	// RediSearch uses RESP2; billing owns a different Redis process.
	cacheOptions := cfg.RedisOptions
	if cacheOptions == nil {
		cacheOptions = &redis.Options{Addr: cfg.RedisAddr, Protocol: 2}
	}
	ledgerOptions := cfg.LedgerOptions
	if ledgerOptions == nil {
		ledgerOptions = &redis.Options{Addr: cfg.LedgerAddr, Protocol: 2}
	}
	redisClient := redis.NewClient(cacheOptions)
	ledgerClient := redis.NewClient(ledgerOptions)
	cleanup := func() { redisClient.Close(); ledgerClient.Close() }
	if err := redisClient.Ping(ctx).Err(); err != nil {
		cleanup()
		return nil, fmt.Errorf("ping cache Redis: %w", err)
	}
	if err := ledgerClient.Ping(ctx).Err(); err != nil {
		cleanup()
		return nil, fmt.Errorf("ping ledger Redis: %w", err)
	}
	cacheInfo, err := redisClient.Info(ctx, "server").Result()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("cache identity: %w", err)
	}
	ledgerInfo, err := ledgerClient.Info(ctx, "server").Result()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("ledger identity: %w", err)
	}
	identity := func(info string) string {
		for _, line := range strings.Split(info, "\n") {
			if strings.HasPrefix(line, "run_id:") {
				return strings.TrimSpace(strings.TrimPrefix(line, "run_id:"))
			}
		}
		return ""
	}
	if identity(cacheInfo) == "" || identity(ledgerInfo) == "" || identity(cacheInfo) == identity(ledgerInfo) {
		cleanup()
		return nil, fmt.Errorf("cache and ledger must use separate Redis processes")
	}
	keyStore := auth.NewKeyStore(ledgerClient, cfg.APIKeyPepper)
	if err := keyStore.CheckLegacyKeys(ctx, redisClient); err != nil {
		cleanup()
		return nil, err
	}
	usageTracker := usage.NewTracker(ledgerClient)
	if err := usageTracker.CheckLegacy(ctx, redisClient); err != nil {
		cleanup()
		return nil, err
	}

	semanticCache, err := cache.NewSemanticCache(
		ctx,
		redisClient,
		cache.NewMeteredEmbeddingClient(cfg.OpenAIAPIKey, usageTracker, "cache_lookup"),
		cfg.CacheTTL,
	)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create semantic cache: %w", err)
	}
	ragger, err := rag.New(ctx, redisClient, cache.NewMeteredEmbeddingClient(cfg.OpenAIAPIKey, usageTracker, "rag_lookup"))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create RAG store: %w", err)
	}

	openAIClient := provider.NewOpenAIClient(cfg.OpenAIAPIKey)
	limiter := ratelimit.New(
		ratelimit.NewRedisStore(redisClient),
		ratelimit.DefaultConfig(),
	)
	completions := completion.NewWithLedger(openAIClient, semanticCache, usageTracker, cfg.Router, ragger)
	handlerAdapter := handler.New(completions, usageTracker, limiter)
	handlerAdapter.Readiness(func(ctx context.Context) error {
		if err := ledgerClient.Ping(ctx).Err(); err != nil {
			return err
		}
		if err := redisClient.Do(ctx, "FT.INFO", "prompt_cache_v2").Err(); err != nil {
			return err
		}
		return redisClient.Do(ctx, "FT.INFO", "project_rag_v1").Err()
	})
	httpHandler := handler.NewServer(
		handlerAdapter,
		middleware.Auth(keyStore),
		middleware.RateLimit(limiter),
		middleware.RequestLimits(cfg.MaxRequestBodyBytes, cfg.RequestTimeout, cfg.MaxConcurrentRequests, cfg.MaxConcurrentPerProject),
	)

	return &Application{
		Server: &http.Server{
			Addr:    defaultListenAddr,
			Handler: httpHandler,
		},
		redis:  redisClient,
		ledger: ledgerClient,
	}, nil
}

func (a *Application) Close() error {
	cacheErr := a.redis.Close()
	ledgerErr := a.ledger.Close()
	if cacheErr != nil {
		return cacheErr
	}
	return ledgerErr
}

func validateConfig(cfg Config) error {
	switch {
	case cfg.OpenAIAPIKey == "":
		return fmt.Errorf("OPENAI_API_KEY not set")
	case cfg.RedisAddr == "":
		return fmt.Errorf("Redis address is required")
	case cfg.LedgerAddr == "" || cfg.LedgerAddr == cfg.RedisAddr:
		return fmt.Errorf("a separate ledger Redis address is required")
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
