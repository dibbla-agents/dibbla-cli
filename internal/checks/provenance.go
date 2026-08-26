// Package checks validates a dibbla-checks.yaml locally, before a deploy.
//
// The normative contract for this file lives in app-hosting-service, in a
// different repository and a different Go module, so it cannot be imported.
// This package therefore carries a BYTE-IDENTICAL copy of the server's
// schema_v1.json and validates it with the same JSON Schema library the server
// uses — so only the schema bytes are duplicated, never a second reading of
// them.
//
// # The authority stays on the server
//
// The gate that actually stops a bad file is deploy-api's: it runs the loader
// after archive extraction and before the build. This package is a
// convenience, and it must not pretend otherwise. A local NO on a file the
// server would accept stops a real deploy and teaches people to ignore the
// tool, which is worse than having no tool at all. So the asymmetry here is
// deliberate: anything this copy cannot decide is reported as NOT VERIFIED
// LOCALLY, never as invalid.
//
// Duplicating the schema is acceptable where duplicating a security boundary
// would not be. This copy is a hint that helps, not a limit that protects.
package checks

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed schema_v1.json
var schemaV1 []byte

const (
	// SourceCommit is the app-hosting-service commit this copy was taken from.
	// It is printed on a SUCCESSFUL validation, not buried in help text: a
	// green answer that says where it came from is hard to over-read, and it
	// makes upstream drift visible instead of silent.
	SourceCommit = "d7793a7"

	// SourceDigest is the sha256 of the copy as vendored. A test pins it, so
	// an accidental local edit fails the build rather than quietly changing
	// what `dibbla manifest validate` accepts. It cannot detect drift upstream
	// — nothing in this repository can, since the two repos share no CI — and
	// that is exactly why SourceCommit is printed rather than assumed.
	SourceDigest = "sha256:88538ed411bf024b26bd108535032b204d13717a7128823750dda225ca40cd1a"
)

// SchemaBytes returns a copy of the vendored normative schema.
func SchemaBytes() []byte { return append([]byte(nil), schemaV1...) }

// SchemaDigest is the sha256 of the vendored schema as it exists on disk.
func SchemaDigest() string {
	sum := sha256.Sum256(schemaV1)
	return "sha256:" + hex.EncodeToString(sum[:])
}
