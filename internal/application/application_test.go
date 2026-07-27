package application

import (
	"context"
	"strings"
	"testing"
	"time"
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
				OpenAIAPIKey: "sk-test",
				RedisAddr:    "localhost:6379",
				CacheTTL:     24 * time.Hour,
				ListenAddr:   ":8080",
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
				OpenAIAPIKey: "sk-test",
				RedisAddr:    "redis:6380",
				CacheTTL:     90 * time.Minute,
				ListenAddr:   ":8080",
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
		OpenAIAPIKey: "sk-test",
		RedisAddr:    "127.0.0.1:0",
		CacheTTL:     time.Hour,
		ListenAddr:   ":8080",
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
		{
			name:    "missing listen address",
			mutate:  func(c *Config) { c.ListenAddr = "" },
			wantErr: "listen address is required",
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
