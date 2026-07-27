package cache

import (
	"crypto/sha256"
	"encoding/hex"
)

const SchemaVersion = "v2"

// Namespace keeps reusable answers inside one caller, routed model, and cache schema.
// Tenant is a one-way fingerprint so Redis never receives the caller's raw API key.
type Namespace struct {
	Tenant  string
	Model   string
	Version string
}

func NewNamespace(apiKey, model string) Namespace {
	sum := sha256.Sum256([]byte(apiKey))
	return Namespace{
		Tenant:  hex.EncodeToString(sum[:]),
		Model:   model,
		Version: SchemaVersion,
	}
}
