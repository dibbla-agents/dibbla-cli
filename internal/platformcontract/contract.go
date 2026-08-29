// Package platformcontract is dibbla-cli's vendored copy of the canonical
// Dibbla platform capability contract, which lives in the architecture
// repository at docs/contract/ (P-0035 Part A).
//
// WHY A COPY. The contract is the one answer to "what can Dibbla do remotely,
// and under what authority", and it has to be the SAME answer at four surfaces
// — the API, the CLI, MCP and the docs. Four surfaces that each describe
// themselves drift, and the drift is invisible until a customer finds it. The
// canonical files are docs-only and have no CI, so each consumer vendors v1/
// plus contract.lock and fails its OWN build when its copy drifts. A test that
// falls in the repo that drifted falls where somebody can fix it. This package
// is the CLI's leg of that matrix (P-0035 S11); app-hosting-service and
// dibbla-docs carry the same gate.
//
// WHAT THIS PACKAGE GATES, AND WHAT IT DOES NOT. It proves the vendored bytes
// still produce the digests recorded in the vendored contract.lock, and it pins
// those digests as literals so a broken canonicalisation cannot hide behind a
// re-vendored lock. It cannot see the architecture repository, so it cannot
// prove the vendored copy still matches the canonical one; re-vendoring is a
// deliberate act (scripts/vendor-contract.sh) and the lock travels with it.
//
// Nothing else in dibbla-cli may embed or hand-write scope names: this is the
// one producer. A command that needs a scope reads it through LookupScope.
package platformcontract

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed v1/scopes.json
var scopesJSON []byte

//go:embed v1/errors.json
var errorsJSON []byte

//go:embed v1/capabilities.json
var capabilitiesJSON []byte

//go:embed contract.lock
var lockJSON []byte

// VendoredFiles is every contract document this package embeds, keyed by the
// path contract.lock uses. The digest check and the tests walk this map rather
// than a literal list, so a fourth document cannot be added without being
// covered.
func VendoredFiles() map[string][]byte {
	return map[string][]byte{
		"v1/scopes.json":       scopesJSON,
		"v1/errors.json":       errorsJSON,
		"v1/capabilities.json": capabilitiesJSON,
	}
}

// Lock is the vendored contract.lock.
type Lock struct {
	ContractVersion string            `json:"contract_version"`
	Files           map[string]string `json:"files"`
	ContractDigest  string            `json:"contract_digest"`
}

// VendoredLock returns the vendored lock.
func VendoredLock() (Lock, error) {
	var l Lock
	if err := json.Unmarshal(lockJSON, &l); err != nil {
		return Lock{}, fmt.Errorf("vendored contract.lock is not valid JSON: %w", err)
	}
	return l, nil
}

// Verify recomputes every digest from the embedded bytes and compares it with
// the vendored lock. It is what a hand-edit of a vendored file falls on.
//
// THE DIGEST IS OVER CANONICAL JSON, NOT OVER THE FILE'S BYTES. The canonical
// verifier (architecture/docs/contract/verify.py) hashes
// json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False),
// so a gate that hashed the file would go red on a reformatted-but-identical
// contract and green on nothing that should be red. Reproduce the
// canonicalisation; do not hash the file.
func Verify() error {
	lock, err := VendoredLock()
	if err != nil {
		return err
	}
	return verify(VendoredFiles(), lock)
}

// verify is Verify with its inputs passed in, so the tests can sabotage a
// vendored document without rewriting an embedded file. The exported wrapper
// above is the only production caller and always passes the real ones.
func verify(files map[string][]byte, lock Lock) error {
	if len(lock.Files) != len(files) {
		return fmt.Errorf("contract.lock names %d files, %d are vendored: a document was added or removed without being locked",
			len(lock.Files), len(files))
	}

	// Walk the LOCK, not the vendored files: an entry that the lock names and
	// this package does not embed has to be an error, and iterating the
	// embedded side would simply not notice it.
	computed := make(map[string]string, len(lock.Files))
	for name, want := range lock.Files {
		raw, ok := files[name]
		if !ok {
			return fmt.Errorf("contract.lock names %s but it is not vendored", name)
		}
		got, err := Digest(raw)
		if err != nil {
			return fmt.Errorf("digesting %s: %w", name, err)
		}
		computed[name] = got
		if got != want {
			return fmt.Errorf("vendored %s does not match contract.lock:\n  lock     %s\n  vendored %s\n"+
				"Either the file was edited here — re-vendor it with scripts/vendor-contract.sh instead — "+
				"or the canonical contract moved and this copy has not been updated", name, want, got)
		}
	}

	contractDigest, err := digestValue(computed)
	if err != nil {
		return fmt.Errorf("digesting the file map: %w", err)
	}
	if contractDigest != lock.ContractDigest {
		return fmt.Errorf("contract_digest does not match:\n  lock     %s\n  computed %s",
			lock.ContractDigest, contractDigest)
	}
	return nil
}

// Digest is the canonical digest of one contract document, given its raw
// bytes: sha256 over the canonical JSON serialisation, hex, "sha256:"-prefixed.
func Digest(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("not valid JSON: %w", err)
	}
	if dec.More() {
		return "", errors.New("trailing data after the JSON document")
	}
	return digestValue(v)
}

func digestValue(v any) (string, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// writeCanonical emits the byte-for-byte equivalent of Python's
// json.dumps(v, sort_keys=True, separators=(",", ":"), ensure_ascii=False).
//
//   - sort_keys: object key order must not affect the digest, or re-saving the
//     file from a different tool would look like a contract change.
//   - compact separators: no incidental whitespace in the hashed form.
//   - ensure_ascii=False: non-ASCII characters stay as literal UTF-8. Go's
//     encoding/json would emit them literally too, but it also escapes <, >
//     and & as < and friends, which Python does not — so json.Marshal is
//     NOT a drop-in here.
func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		writeCanonicalString(buf, t)
	case json.Number:
		// Python re-serialises an int as its decimal form and a float via
		// repr(); matching repr() in Go is a real piece of work. The contract
		// contains no numbers today, so integers pass through and anything
		// else stops loudly rather than silently disagreeing.
		s := t.String()
		if strings.ContainsAny(s, ".eE") {
			return fmt.Errorf("canonical JSON: non-integer number %s is not supported; "+
				"Python's repr() float formatting must be implemented here before the contract may contain one", s)
		}
		buf.WriteString(s)
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// Python sorts str keys by code point; Go sorts valid UTF-8 by byte,
		// which is the same order.
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonicalString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case map[string]string:
		// The file map in contract.lock, digested as-is.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonicalString(buf, k)
			buf.WriteByte(':')
			writeCanonicalString(buf, t[k])
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical JSON: unsupported value of type %T", v)
	}
	return nil
}

// writeCanonicalString reproduces Python's json string escaping with
// ensure_ascii=False: quote and backslash escaped, the five C-style shortcuts,
// every other control character as \u00xx, and everything else literal.
func writeCanonicalString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
				continue
			}
			if r == utf8.RuneError {
				buf.WriteRune(utf8.RuneError)
				continue
			}
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
}

// --- typed access ------------------------------------------------------------

// Scope is one entry of v1/scopes.json.
type Scope struct {
	Name                        string `json:"name"`
	Class                       string `json:"class"`
	Summary                     string `json:"summary"`
	GrantedByFirstPublicToolset bool   `json:"granted_by_first_public_toolset"`
	ConsentDefault              bool   `json:"consent_default"`
	StaticTokenEligible         bool   `json:"static_token_eligible"`
	ReservedReason              string `json:"reserved_reason,omitempty"`
}

type scopesDoc struct {
	ContractVersion string  `json:"contract_version"`
	Scopes          []Scope `json:"scopes"`
}

var (
	loadOnce sync.Once
	loaded   scopesDoc
	loadErr  error
)

func load() (scopesDoc, error) {
	loadOnce.Do(func() {
		loadErr = json.Unmarshal(scopesJSON, &loaded)
	})
	return loaded, loadErr
}

// ContractVersion is the contract_version the vendored scopes document carries.
func ContractVersion() string {
	doc, err := load()
	if err != nil {
		return ""
	}
	return doc.ContractVersion
}

// Scopes returns every scope in the vendored registry, in file order.
func Scopes() []Scope {
	doc, err := load()
	if err != nil {
		return nil
	}
	return append([]Scope(nil), doc.Scopes...)
}

// LookupScope resolves a scope name against the vendored registry. It is the
// only way production code in dibbla-cli may obtain a scope name.
func LookupScope(name string) (Scope, bool) {
	doc, err := load()
	if err != nil {
		return Scope{}, false
	}
	for _, s := range doc.Scopes {
		if s.Name == name {
			return s, true
		}
	}
	return Scope{}, false
}

// Capability is one entry of v1/capabilities.json: an operation and the single
// state it holds in the connector.
type Capability struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	State      string   `json:"state"`
	Scopes     []string `json:"scopes"`
	Auth       string   `json:"auth,omitempty"`
	MCPToolset string   `json:"mcp_toolset,omitempty"`
	MCPTool    string   `json:"mcp_tool,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Note       string   `json:"note,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// Capability states, as the contract spells them.
const (
	StateRemoteRead        = "remote-read"
	StateRemoteWrite       = "remote-write"
	StateRemoteDestructive = "remote-destructive"
	StateRemoteAsync       = "remote-async"
	StateLocalOnly         = "local-only"
	StateNotYetAvailable   = "not-yet-available"
)

type capabilitiesDoc struct {
	ContractVersion string       `json:"contract_version"`
	Capabilities    []Capability `json:"capabilities"`
}

var (
	capOnce   sync.Once
	capLoaded capabilitiesDoc
	capErr    error
)

func loadCapabilities() (capabilitiesDoc, error) {
	capOnce.Do(func() {
		capErr = json.Unmarshal(capabilitiesJSON, &capLoaded)
	})
	return capLoaded, capErr
}

// Capabilities returns every capability in the vendored contract, in file
// order.
func Capabilities() []Capability {
	doc, err := loadCapabilities()
	if err != nil {
		return nil
	}
	return append([]Capability(nil), doc.Capabilities...)
}

// CapabilitiesInState returns the capabilities holding one state, in file
// order. It is how a command that must describe the connector's boundary —
// what is deliberately never remote, what is not yet remote — reads it from
// the contract instead of keeping a list of its own.
func CapabilitiesInState(state string) []Capability {
	var out []Capability
	for _, c := range Capabilities() {
		if c.State == state {
			out = append(out, c)
		}
	}
	return out
}
