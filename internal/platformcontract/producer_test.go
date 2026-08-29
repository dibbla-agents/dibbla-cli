package platformcontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The single-producer rule, the CLI leg of the P-0035 S11 matrix gate.
//
// Identical rendering across the four surfaces is guaranteed by construction
// when every surface reads the same vendored contract, and cannot be
// guaranteed at all when one of them keeps its own list. So no production Go
// file in dibbla-cli may carry a "platform:…" scope literal; a command reads
// the scope through LookupScope. Tests may carry one per line with a
// //contract-pinned: <why> marker, because a pinned control must not be
// derived from the thing it pins.
//
// The same rule, with the same marker, gates app-hosting-service.
const (
	pinnedMarker       = "//contract-pinned:"
	scopeLiteralPrefix = `"platform:`
	// minimumScannedFiles guards against a walker that matches nothing — a
	// moved directory reports the same green as a clean repository.
	minimumScannedFiles = 40
)

func TestScopeRegistryHasExactlyOneProducer(t *testing.T) {
	root := filepath.Join("..", "..")
	scanned := 0
	var findings []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if d.Name() == "platformcontract" || d.Name() == "node_modules" ||
				d.Name() == "installer-site" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		scanned++
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		isTest := strings.HasSuffix(path, "_test.go")
		for i, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, scopeLiteralPrefix) {
				continue
			}
			if isTest && strings.Contains(line, pinnedMarker) {
				continue
			}
			findings = append(findings, path+":"+itoa(i+1)+"  "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if scanned < minimumScannedFiles {
		t.Fatalf("scanned only %d Go files (minimum %d): the scan root matched almost nothing, so this gate proved nothing while reporting green", scanned, minimumScannedFiles)
	}
	for _, f := range findings {
		t.Errorf("scope literal outside the vendored contract — read it through LookupScope, or mark a deliberate test pin with %s <why>:\n    %s", pinnedMarker, f)
	}
}

// The scanner must find a planted literal AND leave a pinned one alone; a
// gate that flags everything passes every test that only checks flagging.
func TestScannerDiscriminates(t *testing.T) {
	flag := func(line string, isTest bool) bool {
		return strings.Contains(line, scopeLiteralPrefix) && !(isTest && strings.Contains(line, pinnedMarker))
	}
	cases := []struct {
		name   string
		line   string
		isTest bool
		want   bool
	}{
		{"production literal", `	scopes := []string{"platform:apps:read"}`, false, true},
		{"production literal with the marker still fails", `	s := "platform:apps:read" ` + pinnedMarker + ` no`, false, true},
		{"unpinned test fixture", `	Scopes: "platform:identity:read",`, true, true},
		{"pinned test control", `	"platform:apps:sudo", ` + pinnedMarker + ` absent by design`, true, false},
		{"unrelated string", `	msg := "platform is not a scope"`, false, false},
		{"a comment naming a scope", `	// scopes look like platform:apps:read`, false, false},
		{"empty line", ``, false, false},
	}
	for _, tc := range cases {
		if got := flag(tc.line, tc.isTest); got != tc.want {
			t.Errorf("%s: flagged=%v, want %v\n    %s", tc.name, got, tc.want, tc.line)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
