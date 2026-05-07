package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestDiscoverFindsYamlOrYml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yaml", "version: 1")
	p, ambiguous, found := Discover(dir)
	if !found || ambiguous || p == "" {
		t.Errorf("yaml only: found=%v ambiguous=%v path=%q", found, ambiguous, p)
	}
}

func TestDiscoverYmlAlternate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yml", "version: 1")
	_, ambiguous, found := Discover(dir)
	if !found || ambiguous {
		t.Errorf("yml only: found=%v ambiguous=%v", found, ambiguous)
	}
}

func TestDiscoverAmbiguousWhenBothPresent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dibbla.yaml", "version: 1")
	writeFile(t, dir, "dibbla.yml", "version: 1")
	_, ambiguous, found := Discover(dir)
	if !ambiguous || !found {
		t.Errorf("both: ambiguous=%v found=%v", ambiguous, found)
	}
}

func TestDiscoverAbsent(t *testing.T) {
	dir := t.TempDir()
	_, _, found := Discover(dir)
	if found {
		t.Errorf("no manifest should report not found")
	}
}

func TestParseValidMinimalManifest(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  app:
    build: .
    port: 3000
    public: true
`)
	m, err := ParseAndValidate(p)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if m.Version != 1 || len(m.Services) != 1 {
		t.Errorf("unexpected manifest: %+v", m)
	}
}

func TestParseValidMultiService(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build: ./web
    port: 3000
    public: true
  worker:
    build: ./worker
  redis:
    image: redis:7
    port: 6379
`)
	if _, err := ParseAndValidate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestParseRejectsWrongVersion(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", "version: 2\nservices:\n  app:\n    build: .\n")
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeManifestInvalid)
}

func TestParseRejectsEmptyServices(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", "version: 1\nservices: {}\n")
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeManifestInvalid)
}

func TestParseRejectsReservedServiceName(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  proxy:
    build: .
    port: 3000
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeServiceNameInvalid)
}

func TestParseRejectsUppercaseServiceName(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  Web:
    build: .
    port: 3000
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeServiceNameInvalid)
}

func TestParseRejectsBuildAndImage(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  app:
    build: .
    image: redis:7
    port: 3000
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeManifestInvalid)
}

func TestParseRejectsNeitherBuildNorImage(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  app:
    port: 3000
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeManifestInvalid)
}

func TestParseRejectsImageWithoutTag(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  cache:
    image: redis
    port: 6379
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeManifestInvalid)
}

func TestParseRejectsBadYAML(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", "version: 1\n  services\n  - oops")
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeManifestInvalid)
}

func TestParseAcceptsBuildObjectForm(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build:
      context: ./web
      dockerfile: Dockerfile.prod
    port: 3000
    public: true
`)
	if _, err := ParseAndValidate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func expectErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *Error, got %T (%v)", err, err)
	}
	if e.Code != code {
		t.Errorf("expected code %s, got %s (detail=%s)", code, e.Code, e.Detail)
	}
}

func TestParseAcceptsStatefulWithRoutes(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true
    volumes:
      - path: /data/db
        size: 5Gi
    routes:
      - type: tcp
        port: 27017
        tls: edge
        hostname: my-db
`)
	m, err := ParseAndValidate(p)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	db := m.Services["db"]
	if db == nil {
		t.Fatal("missing db service")
	}
	if db.Stateful == nil || !*db.Stateful {
		t.Errorf("expected stateful=true")
	}
	if len(db.Routes) != 1 || db.Routes[0].Type != "tcp" || db.Routes[0].Hostname != "my-db" {
		t.Errorf("unexpected routes: %+v", db.Routes)
	}
}

func TestParseRejectsStatefulWithoutVolume(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeStatefulNoVolume)
}

func TestParseRejectsRouteBadType(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true
    volumes:
      - path: /data/db
        size: 1Gi
    routes:
      - type: udp
        port: 27017
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeRouteInvalid)
}

func TestParseRejectsRouteBadTLS(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true
    volumes:
      - path: /data/db
        size: 1Gi
    routes:
      - type: tcp
        port: 27017
        tls: weird
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeRouteInvalid)
}

func TestParseRejectsTcpWithTLSNone(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true
    volumes:
      - path: /data/db
        size: 1Gi
    routes:
      - type: tcp
        port: 27017
        tls: none
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeRouteInvalid)
}

func TestParseRejectsHttpWithEdgeTLS(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  web:
    build: .
    port: 3000
    routes:
      - type: http
        port: 3000
        tls: edge
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeRouteInvalid)
}

func TestParseRejectsRouteBadHostname(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true
    volumes:
      - path: /data/db
        size: 1Gi
    routes:
      - type: tcp
        port: 27017
        hostname: "Bad_Host"
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeRouteInvalid)
}

func TestParseRejectsRouteBadPort(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  db:
    image: mongo:7
    port: 27017
    stateful: true
    volumes:
      - path: /data/db
        size: 1Gi
    routes:
      - type: tcp
        port: 99999
`)
	_, err := ParseAndValidate(p)
	expectErrCode(t, err, ErrCodeRouteInvalid)
}

func TestParseAcceptsMultiRoute(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "dibbla.yaml", `
version: 1
services:
  broker:
    image: rabbitmq:3-management
    port: 15672
    stateful: true
    volumes:
      - path: /var/lib/rabbitmq
        size: 2Gi
    routes:
      - type: https
        port: 15672
        tls: edge
        hostname: broker-admin
      - type: tcp
        port: 5671
        tls: passthrough
        hostname: broker-amqp
`)
	m, err := ParseAndValidate(p)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(m.Services["broker"].Routes) != 2 {
		t.Errorf("expected 2 routes, got %d", len(m.Services["broker"].Routes))
	}
}
