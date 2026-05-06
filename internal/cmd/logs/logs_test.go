package logs

import (
	"strings"
	"testing"
)

func TestFlagDefaults(t *testing.T) {
	// Reset to defaults (cobra binds to package globals via init()).
	if logsCmd.Flags().Lookup("since").DefValue != "15m0s" {
		t.Errorf("--since default = %s, want 15m0s", logsCmd.Flags().Lookup("since").DefValue)
	}
	if logsCmd.Flags().Lookup("follow").DefValue != "false" {
		t.Errorf("--follow default should be false")
	}
	if logsCmd.Flags().Lookup("tail").DefValue != "0" {
		t.Errorf("--tail default should be 0")
	}
}

func TestNewFlagsExist(t *testing.T) {
	if logsCmd.Flags().Lookup("service") == nil {
		t.Error("--service flag missing")
	}
	if logsCmd.Flags().Lookup("pod-stream") == nil {
		t.Error("--pod-stream flag missing")
	}
}

func TestRunLogs_PodStreamRequiresService(t *testing.T) {
	// runLogs reads package globals — set them and call directly.
	defer func() { flagPodStream = false; flagService = "" }()
	flagPodStream = true
	flagService = ""
	err := runLogs(logsCmd, []string{"myapp"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--pod-stream requires --service") {
		t.Errorf("unexpected err: %v", err)
	}
}
