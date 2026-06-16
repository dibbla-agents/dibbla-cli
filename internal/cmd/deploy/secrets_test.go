package deploy

import (
	"bytes"
	"strings"
	"testing"
)

func TestRequireServiceWithDeployment_NoServicePasses(t *testing.T) {
	var buf bytes.Buffer
	if !requireServiceWithDeployment(&buf, "", "") {
		t.Errorf("global scope should be allowed")
	}
	if buf.Len() != 0 {
		t.Errorf("no error expected, got: %q", buf.String())
	}
}

func TestRequireServiceWithDeployment_ServiceWithDeploymentPasses(t *testing.T) {
	var buf bytes.Buffer
	if !requireServiceWithDeployment(&buf, "myapp", "web") {
		t.Errorf("service+deployment should be allowed")
	}
	if buf.Len() != 0 {
		t.Errorf("no error expected, got: %q", buf.String())
	}
}

func TestRequireServiceWithDeployment_ServiceWithoutDeploymentRejected(t *testing.T) {
	var buf bytes.Buffer
	if requireServiceWithDeployment(&buf, "", "web") {
		t.Errorf("service-only should be rejected")
	}
	if !strings.Contains(buf.String(), "--service requires --deployment") {
		t.Errorf("expected guard message, got: %q", buf.String())
	}
}

func TestScopeLabel(t *testing.T) {
	cases := []struct {
		dep, svc, want string
	}{
		{"", "", "global"},
		{"myapp", "", "deployment myapp"},
		{"myapp", "web", "deployment myapp, service web"},
	}
	for _, c := range cases {
		if got := scopeLabel(c.dep, c.svc); got != c.want {
			t.Errorf("scopeLabel(%q,%q) = %q, want %q", c.dep, c.svc, got, c.want)
		}
	}
}

func TestSecretsCmd_HasServiceFlags(t *testing.T) {
	for _, sub := range []string{"list", "set", "get", "delete"} {
		c, _, err := secretsCmd.Find([]string{sub})
		if err != nil {
			t.Fatalf("find %s: %v", sub, err)
		}
		if c.Flags().Lookup("service") == nil {
			t.Errorf("--service flag missing on secrets %s", sub)
		}
	}
}

func TestSecretNameRe(t *testing.T) {
	valid := []string{"A", "API_KEY", "a1", "DATABASE_URL", "x_9"}
	invalid := []string{"", "1ABC", "a-b", "a.b", "WITH SPACE", "lower-case-dash"}
	for _, v := range valid {
		if !secretNameRe.MatchString(v) {
			t.Errorf("expected %q to be a valid secret name", v)
		}
	}
	for _, v := range invalid {
		if secretNameRe.MatchString(v) {
			t.Errorf("expected %q to be an invalid secret name", v)
		}
	}
}

func TestSecretsImportCmd_Registered(t *testing.T) {
	c, _, err := secretsCmd.Find([]string{"import"})
	if err != nil {
		t.Fatalf("find import: %v", err)
	}
	for _, f := range []string{"deployment", "service", "env", "dry-run"} {
		if c.Flags().Lookup(f) == nil {
			t.Errorf("--%s flag missing on secrets import", f)
		}
	}
}
