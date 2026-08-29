package platformcontract

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// The vendored contract is a copy, and a copy is only worth having if
// something falls when it stops being a copy. These tests are that something.
//
// Every negative control runs TWICE: once mutated, where the rejection must
// happen, and once unmutated, where the same predicate must NOT be satisfied.
// A control that the correct input also satisfies would stay green with the
// guard deleted, and it fails here with "does not discriminate" instead.

func TestVendoredContractMatchesLock(t *testing.T) {
	if err := Verify(); err != nil {
		t.Fatalf("the vendored contract does not match its lock: %v", err)
	}
	lock, err := VendoredLock()
	if err != nil {
		t.Fatalf("reading the vendored lock: %v", err)
	}
	if lock.ContractVersion != ContractVersion() {
		t.Errorf("contract.lock says version %q, scopes.json says %q", lock.ContractVersion, ContractVersion())
	}
	if len(Scopes()) == 0 {
		t.Fatal("the vendored scope registry is empty")
	}
}

// TestDigestIsCanonicalNotBytewise is the control that makes every digest
// assertion below mean something: reformatting must NOT move the digest, and
// one changed character must.
func TestDigestIsCanonicalNotBytewise(t *testing.T) {
	raw := VendoredFiles()["v1/scopes.json"]
	base, err := Digest(raw)
	if err != nil {
		t.Fatalf("digesting the vendored scopes: %v", err)
	}

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing the vendored scopes: %v", err)
	}
	reformatted, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		t.Fatalf("re-serialising: %v", err)
	}
	if string(reformatted) == string(raw) {
		t.Fatal("this control does not discriminate: the re-serialised document is byte-identical to the vendored one")
	}
	got, err := Digest(reformatted)
	if err != nil {
		t.Fatalf("digesting the reformatted document: %v", err)
	}
	if got != base {
		t.Errorf("reformatting moved the digest, so it is not canonical:\n  vendored    %s\n  reformatted %s", base, got)
	}

	tampered := mutate(t, raw, func(doc map[string]any) {
		doc["description"] = doc["description"].(string) + "."
	})
	got, err = Digest(tampered)
	if err != nil {
		t.Fatalf("digesting the tampered document: %v", err)
	}
	if got == base {
		t.Error("a changed description did not move the digest")
	}
}

func TestVerifyFallsOnEveryDirectionOfDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, files map[string][]byte, lock *Lock)
		want   string
	}{
		{
			// The canonical contract moved and this copy did not. Seen from
			// here that is indistinguishable from a local edit; the message
			// says both.
			name: "the canonical contract moved and the lock came with it",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				lock.Files["v1/scopes.json"] = "sha256:" + strings.Repeat("0", 64)
				lock.ContractDigest = recomputeContractDigest(t, lock.Files)
			},
			want: "does not match contract.lock",
		},
		{
			// Somebody edited the copy here instead of re-vendoring it.
			name: "a vendored document is edited locally",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				files["v1/scopes.json"] = mutate(t, files["v1/scopes.json"], func(doc map[string]any) {
					scopes := doc["scopes"].([]any)
					scopes[0].(map[string]any)["summary"] = "widened by hand"
				})
			},
			want: "v1/scopes.json does not match contract.lock",
		},
		{
			// An UNRELATED field. A gate that only watches the fields its own
			// caller reads is a gate on today's usage, not on the contract.
			name: "an unrelated field is sabotaged",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				files["v1/errors.json"] = mutate(t, files["v1/errors.json"], func(doc map[string]any) {
					doc["description"] = "sabotaged"
				})
			},
			want: "v1/errors.json does not match contract.lock",
		},
		{
			name: "a capability changes state",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				files["v1/capabilities.json"] = mutate(t, files["v1/capabilities.json"], func(doc map[string]any) {
					caps := doc["capabilities"].([]any)
					caps[0].(map[string]any)["state"] = "remote-destructive"
				})
			},
			want: "v1/capabilities.json does not match contract.lock",
		},
		{
			// An entry REMOVED from the source. A test that iterates the list
			// it is checking loses its assertion along with the entry.
			name: "an entry is removed from the registry",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				files["v1/scopes.json"] = mutate(t, files["v1/scopes.json"], func(doc map[string]any) {
					scopes := doc["scopes"].([]any)
					doc["scopes"] = scopes[:len(scopes)-1]
				})
			},
			want: "v1/scopes.json does not match contract.lock",
		},
		{
			name: "an entry is added to the registry",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				files["v1/scopes.json"] = mutate(t, files["v1/scopes.json"], func(doc map[string]any) {
					doc["scopes"] = append(doc["scopes"].([]any), map[string]any{
						"name":                            "platform:admin:everything", //contract-pinned: smuggled scope that must never resolve
						"class":                           "destructive",
						"summary":                         "smuggled in",
						"granted_by_first_public_toolset": true,
						"consent_default":                 true,
						"static_token_eligible":           true,
					})
				})
			},
			want: "v1/scopes.json does not match contract.lock",
		},
		{
			// The per-file digests can be made to agree with a tampered file
			// by editing the lock too. The contract_digest over the file map
			// is what makes that not enough.
			name: "the lock is edited to agree with a tampered document",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				files["v1/capabilities.json"] = mutate(t, files["v1/capabilities.json"], func(doc map[string]any) {
					doc["description"] = "sabotaged"
				})
				d, err := Digest(files["v1/capabilities.json"])
				if err != nil {
					t.Fatalf("digesting the tampered capabilities: %v", err)
				}
				lock.Files["v1/capabilities.json"] = d
			},
			want: "contract_digest does not match",
		},
		{
			name: "a vendored document is dropped",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				delete(files, "v1/errors.json")
			},
			want: "a document was added or removed without being locked",
		},
		{
			name: "the lock names a document that is not vendored",
			mutate: func(t *testing.T, files map[string][]byte, lock *Lock) {
				delete(files, "v1/errors.json")
				files["v1/somethingelse.json"] = []byte(`{}`)
			},
			want: "contract.lock names v1/errors.json but it is not vendored",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := verify(VendoredFiles(), mustLock(t)); err != nil {
				t.Fatalf("this control does not discriminate: the unmutated contract already fails verification (%v)", err)
			}

			files := VendoredFiles()
			lock := mustLock(t)
			tc.mutate(t, files, &lock)

			err := verify(files, lock)
			if err == nil {
				t.Fatalf("mutation was not rejected: verify() accepted the sabotaged contract")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("rejected for the wrong reason:\n  want substring %q\n  got            %v", tc.want, err)
			}
		})
	}
}

// TestCanonicalDigestMatchesTheCheckedInConstants pins the canonicalisation
// against literals, not against the vendored lock.
//
// Every other test compares one computed value with another, or with a value
// a re-vendoring would replace. If the Go canonicalisation drifted from
// Python's, those comparisons would move together and stay green. These
// constants were copied out of architecture/docs/contract/contract.lock by
// hand on 2026-08-29; they are what the canonical verifier produced, and they
// are the same four literals app-hosting-service and dibbla-docs pin.
//
// If this falls right after a re-vendoring, the constants are stale — replace
// them from the new contract.lock. If it falls and nothing was re-vendored,
// the canonicalisation broke, and that is the case this test exists for.
func TestCanonicalDigestMatchesTheCheckedInConstants(t *testing.T) {
	const (
		scopesDigest       = "sha256:1c6c4f9271b3837d11af62676803dca4804d3c9b8520078fe2933ee41155d270"
		errorsDigest       = "sha256:f2b8aa2e65e6e7ae71c095dc1ba62ab060b85faa3d0d9a2632a1878f6691563e"
		capabilitiesDigest = "sha256:3b79cfd8deff07fe35c82d7fe787e39225e33a3996274117656e47623355b086"
		contractDigest     = "sha256:ae778fdffa93b8ef6ec03c7185108c5715c53060c6f9cce9c4ac4e91b3de2359"
	)
	want := map[string]string{
		"v1/scopes.json":       scopesDigest,
		"v1/errors.json":       errorsDigest,
		"v1/capabilities.json": capabilitiesDigest,
	}

	for name, raw := range VendoredFiles() {
		got, err := Digest(raw)
		if err != nil {
			t.Fatalf("digesting %s: %v", name, err)
		}
		if got != want[name] {
			t.Errorf("canonical digest of %s does not match the constant:\n  constant %s\n  computed %s",
				name, want[name], got)
		}
	}

	got, err := digestValue(want)
	if err != nil {
		t.Fatalf("digesting the file map: %v", err)
	}
	if got != contractDigest {
		t.Errorf("contract digest does not match the constant:\n  constant %s\n  computed %s", contractDigest, got)
	}

	lock := mustLock(t)
	if lock.ContractDigest != contractDigest {
		t.Errorf("the vendored lock carries %s, this test pins %s: re-vendored without updating the constants, or vice versa",
			lock.ContractDigest, contractDigest)
	}
}

// TestRegistryNamesEveryScopeExplicitly is the assertion that survives a
// deletion. A re-vendoring brings a new lock with it, so every digest stays
// green while a scope has ceased to exist; this list, written by hand, does
// not. Each line is a pinned control and must not be derived from the
// registry it pins.
func TestRegistryNamesEveryScopeExplicitly(t *testing.T) {
	expected := []string{
		"platform:identity:read",              //contract-pinned: hand-written inventory
		"platform:apps:read",                  //contract-pinned: hand-written inventory
		"platform:apps:write",                 //contract-pinned: hand-written inventory
		"platform:apps:restart",               //contract-pinned: hand-written inventory
		"platform:apps:delete",                //contract-pinned: hand-written inventory
		"platform:deployments:read",           //contract-pinned: hand-written inventory
		"platform:deployment-proposals:write", //contract-pinned: hand-written inventory
		"platform:deployments:execute",        //contract-pinned: hand-written inventory
		"platform:logs:read",                  //contract-pinned: hand-written inventory
		"platform:secrets:metadata:read",      //contract-pinned: hand-written inventory
		"platform:secrets:write",              //contract-pinned: hand-written inventory
		"platform:databases:read",             //contract-pinned: hand-written inventory
		"platform:databases:write",            //contract-pinned: hand-written inventory
		"platform:databases:restore",          //contract-pinned: hand-written inventory
		"platform:databases:delete",           //contract-pinned: hand-written inventory
		"platform:storage:read",               //contract-pinned: hand-written inventory
		"platform:storage:write",              //contract-pinned: hand-written inventory
		"platform:storage:rotate",             //contract-pinned: hand-written inventory
		"platform:storage:delete",             //contract-pinned: hand-written inventory
		"platform:workflows:read",             //contract-pinned: hand-written inventory
		"platform:workflows:write",            //contract-pinned: hand-written inventory
		"platform:workflows:execute",          //contract-pinned: hand-written inventory
		"platform:files:read",                 //contract-pinned: hand-written inventory
		"platform:files:write",                //contract-pinned: hand-written inventory
		"platform:files:delete",               //contract-pinned: hand-written inventory
		"platform:operations:read",            //contract-pinned: hand-written inventory
		"platform:operations:cancel",          //contract-pinned: hand-written inventory
	}

	var got []string
	for _, s := range Scopes() {
		got = append(got, s.Name)
	}
	want := append([]string(nil), expected...)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Errorf("the scope registry is not the list this test names.\n  missing here: %v\n  missing there: %v",
			missing(got, want), missing(want, got))
	}
	for _, name := range expected {
		if _, ok := LookupScope(name); !ok {
			t.Errorf("LookupScope(%q) says it does not exist", name)
		}
	}
	if _, ok := LookupScope("platform:apps:sudo"); ok { //contract-pinned: absent from the registry by design
		t.Error("LookupScope invented a scope that is not in the registry")
	}
}

// --- helpers -----------------------------------------------------------------

func mustLock(t *testing.T) Lock {
	t.Helper()
	lock, err := VendoredLock()
	if err != nil {
		t.Fatalf("reading the vendored lock: %v", err)
	}
	files := make(map[string]string, len(lock.Files))
	for k, v := range lock.Files {
		files[k] = v
	}
	lock.Files = files
	return lock
}

// mutate parses a document, hands it to fn, and re-serialises. The result is
// deliberately NOT canonical — Digest canonicalises, and a test that fed it
// canonical bytes would not be exercising that.
func mutate(t *testing.T, raw []byte, fn func(map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing document for mutation: %v", err)
	}
	fn(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("re-serialising mutated document: %v", err)
	}
	return out
}

func recomputeContractDigest(t *testing.T, files map[string]string) string {
	t.Helper()
	d, err := digestValue(files)
	if err != nil {
		t.Fatalf("digesting the file map: %v", err)
	}
	return d
}

func missing(from, want []string) []string {
	have := make(map[string]bool, len(from))
	for _, s := range from {
		have[s] = true
	}
	out := []string{}
	for _, s := range want {
		if !have[s] {
			out = append(out, s)
		}
	}
	return out
}
