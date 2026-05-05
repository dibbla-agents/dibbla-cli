package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/update"
	"github.com/minio/selfupdate"
)

// httpClient is the client used for asset downloads. Overridable in tests.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// AssetName returns the goreleaser archive name for the running platform
// at the given version (with or without leading "v").
//
// MUST stay in sync with .goreleaser.yml's `archives.name_template`:
//
//	dibbla_{{ .Version }}_{{ .Os }}_{{ .Arch }}
//
// Format overrides: tar.gz everywhere except windows (zip).
func AssetName(version string) string {
	v := strings.TrimPrefix(version, "v")
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("dibbla_%s_%s_%s.%s", v, runtime.GOOS, runtime.GOARCH, ext)
}

// SelfReplace performs the full download → verify → swap flow.
// targetPath is the (already symlink-resolved) path to overwrite.
func SelfReplace(rel *update.Release, targetPath, currentVersion string) error {
	asset := rel.FindAsset(AssetName(rel.TagName))
	if asset == nil {
		return fmt.Errorf("release %s has no asset for %s/%s (looked for %s)",
			rel.TagName, runtime.GOOS, runtime.GOARCH, AssetName(rel.TagName))
	}
	checksums := rel.ChecksumAsset()
	if checksums == nil {
		return fmt.Errorf("release %s has no checksums.txt — refusing to install without verification", rel.TagName)
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(targetPath), ".dibbla-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir next to %s: %w", targetPath, err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, asset.Name)
	if err := downloadFile(asset.DownloadURL, archivePath, currentVersion); err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	checksumsBody, err := downloadBytes(checksums.DownloadURL, currentVersion)
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}

	expected, ok := lookupChecksum(checksumsBody, asset.Name)
	if !ok {
		return fmt.Errorf("checksums.txt missing entry for %s", asset.Name)
	}
	got, err := sha256File(archivePath)
	if err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	if got != expected {
		return fmt.Errorf("checksum mismatch for %s: got %s, expected %s", asset.Name, got, expected)
	}

	binaryReader, cleanup, err := openBinaryFromArchive(archivePath)
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}
	defer cleanup()

	if err := selfupdate.Apply(binaryReader, selfupdate.Options{TargetPath: targetPath}); err != nil {
		// If selfupdate aborted mid-swap it tries to roll back automatically.
		// Surface a richer error if the rollback itself failed.
		if rerr := selfupdate.RollbackError(err); rerr != nil {
			return fmt.Errorf("apply update: %w (rollback also failed: %v)", err, rerr)
		}
		return fmt.Errorf("apply update: %w", err)
	}
	return nil
}

func downloadFile(url, dst, version string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "dibbla-cli/"+version)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func downloadBytes(url, version string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dibbla-cli/"+version)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	// 1 MB cap is generous for a checksums.txt
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// lookupChecksum scans a goreleaser checksums.txt file (each line
// "<sha256>  <filename>") and returns the hex digest for filename.
func lookupChecksum(body []byte, filename string) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// goreleaser writes "<sha>  <name>"; the second field is the
		// filename, occasionally prefixed with "*" for binary mode.
		name := strings.TrimPrefix(fields[1], "*")
		if name == filename {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// openBinaryFromArchive extracts the dibbla binary from the goreleaser
// archive into a temp file and returns a reader plus a cleanup func.
func openBinaryFromArchive(archivePath string) (io.Reader, func(), error) {
	binaryName := "dibbla"
	if runtime.GOOS == "windows" {
		binaryName = "dibbla.exe"
	}

	if strings.HasSuffix(archivePath, ".zip") {
		return openFromZip(archivePath, binaryName)
	}
	return openFromTarGz(archivePath, binaryName)
}

func openFromTarGz(archivePath, binaryName string) (io.Reader, func(), error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			gz.Close()
			f.Close()
			return nil, nil, err
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}
		// Buffer to memory: dibbla binary is small (< 30 MB) and this
		// avoids juggling open handles across the rename.
		buf := &bytes.Buffer{}
		if _, err := io.Copy(buf, tr); err != nil {
			gz.Close()
			f.Close()
			return nil, nil, err
		}
		gz.Close()
		f.Close()
		return buf, func() {}, nil
	}
	gz.Close()
	f.Close()
	return nil, nil, fmt.Errorf("binary %q not found in archive", binaryName)
}

func openFromZip(archivePath, binaryName string) (io.Reader, func(), error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, nil, err
	}
	for _, zf := range zr.File {
		if filepath.Base(zf.Name) != binaryName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			zr.Close()
			return nil, nil, err
		}
		buf := &bytes.Buffer{}
		if _, err := io.Copy(buf, rc); err != nil {
			rc.Close()
			zr.Close()
			return nil, nil, err
		}
		rc.Close()
		zr.Close()
		return buf, func() {}, nil
	}
	zr.Close()
	return nil, nil, fmt.Errorf("binary %q not found in archive", binaryName)
}

// CanWrite reports whether the current process can replace the file at
// path (it must be able to write the parent directory and either the file
// itself or create it).
func CanWrite(path string) bool {
	dir := filepath.Dir(path)
	probe, err := os.CreateTemp(dir, ".dibbla-update-probe-*")
	if err != nil {
		return false
	}
	probe.Close()
	os.Remove(probe.Name())

	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
