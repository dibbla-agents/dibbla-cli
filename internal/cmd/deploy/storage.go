package deploy

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/spinner"
	"github.com/dibbla-agents/dibbla-cli/internal/storage"
	"github.com/spf13/cobra"
)

var storageCmd = &cobra.Command{
	Use:     "storage",
	Aliases: []string{"buckets"},
	Short:   "Manage Dibbla object storage buckets",
	Long: `Provides commands to list, create, delete, rotate and inspect managed
S3-compatible storage buckets. Creating a bucket provisions credentials scoped
to exactly that bucket and injects them automatically as secrets
(STORAGE_<NAME>_ENDPOINT/BUCKET/ACCESS_KEY_ID/SECRET_ACCESS_KEY). Those are the
app's and are write-only; 'storage credentials' gives you a one-hour key of
your own for one bucket instead.`,
}

var storageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all managed buckets",
	Long:  `Fetches and displays a list of all storage buckets managed by the Dibbla platform.`,
	Run:   runStorageList,
}

var storageCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new bucket",
	Long: `Creates a managed storage bucket with a hard quota (default 5Gi) and
credentials scoped to exactly that bucket, injected automatically as secrets.

Bucket names are 3-48 chars of lowercase letters, digits and hyphens,
starting and ending alphanumeric.`,
	Args: cobra.MaximumNArgs(1),
	Run:  runStorageCreate,
}

var storageDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a bucket",
	Long: `Deletes a bucket, its scoped credentials and its injected secrets.
A non-empty bucket is refused unless --force is passed. This action cannot be undone.`,
	Args: cobra.ExactArgs(1),
	Run:  runStorageDelete,
}

var storageRotateCmd = &cobra.Command{
	Use:   "rotate <name>",
	Short: "Rotate a bucket's credentials",
	Long: `Re-mints the bucket's scoped credentials and re-syncs the injected secrets.

Rotation is restart-coupled: running pods keep the old — now invalid — key
until restarted, so the bound deployment's services are restarted automatically.
Pass --no-restart to skip that (you must restart yourself before the app can
reach its bucket again).`,
	Args: cobra.ExactArgs(1),
	Run:  runStorageRotate,
}

var storageInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show usage vs quota for all buckets",
	Long:  `Displays size, object count and quota for every managed bucket.`,
	Run:   runStorageInfo,
}

// storageCredentialsCmd prints a key of the person's own for one bucket
// (DIB-1344). It used to print the app's keys, read from its STORAGE_<NAME>_*
// secrets; those are write-only since DIB-1337 and stay unread. The key is
// minted for whoever runs the command, works on that bucket's objects only and
// expires in an hour — the bucket counterpart of `dibbla db connect`.
var storageCredentialsCmd = &cobra.Command{
	Use:   "credentials <name>",
	Short: "Print a one-hour key of your own for one bucket, as export lines",
	Long: `Prints shell export lines for using a bucket from your own tools (aws CLI,
mc, rclone, SDKs). The key is yours: Dibbla mints it for you when you run the
command, it reads and writes this one bucket's objects and nothing else, and it
expires by itself after an hour. Run the command again for a new one.

It is not the app's key. The app's STORAGE_<NAME>_* secrets are write-only and
are neither read nor changed; your key expiring never affects the app.

The output carries a secret key. Keep it in your shell, not in a file or a chat:
an AI agent uses this command only inside eval, as below, and never prints it.

Examples:
  eval "$(dibbla storage credentials mybucket -q)"
  aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls "s3://$DIBBLA_BUCKET"
  rclone ls ":s3:$DIBBLA_BUCKET" --s3-env-auth --s3-no-check-bucket --s3-endpoint "$AWS_ENDPOINT_URL"`,
	Args: cobra.ExactArgs(1),
	Run:  runStorageCredentials,
}

var (
	storageListQuiet        bool
	storageCreateName       string
	storageCreateDeployment string
	storageCreateSize       string
	storageCreateExpireDays int
	storageDeleteYes        bool
	storageDeleteForce      bool
	storageDeleteQuiet      bool
	storageRotateNoRestart  bool
	storageCredsQuiet       bool
	storageCredsDeployment  string
)

func init() {
	storageCmd.AddCommand(storageListCmd)
	storageCmd.AddCommand(storageCreateCmd)
	storageCmd.AddCommand(storageDeleteCmd)
	storageCmd.AddCommand(storageRotateCmd)
	storageCmd.AddCommand(storageInfoCmd)
	storageCmd.AddCommand(storageCredentialsCmd)

	storageListCmd.Flags().BoolVarP(&storageListQuiet, "quiet", "q", false, "Only print bucket names, one per line (for scripting)")
	storageCreateCmd.Flags().StringVar(&storageCreateName, "name", "", "Name of the bucket to create")
	storageCreateCmd.Flags().StringVar(&storageCreateDeployment, "deployment", "", "Scope the bucket and its STORAGE_* secrets to a specific deployment")
	storageCreateCmd.Flags().StringVar(&storageCreateSize, "size", "", "Bucket quota, e.g. 5Gi or 500Mi (default: server default, 5Gi)")
	storageCreateCmd.Flags().IntVar(&storageCreateExpireDays, "expire-days", 0, "Automatically delete objects older than this many days (0 = never)")
	storageDeleteCmd.Flags().BoolVarP(&storageDeleteYes, "yes", "y", false, "Skip confirmation prompt")
	storageDeleteCmd.Flags().BoolVar(&storageDeleteForce, "force", false, "Delete even if the bucket still contains objects")
	storageDeleteCmd.Flags().BoolVarP(&storageDeleteQuiet, "quiet", "q", false, "Suppress progress and success output (errors only)")
	storageRotateCmd.Flags().BoolVar(&storageRotateNoRestart, "no-restart", false, "Skip restarting the bound deployment's services (pods keep the old, invalid key until restarted)")
	storageCredentialsCmd.Flags().BoolVarP(&storageCredsQuiet, "quiet", "q", false, "Only print the export lines (for eval)")
	// --deployment named the scope of the app's secrets the old command read.
	// A person's key is per bucket, so it means nothing now; it is accepted so
	// scripts that pass it keep working.
	storageCredentialsCmd.Flags().StringVar(&storageCredsDeployment, "deployment", "", "Ignored: a bucket key is per bucket")
	_ = storageCredentialsCmd.Flags().MarkDeprecated("deployment", "a bucket key is per bucket and per person now; the flag is ignored")
}

func runStorageList(cmd *cobra.Command, args []string) {
	if !storageListQuiet {
		fmt.Printf("%s Retrieving buckets...\n", platform.Icon("🪣", "[>]"))
		fmt.Println()
	}

	cfg := config.Load()
	requireToken(cfg)

	list, err := storage.ListBuckets(cfg.APIURL, cfg.APIToken)
	if err != nil {
		fmt.Printf("%s Failed to list buckets: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	if list.Total == 0 {
		if !storageListQuiet {
			fmt.Println("No buckets found.")
		}
		return
	}

	if storageListQuiet {
		for _, name := range list.Buckets {
			fmt.Println(name)
		}
		return
	}

	fmt.Printf("Found %d bucket(s):\n", list.Total)
	fmt.Println()
	for _, name := range list.Buckets {
		fmt.Println("  ", name)
	}
}

func runStorageCreate(cmd *cobra.Command, args []string) {
	name := storageCreateName
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		fmt.Printf("%s Error: bucket name is required (use argument or --name)\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	if storageCreateDeployment != "" {
		fmt.Printf("%s Creating bucket '%s' (scoped to deployment '%s')...\n", platform.Icon("🪣", "[>]"), name, storageCreateDeployment)
	} else {
		fmt.Printf("%s Creating bucket '%s'...\n", platform.Icon("🪣", "[>]"), name)
	}
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	created, err := storage.CreateBucket(cfg.APIURL, cfg.APIToken, name, storageCreateDeployment, storageCreateSize, storageCreateExpireDays)
	if err != nil {
		fmt.Printf("%s Failed to create bucket: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("%s %s\n", platform.Icon("✅", "[OK]"), created.Message)
	fmt.Printf("  Bucket:   %s\n", created.Bucket)
	fmt.Printf("  Endpoint: %s\n", created.Endpoint)
	fmt.Printf("  Quota:    %s\n", storage.FormatBytes(created.QuotaBytes))
	if len(created.SecretNames) > 0 {
		fmt.Println("  Secrets (auto-created):")
		for _, s := range created.SecretNames {
			fmt.Printf("    %s\n", s)
		}
		if storageCreateDeployment != "" {
			fmt.Printf("\n  The secrets are scoped to deployment '%s'.\n", storageCreateDeployment)
			fmt.Println("  They will be injected automatically when that deployment starts.")
		} else {
			fmt.Println("\n  These are global secrets available to all deployments in your org.")
			fmt.Println("  They will be injected automatically on every deploy.")
		}
	}
}

func runStorageDelete(cmd *cobra.Command, args []string) {
	name := args[0]
	if !storageDeleteQuiet {
		fmt.Printf("%s Attempting to delete bucket '%s'...\n", platform.Icon("🗑️", "[DEL]"), name)
		fmt.Println()
	}

	cfg := config.Load()
	requireToken(cfg)

	if !storageDeleteYes {
		warning := fmt.Sprintf("Are you sure you want to delete bucket '%s'? This action cannot be undone.", name)
		if storageDeleteForce {
			warning = fmt.Sprintf("Are you sure you want to delete bucket '%s' AND ALL ITS OBJECTS? This action cannot be undone.", name)
		}
		ok, err := askConfirm(warning)
		if err != nil {
			os.Exit(refuseUnconfirmable(os.Stderr, fmt.Sprintf("deleting bucket '%s'", name)))
		}
		if !ok {
			if !storageDeleteQuiet {
				fmt.Println("Deletion cancelled.")
			}
			os.Exit(0)
		}
	}

	stop := func() {}
	if !storageDeleteQuiet {
		stop = spinner.Start("Deleting", "\033[31m")
	}

	del, err := storage.DeleteBucket(cfg.APIURL, cfg.APIToken, name, storageDeleteForce)
	stop()
	if err != nil {
		if !storageDeleteQuiet {
			fmt.Printf("\r")
		}
		fmt.Printf("%s Failed to delete bucket '%s': %v\n", platform.Icon("❌", "[X]"), name, err)
		os.Exit(1)
	}

	if !storageDeleteQuiet {
		fmt.Printf("\r%s %s\n", platform.Icon("✅", "[OK]"), del.Message)
	}
}

func runStorageRotate(cmd *cobra.Command, args []string) {
	name := args[0]
	fmt.Printf("%s Rotating credentials for bucket '%s'...\n", platform.Icon("🔄", "[>]"), name)
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	stop := spinner.Start("Rotating", "")

	res, err := storage.RotateBucket(cfg.APIURL, cfg.APIToken, name, storageRotateNoRestart)
	stop()
	if err != nil {
		fmt.Printf("\r%s Failed to rotate credentials: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("\r%s %s\n", platform.Icon("✅", "[OK]"), res.Message)
}

func runStorageInfo(cmd *cobra.Command, args []string) {
	fmt.Printf("%s Retrieving bucket usage...\n", platform.Icon("🪣", "[>]"))
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	info, err := storage.BucketsInfo(cfg.APIURL, cfg.APIToken)
	if err != nil {
		fmt.Printf("%s Failed to get bucket info: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	if info.Total == 0 {
		fmt.Println("No buckets found.")
		return
	}

	fmt.Printf("%-32s %12s %10s %12s\n", "BUCKET", "SIZE", "OBJECTS", "QUOTA")
	for _, b := range info.Buckets {
		quota := "unlimited"
		if b.QuotaBytes > 0 {
			quota = storage.FormatBytes(b.QuotaBytes)
		}
		fmt.Printf("%-32s %12s %10d %12s\n", b.Name, storage.FormatBytes(b.SizeBytes), b.Objects, quota)
	}
}

func runStorageCredentials(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	if !cfg.HasToken() {
		// Not requireToken: that writes to stdout, and stdout is what
		// `eval "$(…)"` runs.
		fmt.Fprintf(os.Stderr, "%s Error: API token is required — run 'dibbla login' or set DIBBLA_API_TOKEN\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}
	os.Exit(storageCredentials(os.Stdout, os.Stderr, args[0], storageCredsQuiet, time.Now(),
		func(name string) (*storage.BucketCredentials, error) {
			return storage.IssueBucketCredentials(cfg.APIURL, cfg.APIToken, name)
		}))
}

// storageCredentials asks for the person's key and prints it. stdout carries
// the export lines and nothing else that a shell would run; every error goes
// to stderr, because under `eval "$(…)"` stdout is executed.
func storageCredentials(stdout, stderr io.Writer, name string, quiet bool, now time.Time, issue func(string) (*storage.BucketCredentials, error)) int {
	creds, err := issue(name)
	if err != nil {
		fmt.Fprintf(stderr, "%s Could not get a key for bucket '%s': %v\n", platform.Icon("❌", "[X]"), name, err)
		return 1
	}
	exports := bucketKeyExports(creds)

	if quiet {
		fmt.Fprintln(stdout, strings.Join(exports, "\n"))
		return 0
	}

	fmt.Fprintf(stdout, "%s Your own key for bucket '%s' — this bucket only, %s:\n", platform.Icon("🔑", "[>]"), creds.Bucket, expiryPhrase(creds.ExpiresAt, now))
	fmt.Fprintln(stdout)
	for _, l := range exports {
		fmt.Fprintf(stdout, "  %s\n", l)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "It is not the app's key: the app's STORAGE_%s_* secrets are untouched.\n", storage.EnvName(creds.Bucket))
	fmt.Fprintln(stdout, "Load it into your shell, and ask again when it expires:")
	fmt.Fprintf(stdout, "  eval \"$(dibbla storage credentials %s -q)\"\n", name)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Then e.g.:")
	fmt.Fprintln(stdout, `  aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls "s3://$DIBBLA_BUCKET"`)
	fmt.Fprintln(stdout, `  rclone ls ":s3:$DIBBLA_BUCKET" --s3-env-auth --s3-no-check-bucket --s3-endpoint "$AWS_ENDPOINT_URL"`)
	fmt.Fprintln(stdout, "  (rclone needs --s3-no-check-bucket: the key may use the bucket, not create one)")
	return 0
}

// bucketKeyExports is the key as POSIX shell export lines, in the order the
// old command printed them, with the session token an STS key carries.
func bucketKeyExports(c *storage.BucketCredentials) []string {
	exports := []string{
		"export AWS_ENDPOINT_URL=" + shellQuote(c.Endpoint),
		"export AWS_ACCESS_KEY_ID=" + shellQuote(c.AccessKeyID),
		"export AWS_SECRET_ACCESS_KEY=" + shellQuote(c.SecretAccessKey),
	}
	if c.SessionToken != "" {
		exports = append(exports, "export AWS_SESSION_TOKEN="+shellQuote(c.SessionToken))
	}
	return append(exports, "export DIBBLA_BUCKET="+shellQuote(c.Bucket))
}

// expiryPhrase says when the key stops working, in the person's local time.
func expiryPhrase(expiresAt, now time.Time) string {
	if expiresAt.IsZero() {
		return "valid for about an hour"
	}
	left := expiresAt.Sub(now).Round(time.Minute)
	if left <= 0 {
		return "already expired at " + expiresAt.Local().Format("2006-01-02 15:04 MST")
	}
	return fmt.Sprintf("valid until %s (in %d min)", expiresAt.Local().Format("2006-01-02 15:04 MST"), int(left.Minutes()))
}

// shellQuote wraps a value in single quotes for a POSIX shell, escaping any
// single quote inside it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
