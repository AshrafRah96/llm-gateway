package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const keyName = "api_keys"
const projectKeyPrefix = "project_key:"

// Principal is the non-secret identity propagated through the gateway after auth.
type Principal struct {
	ProjectID string
	KeyID     string
}

type KeyStore struct {
	client *redis.Client
	pepper []byte
}

// NewKeyStore accepts an optional pepper solely so legacy unit tests can keep using
// the old Redis set. Production must supply a non-empty API_KEY_PEPPER.
func NewKeyStore(client *redis.Client, pepper ...string) *KeyStore {
	s := &KeyStore{client: client}
	if len(pepper) > 0 {
		s.pepper = []byte(pepper[0])
	}
	return s
}

func (s *KeyStore) Valid(ctx context.Context, apiKey string) (bool, error) {
	if len(s.pepper) > 0 {
		_, ok, err := s.Lookup(ctx, apiKey)
		return ok, err
	}
	return s.client.SIsMember(ctx, keyName, apiKey).Result()
}

func (s *KeyStore) Lookup(ctx context.Context, apiKey string) (Principal, bool, error) {
	if apiKey == "" || len(s.pepper) == 0 {
		return Principal{}, false, nil
	}
	id := s.Fingerprint(apiKey)
	data, err := s.client.HGetAll(ctx, projectKeyPrefix+id).Result()
	if err != nil {
		return Principal{}, false, err
	}
	project := data["project"]
	if project == "" || data["revoked"] == "1" {
		return Principal{}, false, nil
	}
	return Principal{ProjectID: project, KeyID: id}, true, nil
}

func (s *KeyStore) Fingerprint(apiKey string) string {
	mac := hmac.New(sha256.New, s.pepper)
	mac.Write([]byte(apiKey))
	return hex.EncodeToString(mac.Sum(nil))
}

// Provision stores only a keyed fingerprint. Operators call this from the small
// project-key command; the raw key must be handed to the caller separately.
func (s *KeyStore) Provision(ctx context.Context, projectID, apiKey string) error {
	if len(s.pepper) == 0 {
		return fmt.Errorf("API_KEY_PEPPER is required")
	}
	if projectID == "" || apiKey == "" {
		return fmt.Errorf("project and API key are required")
	}
	return s.client.HSet(ctx, projectKeyPrefix+s.Fingerprint(apiKey), "project", projectID, "revoked", "0").Err()
}
