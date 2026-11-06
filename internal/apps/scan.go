package apps

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type TestScan struct {
	ID        string    `json:"scan_id"`
	Alias     string    `json:"alias"`
	State     string    `json:"status"`
	Coverage  string    `json:"coverage"`
	Created   time.Time `json:"created_at"`
	Expires   time.Time `json:"expires_at"`
	ErrorCode string    `json:"error_code,omitempty"`
}

func NewScanRequestID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%s", time.Now().Unix(), hex.EncodeToString(random[:])), nil
}
func scanURL(apiURL, alias string) string {
	return strings.TrimSuffix(apiURL, "/") + "/api/deploy/deployments/" + url.PathEscape(alias) + "/security-scan/tests"
}

// StartTestScan streams the caller's archive directly. It creates no local
// archive copy and no deployment. The same request ID + digest is safe to retry
// after either response was lost; the server never scans the same run twice.
func StartTestScan(apiURL, token, alias, archive, requestID string) (*TestScan, []byte, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 50<<20 {
		return nil, nil, fmt.Errorf("archive must be a regular tar.gz file of at most 50 MiB")
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, f); err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(map[string]string{"request_id": requestID, "archive_sha256": hex.EncodeToString(digest.Sum(nil))})
	if err != nil {
		return nil, nil, err
	}
	run, raw, err := scanRequest(http.MethodPost, scanURL(apiURL, alias), token, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	if run.State != "created" {
		return run, raw, nil
	}
	return scanRequest(http.MethodPut, scanURL(apiURL, alias)+"/"+url.PathEscape(run.ID)+"/archive", token, "application/gzip", f)
}
func ReadTestScan(apiURL, token, alias, id string, result bool) (*TestScan, []byte, error) {
	endpoint := scanURL(apiURL, alias) + "/" + url.PathEscape(id)
	if result {
		endpoint += "/result"
	}
	return scanRequest(http.MethodGet, endpoint, token, "", nil)
}
func EndTestScan(apiURL, token, alias, id string) (*TestScan, []byte, error) {
	return scanRequest(http.MethodDelete, scanURL(apiURL, alias)+"/"+url.PathEscape(id), token, "", nil)
}
func scanRequest(method, endpoint, token, contentType string, body io.ReadSeeker) (*TestScan, []byte, error) {
	// A transport failure may occur after the server committed. Replaying the
	// same ID is safe for every endpoint. API failures are returned unchanged.
	for attempt := 0; attempt < 2; attempt++ {
		if body != nil {
			if _, err := body.Seek(0, io.SeekStart); err != nil {
				return nil, nil, err
			}
		}
		var reader io.Reader
		if body != nil {
			reader = io.NopCloser(body)
		} // net/http must not close caller's file before retry
		req, err := http.NewRequest(method, endpoint, reader)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		client := &http.Client{Timeout: 150 * time.Second}
		response, err := client.Do(req)
		if err != nil {
			if attempt == 0 {
				continue
			}
			return nil, nil, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
		response.Body.Close()
		if readErr != nil {
			if attempt == 0 {
				continue
			}
			return nil, nil, readErr
		}
		if len(raw) > 10<<20 {
			return nil, nil, fmt.Errorf("Security scan response exceeds limit")
		}
		if response.StatusCode != http.StatusOK {
			return nil, nil, statusError(response.StatusCode, raw)
		}
		var run TestScan
		if err = json.Unmarshal(raw, &run); err != nil {
			return nil, nil, fmt.Errorf("decode Security scan response: %w", err)
		}
		if run.ID == "" || run.State == "" || !KnownScanCoverage(run.Coverage) {
			return nil, nil, fmt.Errorf("incomplete Security scan response")
		}
		return &run, raw, nil
	}
	panic("unreachable")
}

// Coverage values. A server that predates dependency scanning (DIB-1684)
// still answers secrets_only; an unknown value is refused, never guessed.
const (
	CoverageSecretsOnly            = "secrets_only"
	CoverageSecretsAndDependencies = "secrets_and_dependencies"
)

func KnownScanCoverage(c string) bool {
	return c == CoverageSecretsOnly || c == CoverageSecretsAndDependencies
}

// TestScanReport is the part of a temporary report the text output prints.
// --json prints the server's document unchanged.
type TestScanReport struct {
	ArchiveSHA256 string `json:"archive_sha256"`
	Coverage      string `json:"coverage"`
	Gitleaks      string `json:"gitleaks"`
	Findings      []struct {
		File string `json:"file"`
		Line int    `json:"line"`
		Rule string `json:"rule"`
	} `json:"findings"`
	Dependencies *struct {
		Status string `json:"status"`
		Files  []struct {
			Path     string `json:"path"`
			Service  string `json:"service"`
			Status   string `json:"status"`
			Packages int    `json:"packages"`
		} `json:"files"`
		Gaps []struct {
			Code    string `json:"code"`
			Path    string `json:"path"`
			Service string `json:"service"`
			Message string `json:"message"`
		} `json:"gaps"`
		Packages        int `json:"packages"`
		Vulnerabilities []struct {
			ID       string `json:"id"`
			Severity string `json:"severity"`
			Package  string `json:"package"`
			Version  string `json:"version"`
			FixedIn  string `json:"fixed_in"`
			File     string `json:"file"`
			Service  string `json:"service"`
		} `json:"vulnerabilities"`
		NotCovered    []string   `json:"not_covered_without_build"`
		Syft          string     `json:"syft"`
		Grype         string     `json:"grype"`
		VulnDBBuiltAt *time.Time `json:"vuln_db_built_at"`
	} `json:"dependencies"`
}

// TestScanComparison is the part of a comparison of two temporary reports
// (DIB-1686) the text output prints. --json prints the server's document.
type TestScanComparison struct {
	Alias string `json:"alias"`
	Base  struct {
		ScanID        string     `json:"scan_id"`
		VulnDBBuiltAt *time.Time `json:"vuln_db_built_at"`
	} `json:"base"`
	Head struct {
		ScanID        string     `json:"scan_id"`
		VulnDBBuiltAt *time.Time `json:"vuln_db_built_at"`
	} `json:"head"`
	Comparable *struct {
		Database      string `json:"database"`
		Coverage      string `json:"dependency_coverage"`
		SecretScanner string `json:"secret_scanner"`
	} `json:"comparable"`
	Limitations []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Service string `json:"service"`
		Path    string `json:"path"`
	} `json:"limitations"`
	Summary *struct {
		New          int `json:"new"`
		Persisting   int `json:"persisting"`
		NotDetected  int `json:"not_detected"`
		Unverifiable int `json:"unverifiable"`
		NewlyCovered int `json:"newly_covered"`
	} `json:"summary"`
	Findings []ComparedFinding `json:"findings"`
	Image    *struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Origin  *struct {
			DeploymentID string `json:"deployment_id"`
			CommitSHA    string `json:"commit_sha"`
		} `json:"origin"`
		VulnDBBuiltAt *time.Time `json:"vuln_db_built_at"`
		Images        []struct {
			Service         string `json:"service"`
			Image           string `json:"image"`
			Error           string `json:"error"`
			Vulnerabilities []struct {
				ID           string `json:"id"`
				Severity     string `json:"severity"`
				Package      string `json:"package"`
				Version      string `json:"version"`
				Type         string `json:"type"`
				FixedIn      string `json:"fixed_in"`
				InHeadSource string `json:"in_head_source"`
			} `json:"vulnerabilities"`
		} `json:"images"`
	} `json:"image"`
	Retention string `json:"retention"`
}

// ComparedFinding is one entry of a comparison.
type ComparedFinding struct {
	Kind        string `json:"kind"`
	Change      string `json:"change"`
	Attribution string `json:"attribution"`
	Reason      string `json:"reason"`
	ID          string `json:"id"`
	Severity    string `json:"severity"`
	Package     string `json:"package"`
	Service     string `json:"service"`
	File        string `json:"file"`
	FixedIn     string `json:"fixed_in"`
	BaseDep     *struct {
		Versions []string `json:"versions"`
	} `json:"base_dependency"`
	HeadDep *struct {
		Versions []string `json:"versions"`
	} `json:"head_dependency"`
	BaseSecret *struct {
		Lines []int `json:"lines"`
	} `json:"base_secret"`
	HeadSecret *struct {
		Lines []int `json:"lines"`
	} `json:"head_secret"`
}

// CompareTestScans asks the server to compare two temporary reports of the
// same app. It is a read: retrying a lost response is safe. A response that
// lacks any part is refused, never shown as an empty comparison.
func CompareTestScans(apiURL, token, alias, base, head string, image bool) (*TestScanComparison, []byte, error) {
	q := url.Values{"base": {base}, "head": {head}}
	if image {
		q.Set("image", "rematch")
	}
	endpoint := scanURL(apiURL, alias) + "/compare?" + q.Encode()
	var raw []byte
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err = getJSON(endpoint, token)
		if err == nil {
			break
		}
		var statusErr *StatusError
		if errors.As(err, &statusErr) {
			return nil, nil, err
		}
	}
	if err != nil {
		return nil, nil, err
	}
	var cmp TestScanComparison
	if err = json.Unmarshal(raw, &cmp); err != nil {
		return nil, nil, fmt.Errorf("decode Security scan comparison: %w", err)
	}
	if cmp.Base.ScanID != base || cmp.Head.ScanID != head || cmp.Comparable == nil || cmp.Summary == nil || cmp.Findings == nil || cmp.Image == nil || cmp.Limitations == nil {
		return nil, nil, fmt.Errorf("incomplete Security scan comparison response")
	}
	return &cmp, raw, nil
}

func getJSON(endpoint, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 150 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 10<<20 {
		return nil, fmt.Errorf("Security scan response exceeds limit")
	}
	if response.StatusCode != http.StatusOK {
		return nil, statusError(response.StatusCode, raw)
	}
	return raw, nil
}
