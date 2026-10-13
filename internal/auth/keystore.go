package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

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
	if data["expires_at"] != "" {
		expires, err := strconv.ParseInt(data["expires_at"], 10, 64)
		if err != nil || expires <= 0 {
			return Principal{}, false, fmt.Errorf("invalid key expiry")
		}
		now, err := s.client.Time(ctx).Result()
		if err != nil {
			return Principal{}, false, err
		}
		if !now.Before(time.Unix(expires, 0)) {
			return Principal{}, false, nil
		}
	}
	if data["revoked"] != "" && data["revoked"] != "0" {
		return Principal{}, false, fmt.Errorf("invalid revocation state")
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
	return s.ProvisionFor(ctx, projectID, apiKey, 90*24*time.Hour)
}

func (s *KeyStore) ProvisionFor(ctx context.Context, projectID, apiKey string, lifetime time.Duration) error {
	if len(s.pepper) == 0 {
		return fmt.Errorf("API_KEY_PEPPER is required")
	}
	if projectID == "" || apiKey == "" {
		return fmt.Errorf("project and API key are required")
	}
	if lifetime <= 0 {
		return fmt.Errorf("key lifetime must be positive")
	}
	now, err := s.client.Time(ctx).Result()
	if err != nil {
		return err
	}
	return provisionScript.Run(ctx, s.client, []string{projectKeyPrefix + s.Fingerprint(apiKey)}, projectID, now.Add(lifetime).Unix(), now.Unix()).Err()
}

func (s *KeyStore) Revoke(ctx context.Context, project, key string) error {
	if len(s.pepper) == 0 || project == "" || key == "" {
		return fmt.Errorf("project, key and pepper required")
	}
	return revokeScript.Run(ctx, s.client, []string{projectKeyPrefix + s.Fingerprint(key)}, project).Err()
}

func (s *KeyStore) Rotate(ctx context.Context, project, oldKey, newKey string, overlap, lifetime time.Duration) error {
	if len(s.pepper) == 0 || project == "" || oldKey == "" || newKey == "" || oldKey == newKey || overlap < 0 || lifetime <= 0 {
		return fmt.Errorf("invalid rotation")
	}
	now, err := s.client.Time(ctx).Result()
	if err != nil {
		return err
	}
	oldID, newID := s.Fingerprint(oldKey), s.Fingerprint(newKey)
	return rotateScript.Run(ctx, s.client, []string{projectKeyPrefix + oldID, projectKeyPrefix + newID}, project, now.Unix(), now.Add(overlap).Unix(), now.Add(lifetime).Unix(), oldID, newID).Err()
}

const keyChecks = `
local function check(key)
 local t=redis.call('TYPE',key).ok
 if t~='none' and t~='hash' then error('invalid key record type') end
end
local function expiry(key)
 local raw=redis.call('HGET',key,'expires_at')
 if not raw then return 0 end
 if not string.match(raw,'^%d+$') then error('invalid expiry') end
 local n=tonumber(raw);if not n or n<=0 or n>9007199254740991 then error('invalid expiry') end
 return n
end
`

var provisionScript = redis.NewScript(keyChecks + `
check(KEYS[1])
local old=redis.call('HGET',KEYS[1],'project')
if redis.call('EXISTS',KEYS[1])==1 then
 if not old or old~=ARGV[1] then error('key project cannot change') end
 local revoked=redis.call('HGET',KEYS[1],'revoked')
 if revoked and revoked~='0' then error('revoked key cannot be reprovisioned') end
 expiry(KEYS[1]);return 0
end
redis.call('HSET',KEYS[1],'project',ARGV[1],'revoked','0','expires_at',ARGV[2],'created_at',ARGV[3]);return 1
`)
var revokeScript = redis.NewScript(keyChecks + `
check(KEYS[1]);if redis.call('HGET',KEYS[1],'project')~=ARGV[1] then error('key project mismatch') end
redis.call('HSET',KEYS[1],'revoked','1');return 1
`)
var rotateScript = redis.NewScript(keyChecks + `
check(KEYS[1]);check(KEYS[2])
local project=redis.call('HGET',KEYS[1],'project')
local revoked=redis.call('HGET',KEYS[1],'revoked')
if project~=ARGV[1] or (revoked and revoked~='0') then error('old key is not live for project') end
local now=tonumber(ARGV[2]);local oldExpiry=expiry(KEYS[1]);local untilTime=tonumber(ARGV[3])
if oldExpiry>0 and oldExpiry<=now then error('old key expired') end
local rotated=redis.call('HGET',KEYS[1],'rotated_to')
if rotated then
 if rotated==ARGV[6] and redis.call('HGET',KEYS[2],'project')==project then return 0 end
 error('old key already rotated')
end
if redis.call('EXISTS',KEYS[2])==1 then error('replacement fingerprint already exists') end
if oldExpiry>0 and oldExpiry<untilTime then untilTime=oldExpiry end
redis.call('HSET',KEYS[2],'project',project,'revoked','0','expires_at',ARGV[4],'created_at',ARGV[2],'rotated_from',ARGV[5])
redis.call('HSET',KEYS[1],'expires_at',string.format('%.0f',untilTime),'rotated_to',ARGV[6])
if untilTime<=now then redis.call('HSET',KEYS[1],'revoked','1') end
return 1
`)
