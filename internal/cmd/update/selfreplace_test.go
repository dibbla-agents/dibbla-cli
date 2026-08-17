package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/update"
)

func TestAssetName(t *testing.T) {
	got := AssetName("v1.2.3")
	wantPrefix := "dibbla_1.2.3_" + runtime.GOOS + "_" + runtime.GOARCH
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("AssetName = %q, want prefix %q", got, wantPrefix)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(got, ".zip") {
			t.Errorf("expected .zip suffix on windows, got %q", got)
		}
	} else {
		if !strings.HasSuffix(got, ".tar.gz") {
			t.Errorf("expected .tar.gz suffix, got %q", got)
		}
	}
}

func TestLookupChecksum(t *testing.T) {
	body := []byte("abc123  somefile.tar.gz\n" +
		"deadbeef  *another.zip\n" +
		"# a comment line\n" +
		"feedface  third\n")

	got, ok := lookupChecksum(body, "somefile.tar.gz")
	if !ok || got != "abc123" {
		t.Errorf("got (%q,%v), want (abc123,true)", got, ok)
	}

	got, ok = lookupChecksum(body, "another.zip")
	if !ok || got != "deadbeef" {
		t.Errorf("got (%q,%v), want (deadbeef,true)", got, ok)
	}

	if _, ok := lookupChecksum(body, "missing"); ok {
		t.Error("expected lookup to fail for missing entry")
	}
}

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256(content)
	want := hex.EncodeToString(h[:])

	got, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("sha256File = %q, want %q", got, want)
	}
}

func TestExtractFromTarGz(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz path is not used on windows")
	}
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.tar.gz")
	want := []byte("FAKE-DIBBLA-BINARY")
	writeTarGz(t, archivePath, "dibbla", want)

	r, cleanup, err := openBinaryFromArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted bytes mismatch: got %q want %q", got, want)
	}
}

func TestExtractFromZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.zip")
	want := []byte("FAKE-DIBBLA-BINARY")
	binaryName := "dibbla"
	if runtime.GOOS == "windows" {
		binaryName = "dibbla.exe"
	}
	writeZip(t, archivePath, binaryName, want)

	if runtime.GOOS != "windows" {
		// On non-windows openBinaryFromArchive picks tar.gz by suffix;
		// drive the zip path directly.
		r, cleanup, err := openFromZip(archivePath, binaryName)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("zip extracted bytes mismatch: got %q want %q", got, want)
		}
		return
	}

	r, cleanup, err := openBinaryFromArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, want) {
		t.Errorf("extracted bytes mismatch")
	}
}

func TestExtractMissingBinary(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.tar.gz")
	writeTarGz(t, archivePath, "not-dibbla", []byte("noise"))

	if runtime.GOOS == "windows" {
		// On windows the function would look at the .tar.gz path's extension and
		// dispatch to tar.gz too — exercise it the same way.
	}
	_, _, err := openFromTarGz(archivePath, "dibbla")
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestSelfReplace_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("happy-path test exercises tar.gz; windows uses zip and a different swap dance")
	}
	tmpHome := t.TempDir()

	// Pretend the running binary lives at $tmpHome/dibbla.
	target := filepath.Join(tmpHome, "dibbla")
	if err := os.WriteFile(target, []byte("OLD"), 0755); err != nil {
		t.Fatal(err)
	}

	// Build a fake release archive + checksums.txt.
	newBinary := []byte("NEW-BINARY-CONTENT")
	archiveBytes := mustTarGzBytes(t, "dibbla", newBinary)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(archiveBytes)
		case "/checksums":
			h := sha256.Sum256(archiveBytes)
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(h[:]), AssetName("v9.9.9"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := &update.Release{
		TagName: "v9.9.9",
		Assets: []update.Asset{
			{Name: AssetName("v9.9.9"), DownloadURL: srv.URL + "/archive"},
			{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums"},
		},
	}

	if err := SelfReplace(rel, target, "v0.0.1"); err != nil {
		t.Fatalf("SelfReplace failed: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBinary) {
		t.Errorf("target binary not replaced: got %q want %q", got, newBinary)
	}
}

func TestSelfReplace_ChecksumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("happy-path only exercised on unix")
	}
	tmpHome := t.TempDir()
	target := filepath.Join(tmpHome, "dibbla")
	original := []byte("OLD")
	if err := os.WriteFile(target, original, 0755); err != nil {
		t.Fatal(err)
	}

	archiveBytes := mustTarGzBytes(t, "dibbla", []byte("NEW"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(archiveBytes)
		case "/checksums":
			// Wrong digest on purpose.
			fmt.Fprintf(w, "%s  %s\n", strings.Repeat("0", 64), AssetName("v9.9.9"))
		}
	}))
	defer srv.Close()

	rel := &update.Release{
		TagName: "v9.9.9",
		Assets: []update.Asset{
			{Name: AssetName("v9.9.9"), DownloadURL: srv.URL + "/archive"},
			{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums"},
		},
	}

	err := SelfReplace(rel, target, "v0.0.1")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got %v", err)
	}
	got, _ := os.ReadFile(target)
	if !bytes.Equal(got, original) {
		t.Errorf("target should be untouched on checksum failure, got %q", got)
	}
}

func TestSelfReplace_MissingChecksumAsset(t *testing.T) {
	tmpHome := t.TempDir()
	target := filepath.Join(tmpHome, "dibbla")
	if err := os.WriteFile(target, []byte("OLD"), 0755); err != nil {
		t.Fatal(err)
	}

	rel := &update.Release{
		TagName: "v9.9.9",
		Assets: []update.Asset{
			{Name: AssetName("v9.9.9"), DownloadURL: "http://example.invalid/x"},
		},
	}
	err := SelfReplace(rel, target, "v0.0.1")
	if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
		t.Fatalf("expected checksums missing error, got %v", err)
	}
}

func TestSelfReplace_MissingPlatformAsset(t *testing.T) {
	tmpHome := t.TempDir()
	target := filepath.Join(tmpHome, "dibbla")
	os.WriteFile(target, []byte("OLD"), 0755)

	rel := &update.Release{
		TagName: "v9.9.9",
		Assets: []update.Asset{
			{Name: "checksums.txt", DownloadURL: "http://example.invalid/c"},
		},
	}
	err := SelfReplace(rel, target, "v0.0.1")
	if err == nil || !strings.Contains(err.Error(), "no asset for") {
		t.Fatalf("expected no-asset error, got %v", err)
	}
}

func TestCanWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x")
	os.WriteFile(path, []byte("a"), 0644)
	if !CanWrite(path) {
		t.Error("expected CanWrite=true for writable file")
	}

	// Read-only file in a writable dir: CanWrite should report TRUE.
	// Selfupdate writes a sibling tempfile and renames it over the
	// target — only the parent directory's write bit matters.
	roPath := filepath.Join(dir, "ro")
	os.WriteFile(roPath, []byte("a"), 0444)
	if !CanWrite(roPath) {
		t.Error("expected CanWrite=true for readonly file in writable dir")
	}

	// Non-existent path: report false — we have nothing to swap.
	if CanWrite(filepath.Join(dir, "missing")) {
		t.Error("expected CanWrite=false for non-existent path")
	}

	// Read-only parent directory: report false. Skip when running as
	// root (e.g. CI containers) since root bypasses the dir mode check.
	if os.Geteuid() != 0 {
		roDir := filepath.Join(dir, "rodir")
		if err := os.Mkdir(roDir, 0755); err != nil {
			t.Fatal(err)
		}
		inside := filepath.Join(roDir, "f")
		os.WriteFile(inside, []byte("a"), 0644)
		if err := os.Chmod(roDir, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(roDir, 0755) })
		if CanWrite(inside) {
			t.Error("expected CanWrite=false when parent dir is read-only")
		}
	}

	// Regression: the running test binary itself must report writable.
	// Before the ETXTBSY fix, CanWrite probed the target with O_WRONLY,
	// which on Linux fails for any running ELF — meaning `dibbla
	// update` always refused to replace itself.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !CanWrite(exe) {
		t.Errorf("expected CanWrite=true for running executable %s", exe)
	}
}

// --- helpers ---

func writeTarGz(t *testing.T, path, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, mustTarGzBytes(t, name, body), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustTarGzBytes(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name: name,
		Mode: 0755,
		Size: int64(len(body)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func writeZip(t *testing.T, path, name string, body []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(body)
	zw.Close()
}

// --- P-0020: verification via the inline digest from latest.json ---

// The mirror path verifies without a second HTTP request. The assertion that
// matters is not just "it worked" but that checksums.txt was never fetched —
// one fewer request is the whole benefit, and a silent fallback would hide a
// regression.
func TestSelfReplace_PrefersInlineDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("happy-path test exercises tar.gz; windows uses zip")
	}
	tmpHome := t.TempDir()
	target := filepath.Join(tmpHome, "dibbla")
	if err := os.WriteFile(target, []byte("OLD"), 0755); err != nil {
		t.Fatal(err)
	}

	newBinary := []byte("NEW-BINARY-CONTENT")
	archiveBytes := mustTarGzBytes(t, "dibbla", newBinary)
	sum := sha256.Sum256(archiveBytes)

	checksumsFetched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(archiveBytes)
		case "/checksums":
			checksumsFetched = true
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := &update.Release{
		TagName: "v9.9.9",
		Assets: []update.Asset{
			{
				Name:        AssetName("v9.9.9"),
				DownloadURL: srv.URL + "/archive",
				Digest:      "sha256:" + hex.EncodeToString(sum[:]),
			},
			{Name: "checksums.txt", DownloadURL: srv.URL + "/checksums"},
		},
	}

	if err := SelfReplace(rel, target, "v0.0.1"); err != nil {
		t.Fatalf("SelfReplace failed: %v", err)
	}
	if checksumsFetched {
		t.Error("checksums.txt was fetched even though the asset carried a digest")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBinary) {
		t.Errorf("target binary not replaced: got %q want %q", got, newBinary)
	}
}

// A digest that disagrees with the bytes must abort and leave the running
// binary untouched — the mirror is a new origin, and this is the check that
// makes trusting it reasonable.
func TestSelfReplace_InlineDigestMismatchLeavesBinaryIntact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("happy-path only exercised on unix")
	}
	tmpHome := t.TempDir()
	target := filepath.Join(tmpHome, "dibbla")
	original := []byte("OLD")
	if err := os.WriteFile(target, original, 0755); err != nil {
		t.Fatal(err)
	}

	archiveBytes := mustTarGzBytes(t, "dibbla", []byte("NEW"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/archive" {
			w.Write(archiveBytes)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rel := &update.Release{
		TagName: "v9.9.9",
		Assets: []update.Asset{
			{
				Name:        AssetName("v9.9.9"),
				DownloadURL: srv.URL + "/archive",
				Digest:      "sha256:" + strings.Repeat("0", 64),
			},
		},
	}

	err := SelfReplace(rel, target, "v0.0.1")
	if err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error should name the mismatch, got: %v", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(got, original) {
		t.Errorf("binary was replaced despite a bad digest: got %q", got)
	}
}

// An unparseable digest is treated as absent, not as a reason to skip
// verification. With no checksums.txt to fall back on, the update must refuse.
func TestSelfReplace_MalformedDigestFallsBackAndRefuses(t *testing.T) {
	tmpHome := t.TempDir()
	target := filepath.Join(tmpHome, "dibbla")
	if err := os.WriteFile(target, []byte("OLD"), 0755); err != nil {
		t.Fatal(err)
	}

	rel := &update.Release{
		TagName: "v9.9.9",
		Assets: []update.Asset{
			{Name: AssetName("v9.9.9"), DownloadURL: "http://127.0.0.1:1/archive", Digest: "sha256:nope"},
		},
	}

	err := SelfReplace(rel, target, "v0.0.1")
	if err == nil {
		t.Fatal("expected a refusal when there is neither a usable digest nor checksums.txt")
	}
	if !strings.Contains(err.Error(), "refusing to install without verification") {
		t.Errorf("error should refuse explicitly, got: %v", err)
	}
}
