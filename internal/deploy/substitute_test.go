package deploy

import (
	"strings"
	"testing"
)

// fakeEnv is a deterministic env-var lookup for tests.
func fakeEnv(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestSubstitute_HappyPath_VarSet(t *testing.T) {
	in := []byte(`environment:
  HOME: ${HOME}
  USER: ${USER}
`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{"HOME": "/home/erik", "USER": "erik"}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "HOME: /home/erik") {
		t.Errorf("missing HOME substitution: %s", got)
	}
	if !strings.Contains(got, "USER: erik") {
		t.Errorf("missing USER substitution: %s", got)
	}
}

func TestSubstitute_MultipleOnSameLine(t *testing.T) {
	in := []byte(`url: ${PROTO}://${HOST}:${PORT}/api`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{
		"PROTO": "https", "HOST": "example.com", "PORT": "8443",
	}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if string(out) != "url: https://example.com:8443/api" {
		t.Errorf("unexpected: %s", string(out))
	}
}

func TestSubstitute_DefaultValue(t *testing.T) {
	in := []byte(`x: ${MISSING:-fallback}`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if string(out) != "x: fallback" {
		t.Errorf("default not used: %s", string(out))
	}
}

func TestSubstitute_DefaultValueOverriddenByEnv(t *testing.T) {
	// When the var IS set, the default is ignored.
	in := []byte(`x: ${HOST:-localhost}`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{"HOST": "prod.example.com"}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if string(out) != "x: prod.example.com" {
		t.Errorf("env should win over default: %s", string(out))
	}
}

func TestSubstitute_EmptyDefault(t *testing.T) {
	in := []byte(`x: ${MISSING:-}`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if string(out) != "x: " {
		t.Errorf("empty default: got %q", string(out))
	}
}

func TestSubstitute_UnresolvedFails(t *testing.T) {
	in := []byte(`x: ${MISSING_NO_DEFAULT}`)
	_, err := SubstituteShellVars(in, fakeEnv(map[string]string{}))
	if err == nil {
		t.Fatal("expected error for unresolved var")
	}
	if !strings.Contains(err.Error(), "MISSING_NO_DEFAULT") {
		t.Errorf("error should name the var: %v", err)
	}
	if !strings.Contains(err.Error(), ":-fallback") {
		t.Errorf("error should hint at default syntax: %v", err)
	}
}

func TestSubstitute_DibblaPrefixPassesThrough(t *testing.T) {
	// Server-side vars are reserved — must not be substituted client-side
	// even when not present in the shell env.
	in := []byte(`environment:
  REDIS_URL: ${DIBBLA_SVC_REDIS_URL}
  ALIAS: ${DIBBLA_ALIAS}
`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("substitute should not fail on DIBBLA_*: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "${DIBBLA_SVC_REDIS_URL}") {
		t.Errorf("DIBBLA_SVC_REDIS_URL should pass through: %s", got)
	}
	if !strings.Contains(got, "${DIBBLA_ALIAS}") {
		t.Errorf("DIBBLA_ALIAS should pass through: %s", got)
	}
}

func TestSubstitute_DibblaPrefixUntouchedEvenWhenShellSet(t *testing.T) {
	// If a user accidentally has DIBBLA_ALIAS in their shell env, we must
	// NOT substitute — that would shadow the server's value.
	in := []byte(`alias: ${DIBBLA_ALIAS}`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{
		"DIBBLA_ALIAS": "shouldnt-be-used",
	}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if !strings.Contains(string(out), "${DIBBLA_ALIAS}") {
		t.Errorf("DIBBLA_* must pass through even if set in shell: %s", string(out))
	}
}

func TestSubstitute_DollarEscapeYieldsLiteralDollarBrace(t *testing.T) {
	// Compose convention: $$VAR or $${VAR} produces a literal $ in the
	// output. Lets users write `${VAR}` literally if they need to.
	in := []byte(`literal: $${KEEP_ME}
escaped: $$plain
mixed: $${SHELL} actual ${HOME}
`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{"HOME": "/home/erik"}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "literal: ${KEEP_ME}") {
		t.Errorf("$${VAR} should become ${VAR}: %s", got)
	}
	if !strings.Contains(got, "escaped: $plain") {
		t.Errorf("$$plain should become $plain: %s", got)
	}
	if !strings.Contains(got, "mixed: ${SHELL} actual /home/erik") {
		t.Errorf("mixed escape + sub: %s", got)
	}
}

func TestSubstitute_DollarVarFormNotSupported(t *testing.T) {
	// Only ${VAR} form. Bare $VAR is NOT substituted (matches the server's
	// behavior and avoids false positives on shell scripts inline in YAML).
	in := []byte(`x: $HOME y: ${HOME}`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{"HOME": "/home/erik"}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if !strings.Contains(string(out), "$HOME y: /home/erik") {
		t.Errorf("$HOME should NOT be substituted; ${HOME} should: %s", string(out))
	}
}

func TestSubstitute_LowercaseAllowed(t *testing.T) {
	// Unlike the server's uppercase-only regex, the CLI accepts mixed-case
	// names so users can write `${user}` if they want.
	in := []byte(`a: ${myVar}`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{"myVar": "ok"}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if string(out) != "a: ok" {
		t.Errorf("got %s", string(out))
	}
}

func TestSubstitute_NoVarsNoChange(t *testing.T) {
	// No `${...}` placeholders at all — bytes pass through verbatim.
	in := []byte(`version: 1
services:
  web:
    image: nginx:1.27
    port: 3000
    public: true
`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("no-vars input should pass through unchanged")
	}
}

func TestSubstitute_MixedDibblaAndShell(t *testing.T) {
	// Realistic case: manifest uses both server-side discovery vars
	// (passes through) and shell vars (substituted).
	in := []byte(`environment:
  REDIS_URL: ${DIBBLA_SVC_REDIS_URL}
  APP_VERSION: ${BUILD_VERSION:-dev}
  USER_HOME: ${HOME}
`)
	out, err := SubstituteShellVars(in, fakeEnv(map[string]string{
		"BUILD_VERSION": "v1.2.3",
		"HOME":          "/home/erik",
	}))
	if err != nil {
		t.Fatalf("substitute: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "REDIS_URL: ${DIBBLA_SVC_REDIS_URL}") {
		t.Errorf("DIBBLA_* should pass through: %s", got)
	}
	if !strings.Contains(got, "APP_VERSION: v1.2.3") {
		t.Errorf("BUILD_VERSION should resolve: %s", got)
	}
	if !strings.Contains(got, "USER_HOME: /home/erik") {
		t.Errorf("HOME should resolve: %s", got)
	}
}
