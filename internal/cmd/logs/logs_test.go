package logs

import "testing"

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
