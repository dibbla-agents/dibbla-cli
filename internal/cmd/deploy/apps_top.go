package deploy

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/apps"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/spf13/cobra"
)

// DIB-1645: `apps top` shows what each pod of an app uses of CPU and memory
// right now next to what it reserves (request) and may use (limit) — the
// numbers to size cpu/memory on. One reading, no history; a pod in its
// start-up phase is marked, because its number still carries the boot cost.

var appsTopCmd = &cobra.Command{
	Use:   "top <alias>",
	Short: "Show the CPU and memory each pod uses now, next to its request and limit",
	Long: `Show, per service and pod, the CPU and memory the app uses right now next
to its request (what is reserved for it) and its limit (the most it may use),
and usage as a percentage of each.

How to read it:
  %REQ far below 100 on a steady pod   the service reserves more than it uses;
                                       lower cpu/memory and the plan fits more
  %REQ above 100                       it uses more than it reserves (burst)
  memory %LIM near 100                 risk of out-of-memory kills; raise memory
  START-UP                             the container started under 5 minutes ago,
                                       so the reading still includes boot cost
                                       and is not steady state; read again later

CPU is in millicores (1000m = one core). The reading is the cluster's
average over its last ~15-30 seconds; there is no history.

Examples:
  dibbla apps top myapp
  dibbla apps top myapp --json`,
	Args: cobra.ExactArgs(1),
	Run:  runAppsTop,
}

var topJSON bool

func init() {
	appsCmd.AddCommand(appsTopCmd)
	appsTopCmd.Flags().BoolVar(&topJSON, "json", false, "Print the raw API document")
}

func runAppsTop(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	requireToken(cfg)
	os.Exit(runAppsTopCore(os.Stdout, os.Stderr, cfg.APIURL, cfg.APIToken, args[0], topJSON))
}

// runAppsTopCore is the testable inner implementation of `apps top`.
func runAppsTopCore(stdout, stderr io.Writer, apiURL, apiToken, alias string, jsonOut bool) int {
	if !apps.AliasRe.MatchString(alias) {
		return invalidAlias(stderr, alias)
	}
	doc, raw, err := apps.GetUsage(apiURL, apiToken, alias)
	if err != nil {
		return reportAppError(stderr, "top", alias, err)
	}
	if jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	printUsage(stdout, alias, doc)
	return 0
}

func printUsage(stdout io.Writer, alias string, doc *apps.UsageResponse) {
	fmt.Fprintf(stdout, "%s %s — CPU and memory now\n", platform.Icon("📊", "[TOP]"), alias)
	if !doc.MetricsAvailable {
		fmt.Fprintf(stdout, "   %s usage unavailable: the cluster's metrics did not answer. Requests and limits only.\n", platform.Icon("⚠️", "[!]"))
	}
	if len(doc.Pods) == 0 {
		fmt.Fprintln(stdout, "   (no running pods — nothing to measure)")
		return
	}
	fmt.Fprintln(stdout)
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "   SERVICE\tPOD\tAGE\tCPU\tREQ\tLIM\t%REQ\tMEM\tREQ\tLIM\t%REQ\t%LIM\t")
	warming := false
	for _, p := range doc.Pods {
		note := ""
		if p.WarmingUp {
			note = platform.Icon("⏳", "") + " START-UP"
			warming = true
		}
		if p.Restarts > 0 {
			note = strings.TrimSpace(note + fmt.Sprintf(" %d restart(s)", p.Restarts))
		}
		fmt.Fprintf(tw, "   %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Service, shortPodName(p.Pod), topAge(p.AgeSeconds),
			usageOr(p.CPU.Usage, milli), quantOr(p.CPU.Request, milli), quantOr(p.CPU.Limit, milli), pct(p.CPU.PercentOfRequest),
			usageOr(p.Memory.Usage, mebi), quantOr(p.Memory.Request, mebi), quantOr(p.Memory.Limit, mebi), pct(p.Memory.PercentOfRequest), pct(p.Memory.PercentOfLimit),
			note)
	}
	_ = tw.Flush()
	fmt.Fprintln(stdout)
	if warming {
		fmt.Fprintf(stdout, "   START-UP: the container started under %d minutes ago — the reading still includes boot cost, not steady state.\n", doc.WarmupSeconds/60)
	}
	fmt.Fprintln(stdout, "   %REQ = usage as a share of the request (reserved); far below 100 on a steady pod means it reserves more than it uses.")
	fmt.Fprintln(stdout, "   One reading (the cluster's ~15-30 s average), no history. See 'dibbla apps top --help'.")
}

// shortPodName keeps the pod's own suffix: the app and service are already
// in the row, and the full name is in --json.
func shortPodName(name string) string {
	if i := strings.LastIndex(name, "-"); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return name
}

func milli(v int64) string { return fmt.Sprintf("%dm", v) }

func mebi(v int64) string {
	const mi = 1 << 20
	if v >= 1<<30 && v%(1<<30) == 0 {
		return fmt.Sprintf("%dGi", v>>30)
	}
	return fmt.Sprintf("%dMi", (v+mi/2)/mi)
}

func usageOr(v *int64, f func(int64) string) string {
	if v == nil {
		return "?"
	}
	return f(*v)
}

func quantOr(v int64, f func(int64) string) string {
	if v <= 0 {
		return "-"
	}
	return f(v)
}

func pct(p *float64) string {
	if p == nil {
		return "-"
	}
	if *p < 10 {
		return fmt.Sprintf("%.1f%%", *p)
	}
	return fmt.Sprintf("%.0f%%", *p)
}

func topAge(s int64) string {
	d := time.Duration(s) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", s)
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	}
	return fmt.Sprintf("%dd", s/86400)
}
