package auth

import (
	"context"
	"crypto/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func keyMigrationFixture(t *testing.T) (*KeyStore, *redis.Client, *redis.Client) {
	t.Helper()
	client, err := dial()
	if err != nil {
		t.Skip(err)
	}
	sourceOptions, destinationOptions := *client.Options(), *client.Options()
	sourceOptions.DB, destinationOptions.DB = 11, 12
	source, destination := redis.NewClient(&sourceOptions), redis.NewClient(&destinationOptions)
	t.Cleanup(func() { source.Close(); destination.Close() })
	return NewKeyStore(destination, "migration-pepper"), source, destination
}

func seedLegacyKey(t *testing.T, store *KeyStore, source, destination *redis.Client, raw string, record map[string]string) string {
	t.Helper()
	ctx := context.Background()
	key := projectKeyPrefix + store.Fingerprint(raw)
	if err := source.HSet(ctx, key, record).Err(); err != nil {
		t.Fatal(err)
	}
	receipt, _ := keyMigrationReceipt(record)
	t.Cleanup(func() {
		source.Del(ctx, key)
		destination.Del(ctx, key, receipt)
	})
	return key
}

func TestLegacyKeyMigrationPreservesMetadataAndLifecycle(t *testing.T) {
	store, source, destination := keyMigrationFixture(t)
	ctx := context.Background()
	project := "migration-project:" + rand.Text()
	live, legacy, revoked := rand.Text(), rand.Text(), rand.Text()
	replacement := rand.Text()
	records := map[string]map[string]string{
		live:    {"project": project, "revoked": "0", "expires_at": "4102444800", "created_at": "1700000000"},
		legacy:  {"project": project},
		revoked: {"project": project, "revoked": "1", "expires_at": "1700000001", "created_at": "1700000000", "rotated_from": store.Fingerprint("previous"), "rotated_to": store.Fingerprint("next")},
	}
	for raw, record := range records {
		seedLegacyKey(t, store, source, destination, raw, record)
	}
	t.Cleanup(func() { destination.Del(ctx, projectKeyPrefix+store.Fingerprint(replacement)) })
	if err := store.CheckLegacyKeys(ctx, source); err == nil {
		t.Fatal("startup accepted unmigrated keys")
	}
	if err := store.MigrateLegacy(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateLegacy(ctx, source); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
	for raw, want := range records {
		key := projectKeyPrefix + store.Fingerprint(raw)
		for _, client := range []*redis.Client{source, destination} {
			got, err := client.HGetAll(ctx, key).Result()
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("record changed: %+v, %v; want %+v", got, err, want)
			}
		}
		principal, valid, err := store.Lookup(ctx, raw)
		if err != nil || valid != (raw != revoked) || (valid && principal.ProjectID != project) {
			t.Fatalf("migrated principal = %+v, %v, %v", principal, valid, err)
		}
	}
	if err := store.Rotate(ctx, project, legacy, replacement, time.Hour, 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, project, live); err != nil {
		t.Fatal(err)
	}
	expires := destination.HGet(ctx, projectKeyPrefix+store.Fingerprint(legacy), "expires_at").Val()
	if expires == "" {
		t.Fatal("rotation did not set expiry")
	}
	if err := store.CheckLegacyKeys(ctx, source); err != nil {
		t.Fatalf("legitimate lifecycle blocked startup: %v", err)
	}
	if err := store.MigrateLegacy(ctx, source); err != nil {
		t.Fatalf("legitimate lifecycle blocked replay: %v", err)
	}
	if _, valid, err := store.Lookup(ctx, live); err != nil || valid {
		t.Fatalf("migration revived revoked key: %v, %v", valid, err)
	}
	if got := destination.HGet(ctx, projectKeyPrefix+store.Fingerprint(legacy), "expires_at").Val(); got != expires {
		t.Fatalf("migration changed rotated expiry: %q, want %q", got, expires)
	}
	for raw, want := range records {
		got := source.HGetAll(ctx, projectKeyPrefix+store.Fingerprint(raw)).Val()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("source mutated: %+v", got)
		}
	}
}

func TestLegacyKeyMigrationConflictsRemainUnchanged(t *testing.T) {
	store, source, destination := keyMigrationFixture(t)
	ctx := context.Background()
	for _, conflict := range []map[string]string{
		{"project": "another-project", "revoked": "0"},
		{"project": "migration-conflict", "revoked": "1"},
		{"project": "migration-conflict", "revoked": "0", "expires_at": "1700000000"},
	} {
		t.Run(conflict["project"]+conflict["revoked"]+conflict["expires_at"], func(t *testing.T) {
			record := map[string]string{"project": "migration-conflict", "revoked": "0"}
			key := seedLegacyKey(t, store, source, destination, rand.Text(), record)
			if err := destination.HSet(ctx, key, conflict).Err(); err != nil {
				t.Fatal(err)
			}
			if err := store.MigrateLegacy(ctx, source); err == nil {
				t.Fatal("conflicting key accepted")
			}
			if got := destination.HGetAll(ctx, key).Val(); !reflect.DeepEqual(got, conflict) {
				t.Fatalf("conflicting destination changed: %+v", got)
			}
			receipt, _ := keyMigrationReceipt(record)
			if destination.Exists(ctx, receipt).Val() != 0 {
				t.Fatal("failed migration wrote a receipt")
			}
			if got := source.HGetAll(ctx, key).Val(); !reflect.DeepEqual(got, record) {
				t.Fatalf("source changed: %+v", got)
			}
		})
	}
}

func TestLegacyKeyMigrationDetectsChangedSourceAndBinding(t *testing.T) {
	store, source, destination := keyMigrationFixture(t)
	ctx := context.Background()
	record := map[string]string{"project": "changed-migration:" + rand.Text(), "revoked": "0"}
	key := seedLegacyKey(t, store, source, destination, rand.Text(), record)
	if err := destination.HSet(ctx, key, record).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckLegacyKeys(ctx, source); err == nil {
		t.Fatal("identical destination bypassed explicit migration receipt")
	}
	if err := store.MigrateLegacy(ctx, source); err != nil {
		t.Fatalf("identical existing destination rejected: %v", err)
	}
	source.HSet(ctx, key, "revoked", "1")
	if err := store.CheckLegacyKeys(ctx, source); err == nil {
		t.Fatal("startup accepted changed legacy source")
	}
	if err := store.MigrateLegacy(ctx, source); err == nil {
		t.Fatal("migration accepted changed legacy source")
	}
	if got := destination.HGetAll(ctx, key).Val(); !reflect.DeepEqual(got, record) {
		t.Fatalf("destination changed: %+v", got)
	}
	source.HSet(ctx, key, "revoked", "0")
	destination.HSet(ctx, key, "project", "wrong-project")
	if err := store.CheckLegacyKeys(ctx, source); err == nil {
		t.Fatal("startup accepted changed project binding")
	}
	if err := store.MigrateLegacy(ctx, source); err == nil {
		t.Fatal("replay accepted changed project binding")
	}
}

func TestLegacyKeyMigrationValidatesSchemaBeforeWrites(t *testing.T) {
	store, source, destination := keyMigrationFixture(t)
	ctx := context.Background()
	for _, bad := range []map[string]string{
		{"project": ""}, {"project": "p", "revoked": "2"},
		{"project": "p", "expires_at": "0"}, {"project": "p", "created_at": "9007199254740992"},
		{"project": "p", "raw_key": "must-not-copy"}, {"project": "p", "rotated_to": "invalid"},
	} {
		t.Run(fmtRecord(bad), func(t *testing.T) {
			key := seedLegacyKey(t, store, source, destination, rand.Text(), bad)
			if err := store.MigrateLegacy(ctx, source); err == nil {
				t.Fatal("invalid legacy record accepted")
			}
			if destination.Exists(ctx, key).Val() != 0 {
				t.Fatal("invalid legacy record copied")
			}
		})
	}
	if err := validateLegacyKey(projectKeyPrefix+strings.Repeat("A", 64), map[string]string{"project": "p"}); err == nil {
		t.Fatal("noncanonical fingerprint accepted")
	}
}

func TestLegacyKeyMigrationValidatesReceiptBeforeInsertingKey(t *testing.T) {
	store, source, destination := keyMigrationFixture(t)
	ctx := context.Background()
	record := map[string]string{"project": "corrupt-receipt:" + rand.Text()}
	key := seedLegacyKey(t, store, source, destination, rand.Text(), record)
	receipt, _ := keyMigrationReceipt(record)
	if err := destination.Set(ctx, receipt, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateLegacy(ctx, source); err == nil {
		t.Fatal("invalid receipt type accepted")
	}
	if destination.Exists(ctx, key).Val() != 0 || destination.Get(ctx, receipt).Val() != "wrong-type" {
		t.Fatal("failed migration partially wrote destination")
	}
}

func fmtRecord(record map[string]string) string {
	for field := range record {
		if field != "project" {
			return field
		}
	}
	return "project"
}
