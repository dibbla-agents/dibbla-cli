package render

import (
	"bytes"
	"strings"
	"testing"
)

// TestQuietRendererSuppressesServiceCountForLegacy verifies the byte-stable
// invariant: legacy single-app deploys produce the same one-line output as
// today (no "(N services)" suffix).
func TestQuietRendererSuppressesServiceCountForLegacy(t *testing.T) {
	var buf bytes.Buffer
	q := NewQuiet(&buf)
	q.OnEvent(DeployEvent{Type: "result", Result: &DeployResult{
		Status: "success",
		Deployment: ResultDeployment{
			Alias:  "myapp",
			URL:    "https://myapp.dibbla.app",
			Status: "running",
			Services: []ServiceView{
				{Name: "app", IsPublic: true, Replicas: 1, ReadyReplicas: 1, Status: "running"},
			},
		},
	}})
	if got := q.OnDone(); got != 0 {
		t.Errorf("legacy success exit code: want 0, got %d", got)
	}
	out := buf.String()
	if strings.Contains(out, "services") {
		t.Errorf("legacy line should not mention services; got %q", out)
	}
}

// TestQuietRendererShowsServiceCountForMultiService verifies the new
// "(N services)" suffix appears for multi-service deploys.
func TestQuietRendererShowsServiceCountForMultiService(t *testing.T) {
	var buf bytes.Buffer
	q := NewQuiet(&buf)
	q.OnEvent(DeployEvent{Type: "result", Result: &DeployResult{
		Status: "success",
		Deployment: ResultDeployment{
			Alias:  "shop",
			URL:    "https://shop.dibbla.app",
			Status: "running",
			Services: []ServiceView{
				{Name: "web", IsPublic: true, Replicas: 1, ReadyReplicas: 1, Status: "running"},
				{Name: "worker", Replicas: 1, ReadyReplicas: 1, Status: "running"},
				{Name: "redis", Replicas: 1, ReadyReplicas: 1, Status: "running"},
			},
		},
	}})
	if got := q.OnDone(); got != 0 {
		t.Errorf("multi-service success exit code: want 0, got %d", got)
	}
	out := buf.String()
	if !strings.Contains(out, "3 services") {
		t.Errorf("multi-service line should include '3 services'; got %q", out)
	}
}

// TestTTYPrintsRouteConnectionInfo verifies that the TTY renderer prints a
// connection-string line under each TCP-route service. ANSI colors are
// disabled to keep the assertion plain-text-stable.
func TestTTYPrintsRouteConnectionInfo(t *testing.T) {
	var buf bytes.Buffer
	tty := NewTTY(&buf, false /* enableANSI */)
	tty.OnEvent(DeployEvent{Type: "result", Result: &DeployResult{
		Status: "success",
		Deployment: ResultDeployment{
			Alias:  "rt",
			URL:    "https://rt.dibbla.app",
			Status: "running",
			Services: []ServiceView{{
				Name:          "db",
				Replicas:      1,
				ReadyReplicas: 1,
				Status:        "running",
				Stateful:      true,
				Routes: []RouteView{
					{Type: "tcp", Port: 27017, TLS: "edge", Hostname: "rt-db.dibbla.app", ExternalPort: 443},
				},
			}},
		},
	}})
	tty.OnDone()
	out := buf.String()
	if !strings.Contains(out, "tcp+tls://rt-db.dibbla.app:443") {
		t.Errorf("expected connection URL with public :443 in TTY output; got:\n%s", out)
	}
	if strings.Contains(out, ":27017") {
		t.Errorf("must NOT print the backend port; got:\n%s", out)
	}
	if !strings.Contains(out, "[stateful]") {
		t.Errorf("expected [stateful] marker in TTY output; got:\n%s", out)
	}
}

// TestTTYHidesNonTCPRoutesUnderServices verifies non-tcp routes are not
// surfaced in the per-service block (they're already covered by the top
// deployment URL).
func TestTTYHidesNonTCPRoutesUnderServices(t *testing.T) {
	var buf bytes.Buffer
	tty := NewTTY(&buf, false)
	tty.OnEvent(DeployEvent{Type: "result", Result: &DeployResult{
		Status: "success",
		Deployment: ResultDeployment{
			Alias:  "x",
			URL:    "https://x.dibbla.app",
			Status: "running",
			Services: []ServiceView{{
				Name:          "web",
				IsPublic:      true,
				Replicas:      1,
				ReadyReplicas: 1,
				Status:        "running",
				Routes: []RouteView{
					{Type: "https", Port: 3000, TLS: "edge", Hostname: "x.dibbla.app"},
				},
			}},
		},
	}})
	tty.OnDone()
	out := buf.String()
	if strings.Contains(out, "tcp+tls://") {
		t.Errorf("https route must NOT produce a tcp+tls connection line; got:\n%s", out)
	}
}

func TestCountDisplayServices(t *testing.T) {
	cases := []struct {
		name string
		in   []ServiceView
		want int
	}{
		{"empty array (true legacy)", nil, 0},
		{"single 'app' (synthesized legacy)", []ServiceView{{Name: "app"}}, 0},
		{"single 'web' (multi-service single)", []ServiceView{{Name: "web"}}, 1},
		{"three services", []ServiceView{{Name: "a"}, {Name: "b"}, {Name: "c"}}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := countDisplayServices(c.in); got != c.want {
				t.Errorf("want %d, got %d", c.want, got)
			}
		})
	}
}
