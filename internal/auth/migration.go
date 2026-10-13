package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

func validFingerprint(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == sha256.Size && id == strings.ToLower(id)
}

func validateLegacyKey(key string, record map[string]string) error {
	if !strings.HasPrefix(key, projectKeyPrefix) || !validFingerprint(strings.TrimPrefix(key, projectKeyPrefix)) || record["project"] == "" {
		return fmt.Errorf("invalid legacy key identity")
	}
	for field, value := range record {
		switch field {
		case "project":
		case "revoked":
			if value != "0" && value != "1" {
				return fmt.Errorf("invalid legacy key revocation")
			}
		case "created_at", "expires_at":
			if value == "" || strings.Trim(value, "0123456789") != "" {
				return fmt.Errorf("invalid legacy key timestamp")
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n <= 0 || n > 9007199254740991 {
				return fmt.Errorf("invalid legacy key timestamp")
			}
		case "rotated_from", "rotated_to":
			if !validFingerprint(value) {
				return fmt.Errorf("invalid legacy key rotation")
			}
		default:
			return fmt.Errorf("unknown legacy key field %q", field)
		}
	}
	return nil
}

func keyMigrationReceipt(record map[string]string) (string, string) {
	project := sha256.Sum256([]byte(record["project"]))
	data, _ := json.Marshal(record)
	digest := sha256.Sum256(data)
	return "ledger:{" + hex.EncodeToString(project[:]) + "}:key-migrations", hex.EncodeToString(digest[:])
}

func (s *KeyStore) legacyKeys(ctx context.Context, source *redis.Client, visit func(string, map[string]string) error) error {
	if source == nil || len(s.pepper) == 0 {
		return fmt.Errorf("legacy source and API_KEY_PEPPER are required")
	}
	iter := source.Scan(ctx, 0, projectKeyPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		record, err := source.HGetAll(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("read legacy key %s: %w", key, err)
		}
		if err := validateLegacyKey(key, record); err != nil {
			return fmt.Errorf("legacy key %s: %w", key, err)
		}
		if err := visit(key, record); err != nil {
			return fmt.Errorf("legacy key %s: %w", key, err)
		}
	}
	return iter.Err()
}

// MigrateLegacy copies fingerprints only; an immutable receipt permits later key rotation or revocation.
func (s *KeyStore) MigrateLegacy(ctx context.Context, source *redis.Client) error {
	return s.legacyKeys(ctx, source, func(key string, record map[string]string) error {
		receipt, digest := keyMigrationReceipt(record)
		fields := make([]string, 0, len(record))
		for field := range record {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		args := []any{record["project"], digest, strings.TrimPrefix(key, projectKeyPrefix)}
		for _, field := range fields {
			args = append(args, field, record[field])
		}
		return migrateKeyScript.Run(ctx, s.client, []string{key, receipt}, args...).Err()
	})
}

// CheckLegacyKeys prevents startup from silently losing old keys after moving authentication stores.
func (s *KeyStore) CheckLegacyKeys(ctx context.Context, source *redis.Client) error {
	return s.legacyKeys(ctx, source, func(key string, record map[string]string) error {
		receipt, digest := keyMigrationReceipt(record)
		id := strings.TrimPrefix(key, projectKeyPrefix)
		got, err := s.client.HGet(ctx, receipt, id).Result()
		if err == redis.Nil {
			return fmt.Errorf("explicit key migration required")
		}
		if err != nil {
			return err
		}
		if got != digest {
			return fmt.Errorf("legacy key changed after migration")
		}
		project, err := s.client.HGet(ctx, key, "project").Result()
		if err != nil || project != record["project"] {
			return fmt.Errorf("migrated key project missing or mismatched")
		}
		return nil
	})
}

var migrateKeyScript = redis.NewScript(keyChecks + `
check(KEYS[1]);check(KEYS[2])
local receipt=redis.call('HGET',KEYS[2],ARGV[3])
if receipt then
 if receipt~=ARGV[2] then error('legacy key changed after migration') end
 if redis.call('HGET',KEYS[1],'project')~=ARGV[1] then error('migrated key project missing or mismatched') end
 return 0
end
if redis.call('EXISTS',KEYS[1])==1 then
 if redis.call('HLEN',KEYS[1])~=(#ARGV-3)/2 then error('conflicting existing key record') end
 for i=4,#ARGV,2 do
  if redis.call('HGET',KEYS[1],ARGV[i])~=ARGV[i+1] then error('conflicting existing key record') end
 end
else
 redis.call('HSET',KEYS[1],unpack(ARGV,4))
end
redis.call('HSET',KEYS[2],ARGV[3],ARGV[2]);return 1
`)
