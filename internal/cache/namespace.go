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
	return NewProjectNamespace(apiKey, model, "")
}

// NewProjectNamespace scopes reusable responses to an authenticated project rather
// than a raw client key. corpusVersion changes whenever that project's RAG corpus
// changes, so answers produced from removed or replaced context are never replayed.
func NewProjectNamespace(projectID, model, corpusVersion string) Namespace {
	sum := sha256.Sum256([]byte(projectID))
	version := SchemaVersion
	if corpusVersion != "" {
		version += ":" + corpusVersion
	}
	return Namespace{
		Tenant:  hex.EncodeToString(sum[:]),
		Model:   model,
		Version: version,
	}
}
