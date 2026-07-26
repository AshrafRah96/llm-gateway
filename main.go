package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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

const shutdownTimeout = 30 * time.Second

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY not set")
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}

	openaiClient := provider.NewOpenAIClient(apiKey)
	keyStore := auth.NewKeyStore(redisClient)
	rateLimitStore := ratelimit.NewRedisStore(redisClient)
	limiter := ratelimit.New(rateLimitStore, ratelimit.DefaultConfig())
	embedder := cache.NewEmbeddingClient(apiKey)

	semanticCache, err := cache.NewSemanticCache(redisClient, embedder)
	if err != nil {
		log.Fatalf("semantic cache: %v", err)
	}

	usageTracker := usage.NewTracker(redisClient)

	completions := completion.New(openaiClient, semanticCache, usageTracker)
	h := handler.New(completions, usageTracker, limiter)

	mux := handler.NewServer(h,
		middleware.Auth(keyStore),
		middleware.RateLimit(limiter),
	)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	done := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		close(done)
	}()

	log.Printf("listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}

	<-done
}
