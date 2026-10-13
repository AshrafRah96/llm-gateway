// project-key administers project access with scoped operator Redis credentials.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/auth"
	"github.com/ashrafrah96/llm-gateway/internal/redisconfig"
	"github.com/redis/go-redis/v9"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	action := flag.String("action", "provision", "provision, rotate or revoke")
	project := flag.String("project", "", "project identifier")
	key := flag.String("key", "", "existing API key; prefer -key-file")
	keyFile := flag.String("key-file", "", "existing key file; required for rotation/revocation")
	lifetime := flag.Duration("ttl", 90*24*time.Hour, "new key lifetime")
	overlap := flag.Duration("overlap", 24*time.Hour, "rotation overlap; zero revokes immediately")
	flag.Parse()
	if *project == "" {
		return fmt.Errorf("-project is required")
	}
	if *keyFile != "" {
		if *key != "" {
			return fmt.Errorf("choose -key or -key-file")
		}
		data, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		*key = strings.TrimSpace(string(data))
	}
	pepper, err := redisconfig.Secret(os.Getenv, "API_KEY_PEPPER")
	if err != nil {
		return err
	}
	if pepper == "" {
		return fmt.Errorf("API_KEY_PEPPER is required")
	}
	options, err := redisconfig.Options(os.Getenv, "LEDGER_REDIS_", "localhost:6380")
	if err != nil {
		return err
	}
	client := redis.NewClient(options)
	defer client.Close()
	store := auth.NewKeyStore(client, pepper)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	generate := func() (string, error) {
		value := make([]byte, 32)
		if _, err := rand.Read(value); err != nil {
			return "", err
		}
		return "lgw_" + base64.RawURLEncoding.EncodeToString(value), nil
	}
	switch *action {
	case "provision":
		if *key == "" {
			*key, err = generate()
			if err != nil {
				return err
			}
		}
		if err := store.ProvisionFor(ctx, *project, *key, *lifetime); err != nil {
			return err
		}
		fmt.Println(*key)
	case "rotate":
		if *key == "" {
			return fmt.Errorf("existing key is required")
		}
		replacement, err := generate()
		if err != nil {
			return err
		}
		if err := store.Rotate(ctx, *project, *key, replacement, *overlap, *lifetime); err != nil {
			return err
		}
		fmt.Println(replacement)
	case "revoke":
		if *key == "" {
			return fmt.Errorf("existing key is required")
		}
		if err := store.Revoke(ctx, *project, *key); err != nil {
			return err
		}
		fmt.Println("revoked")
	default:
		return fmt.Errorf("unknown action")
	}
	return nil
}
