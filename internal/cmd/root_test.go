package cmd

import (
	"testing"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/update"
	"github.com/spf13/cobra"
)

func TestExecute_DoesNotBlockOnPendingUpdateCheck(t *testing.T) {
	origRootCmd := rootCmd
	origCheckInBackground := checkInBackground
	origPrintNotice := printNotice
	defer func() {
		rootCmd = origRootCmd
		checkInBackground = origCheckInBackground
		printNotice = origPrintNotice
	}()

	cmdRan := false
	rootCmd = &cobra.Command{
		Use: "dibbla",
		Run: func(cmd *cobra.Command, args []string) {
			cmdRan = true
		},
	}

	ch := make(chan *update.UpdateInfo, 1)
	checkInBackground = func(currentVersion string) <-chan *update.UpdateInfo {
		return ch
	}

	printCalled := false
	printNotice = func(info *update.UpdateInfo, currentVersion string) {
		printCalled = true
	}

	start := time.Now()
	if err := Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	elapsed := time.Since(start)

	if !cmdRan {
		t.Fatal("expected root command to run")
	}
	if printCalled {
		t.Fatal("expected printNotice not to be called when update check is pending")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected Execute to return quickly, took %v", elapsed)
	}
}

func TestExecute_PrintsWhenUpdateResultReady(t *testing.T) {
	origRootCmd := rootCmd
	origCheckInBackground := checkInBackground
	origPrintNotice := printNotice
	defer func() {
		rootCmd = origRootCmd
		checkInBackground = origCheckInBackground
		printNotice = origPrintNotice
	}()

	rootCmd = &cobra.Command{
		Use: "dibbla",
		Run: func(cmd *cobra.Command, args []string) {},
	}

	ch := make(chan *update.UpdateInfo, 1)
	ch <- &update.UpdateInfo{LatestVersion: "v9.9.9"}
	close(ch)
	checkInBackground = func(currentVersion string) <-chan *update.UpdateInfo {
		return ch
	}

	printCalled := false
	printNotice = func(info *update.UpdateInfo, currentVersion string) {
		printCalled = true
		if info == nil || info.LatestVersion != "v9.9.9" {
			t.Fatalf("unexpected update info: %+v", info)
		}
	}

	if err := Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !printCalled {
		t.Fatal("expected printNotice to be called for ready update result")
	}
}
