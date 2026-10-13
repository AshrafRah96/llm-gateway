package main

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateInitialization(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := initialize(dir); err != nil {
		t.Fatal(err)
	}
	if err := initialize(dir); err == nil {
		t.Fatal("existing material overwritten")
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("private directory: %v %v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ledger.crt"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("ledger"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "ledger_gateway"))
	if err != nil {
		t.Fatal(err)
	}
	acl, err := os.ReadFile(filepath.Join(dir, "ledger.acl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cache.acl", "ledger.acl"} {
		generated, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(generated), "%!") {
			t.Fatalf("invalid formatted ACL %s", name)
		}
	}
	if strings.Contains(string(acl), string(raw)) {
		t.Fatal("plaintext credential stored in ACL")
	}
	if !strings.Contains(string(acl), "%R~project_key:*") {
		t.Fatal("runtime auth metadata not read-only")
	}
}

func TestConcurrentInitializationHasOneOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "private")
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- initialize(dir) }()
	}
	success := 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("initialization owners=%d, want exactly one", success)
	}
	for _, name := range []string{"cache.acl", "ledger.acl", "pepper", "ca.crt"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(raw) == 0 {
			t.Fatalf("partial material %s: %v", name, err)
		}
	}
}
