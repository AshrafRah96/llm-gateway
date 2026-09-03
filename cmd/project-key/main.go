// project-key provisions a project-scoped gateway key without placing its raw value
// in Redis. The generated key is printed once; operators must store it securely.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/ashrafrah96/llm-gateway/internal/auth"
	"github.com/redis/go-redis/v9"
)

func main() {
	project := flag.String("project", "", "project identifier")
	key := flag.String("key", "", "existing API key; omit to generate one")
	flag.Parse()
	if *project == "" {
		log.Fatal("-project is required")
	}
	pepper := os.Getenv("API_KEY_PEPPER")
	if pepper == "" {
		log.Fatal("API_KEY_PEPPER not set")
	}
	if *key == "" {
		value := make([]byte, 32)
		if _, err := rand.Read(value); err != nil {
			log.Fatalf("generate key: %v", err)
		}
		*key = "lgw_" + base64.RawURLEncoding.EncodeToString(value)
	}
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	if err := auth.NewKeyStore(client, pepper).Provision(context.Background(), *project, *key); err != nil {
		log.Fatalf("provision key: %v", err)
	}
	fmt.Println(*key)
}
