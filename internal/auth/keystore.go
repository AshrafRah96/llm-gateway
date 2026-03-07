package auth

import (
	"context"

	"github.com/redis/go-redis/v9"
)

const keyName = "api_keys"

type KeyStore struct {
	client *redis.Client
}

func NewKeyStore(client *redis.Client) *KeyStore {
	return &KeyStore{client: client}
}

func (s *KeyStore) Valid(ctx context.Context, apiKey string) (bool, error) {
	return s.client.SIsMember(ctx, keyName, apiKey).Result()
}

func (s *KeyStore) Add(ctx context.Context, apiKey string) error {
	return s.client.SAdd(ctx, keyName, apiKey).Err()
}

func (s *KeyStore) Remove(ctx context.Context, apiKey string) error {
	return s.client.SRem(ctx, keyName, apiKey).Err()
}
