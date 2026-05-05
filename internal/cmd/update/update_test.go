package update

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestRegisterAddsUpdateCommand(t *testing.T) {
	root := &cobra.Command{Use: "dibbla"}
	Register(root, "v1.2.3")

	cmd, _, err := root.Find([]string{"update"})
	if err != nil {
		t.Fatalf("update command not found: %v", err)
	}
	if cmd.Use != "update" {
		t.Errorf("got Use %q, want update", cmd.Use)
	}
	if version != "v1.2.3" {
		t.Errorf("Register did not propagate version, got %q", version)
	}
}

func TestUpdateFlagsExist(t *testing.T) {
	flags := []string{"check", "force", "yes", "version"}
	for _, name := range flags {
		if updateCmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag --%s", name)
		}
	}
}

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.0.1", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.5.0", "v1.0.0", false},
		{"v1.0.0", "v2.0.0", true},
		{"garbage", "v1.0.0", true}, // unparseable current ⇒ assume update needed
		{"v1.0.0", "garbage", false},
	}
	for _, c := range cases {
		got := needsUpdate(c.current, c.latest)
		if got != c.want {
			t.Errorf("needsUpdate(%q,%q) = %v want %v", c.current, c.latest, got, c.want)
		}
	}
}
