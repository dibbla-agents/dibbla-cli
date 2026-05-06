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
