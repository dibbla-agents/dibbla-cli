package admincmd

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/spf13/cobra"
)

type fakeCatalog struct {
	putErr  *apiclient.APIError
	models  []catalogModel
	puts    map[string]catalogInput
	deletes []string
	status  int
}

func (f *fakeCatalog) Get(path string) (*apiclient.Response, error) {
	if f.status != 0 {
		return nil, &apiclient.APIError{StatusCode: f.status, Message: "forbidden"}
	}
	b, _ := json.Marshal(map[string]any{"models": f.models})
	return &apiclient.Response{StatusCode: 200, Body: b}, nil
}

func (f *fakeCatalog) Put(path string, body interface{}) (*apiclient.Response, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	in := body.(catalogInput)
	alias := path[strings.LastIndex(path, "/")+1:]
	if f.puts == nil {
		f.puts = map[string]catalogInput{}
	}
	f.puts[alias] = in
	b, _ := json.Marshal(catalogModel{Alias: alias, Provider: in.Provider, ProviderModelID: in.ProviderModelID,
		InputUSDPerMTok: in.InputUSDPerMTok, OutputUSDPerMTok: in.OutputUSDPerMTok,
		CacheWriteUSDPerMTok: in.CacheWriteUSDPerMTok, CacheReadUSDPerMTok: in.CacheReadUSDPerMTok, Active: in.Active})
	return &apiclient.Response{StatusCode: 200, Body: b}, nil
}

func (f *fakeCatalog) Delete(path string) (*apiclient.Response, error) {
	f.deletes = append(f.deletes, path)
	return &apiclient.Response{StatusCode: 204}, nil
}

func (f *fakeCatalog) factory() func(io.Writer) (catalogClient, bool) {
	return func(io.Writer) (catalogClient, bool) { return f, true }
}

var sonnet = catalogModel{Alias: "sonnet-5", Provider: "anthropic", ProviderModelID: "claude-sonnet-5",
	InputUSDPerMTok: 2, OutputUSDPerMTok: 10, CacheWriteUSDPerMTok: 2.5, CacheReadUSDPerMTok: 0.2, Active: true}

// setCommand builds a fresh `set` with its flags parsed from args, so
// Changed() answers exactly what the user typed.
func setCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "set"}
	f := cmd.Flags()
	f.StringVar(&setFlags.provider, "provider", "", "")
	f.StringVar(&setFlags.model, "model", "", "")
	f.Float64Var(&setFlags.input, "input", 0, "")
	f.Float64Var(&setFlags.output, "output", 0, "")
	f.Float64Var(&setFlags.cacheWrite, "cache-write", 0, "")
	f.Float64Var(&setFlags.cacheRead, "cache-read", 0, "")
	f.BoolVar(&setFlags.active, "active", true, "")
	if err := f.Parse(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestModelsListPrintsAliasesWithPrefixAndPrices(t *testing.T) {
	f := &fakeCatalog{models: []catalogModel{sonnet}}
	var out, errb bytes.Buffer
	modelsJSON = false
	if code := runModelsList(f.factory(), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, want := range []string{"dibbla/sonnet-5", "claude-sonnet-5", "$2.00", "$10.00", "$0.20", "yes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list output missing %q:\n%s", want, out.String())
		}
	}
}

// Changing one field of an existing alias keeps every other field — a PUT
// replaces the row, so the CLI must send the current values for the rest.
func TestModelsSetExistingChangesOnlyWhatWasPassed(t *testing.T) {
	f := &fakeCatalog{models: []catalogModel{sonnet}}
	var out, errb bytes.Buffer
	cmd := setCommand(t, "--model", "claude-sonnet-5-1")
	if code := runModelsSet(f.factory(), cmd, "dibbla/sonnet-5", &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	got := f.puts["sonnet-5"]
	want := catalogInput{"anthropic", "claude-sonnet-5-1", 2, 10, 2.5, 0.2, true}
	if got != want {
		t.Fatalf("PUT %+v, want %+v", got, want)
	}
	if !strings.Contains(out.String(), "next call") {
		t.Errorf("output should say when it applies: %s", out.String())
	}
}

func TestModelsSetDeactivates(t *testing.T) {
	f := &fakeCatalog{models: []catalogModel{sonnet}}
	var out, errb bytes.Buffer
	if code := runModelsSet(f.factory(), setCommand(t, "--active=false"), "sonnet-5", &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if f.puts["sonnet-5"].Active {
		t.Fatal("alias still active")
	}
}

func TestModelsSetNewAliasNeedsEveryField(t *testing.T) {
	f := &fakeCatalog{models: []catalogModel{sonnet}}
	var out, errb bytes.Buffer
	cmd := setCommand(t, "--provider", "anthropic", "--model", "claude-opus-5-5", "--input", "4")
	if code := runModelsSet(f.factory(), cmd, "opus-next", &out, &errb); code == 0 {
		t.Fatal("a new alias without prices must be refused")
	}
	if len(f.puts) != 0 {
		t.Fatal("nothing may be written")
	}
	if !strings.Contains(errb.String(), "--cache-read") {
		t.Errorf("error should name the missing flags: %s", errb.String())
	}
}

func TestModelsNonGlobalAdminIsToldWhy(t *testing.T) {
	f := &fakeCatalog{status: 403}
	var out, errb bytes.Buffer
	if code := runModelsList(f.factory(), &out, &errb); code == 0 {
		t.Fatal("403 must fail")
	}
	if !strings.Contains(errb.String(), "global admins") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestModelsDeleteNeedsYes(t *testing.T) {
	f := &fakeCatalog{models: []catalogModel{sonnet}}
	var out, errb bytes.Buffer
	if code := runModelsDelete(f.factory(), "sonnet-5", false, &out, &errb); code == 0 || len(f.deletes) != 0 {
		t.Fatal("delete without --yes must not delete")
	}
	if code := runModelsDelete(f.factory(), "sonnet-5", true, &out, &errb); code != 0 || len(f.deletes) != 1 || f.deletes[0] != catalogPath+"/sonnet-5" {
		t.Fatalf("delete: code=%d deletes=%v", code, f.deletes)
	}
}

func TestPrice(t *testing.T) {
	for in, want := range map[float64]string{2: "$2.00", 0.2: "$0.20", 0.125: "$0.125", 0.005: "$0.005", 1.875: "$1.875"} {
		if got := price(in); got != want {
			t.Errorf("price(%v) = %q, want %q", in, got, want)
		}
	}
}

// The gateway's error envelope is shown as its message, not as raw JSON —
// measured on dev: a typo printed the whole {"error":{…}} body.
func TestModelsSetShowsTheGatewayMessage(t *testing.T) {
	f := &fakeCatalog{models: []catalogModel{sonnet}, putErr: &apiclient.APIError{StatusCode: 400,
		Message: `{"error":{"code":"MODEL_NOT_FOUND_AT_PROVIDER","message":"anthropic has no model claude-sonet-5"}}` + "\n"}}
	var out, errb bytes.Buffer
	if code := runModelsSet(f.factory(), setCommand(t, "--model", "claude-sonet-5"), "sonnet-5", &out, &errb); code == 0 {
		t.Fatal("must fail")
	}
	if got := errb.String(); strings.Contains(got, "{") || !strings.Contains(got, "anthropic has no model claude-sonet-5") {
		t.Fatalf("stderr = %q", got)
	}
}
