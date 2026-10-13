package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/router"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{
			name: "defaults",
			env:  map[string]string{"OPENAI_API_KEY": "sk-test"},
			want: Config{
				OpenAIAPIKey:            "sk-test",
				RedisAddr:               "localhost:6379",
				LedgerAddr:              "localhost:6380",
				CacheTTL:                24 * time.Hour,
				RequestTimeout:          60 * time.Second,
				MaxRequestBodyBytes:     64 << 10,
				MaxConcurrentRequests:   16,
				MaxConcurrentPerProject: 4,
				Router:                  router.Default(),
			},
		},
		{
			name: "explicit values",
			env: map[string]string{
				"OPENAI_API_KEY": "sk-test",
				"REDIS_ADDR":     "redis:6380",
				"CACHE_TTL":      "90m",
			},
			want: Config{
				OpenAIAPIKey:            "sk-test",
				RedisAddr:               "redis:6380",
				LedgerAddr:              "localhost:6380",
				CacheTTL:                90 * time.Minute,
				RequestTimeout:          60 * time.Second,
				MaxRequestBodyBytes:     64 << 10,
				MaxConcurrentRequests:   16,
				MaxConcurrentPerProject: 4,
				Router:                  router.Default(),
			},
		},
		{
			name:    "missing OpenAI key",
			env:     map[string]string{},
			wantErr: "OPENAI_API_KEY not set",
		},
		{
			name: "invalid cache TTL",
			env: map[string]string{
				"OPENAI_API_KEY": "sk-test",
				"CACHE_TTL":      "zero",
			},
			wantErr: "parse CACHE_TTL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadConfig(func(key string) string {
				return tt.env[key]
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadConfig error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got != tt.want {
				t.Fatalf("LoadConfig = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNewRejectsInvalidConfigurationBeforeDialingRedis(t *testing.T) {
	valid := Config{
		OpenAIAPIKey:            "sk-test",
		RedisAddr:               "127.0.0.1:0",
		LedgerAddr:              "127.0.0.1:1",
		CacheTTL:                time.Hour,
		APIKeyPepper:            "test-pepper",
		RequestTimeout:          time.Second,
		MaxRequestBodyBytes:     1024,
		MaxConcurrentRequests:   1,
		MaxConcurrentPerProject: 1,
		Router:                  router.Default(),
	}
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:    "missing OpenAI key",
			mutate:  func(c *Config) { c.OpenAIAPIKey = "" },
			wantErr: "OPENAI_API_KEY not set",
		},
		{
			name:    "missing Redis address",
			mutate:  func(c *Config) { c.RedisAddr = "" },
			wantErr: "Redis address is required",
		},
		{
			name:    "non-positive cache TTL",
			mutate:  func(c *Config) { c.CacheTTL = 0 },
			wantErr: "cache TTL must be greater than zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)

			if _, err := New(context.Background(), cfg); err == nil ||
				!strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfiguredModelsRequireCorrectPricesAndCaps(t *testing.T) {
	for _, env := range []map[string]string{
		{"CHEAP_MODEL": "unknown"},
		{"CHEAP_MODEL": "unknown", "CHEAP_PRICE_IN": "0.001", "CHEAP_PRICE_OUT": "0.002"},
		{"CHEAP_PRICE_IN": "+1", "CHEAP_PRICE_OUT": "1"},
		{"CHEAP_MAX_OUTPUT_TOKENS": "0"},
		{"CHEAP_FALLBACK_PRICE_IN": "1", "CHEAP_FALLBACK_PRICE_OUT": "2"},
	} {
		env["OPENAI_API_KEY"] = "fixture"
		if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("accepted invalid catalogue: %+v", env)
		}
	}
	env := map[string]string{"OPENAI_API_KEY": "fixture", "CHEAP_MODEL": "custom", "CHEAP_PRICE_IN": "0.001", "CHEAP_PRICE_OUT": "0.002", "CHEAP_MAX_OUTPUT_TOKENS": "100"}
	cfg, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Router.Cheap.ID != "custom" || cfg.Router.Models()[0].PriceIn != 0.001 || cfg.Router.Models()[0].MaxOutputTokens != 100 {
		t.Fatalf("catalogue mismatch: %+v", cfg.Router.Models())
	}
}
