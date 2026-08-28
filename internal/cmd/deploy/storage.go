package deploy

import (
	"fmt"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/secrets"
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
(STORAGE_<NAME>_ENDPOINT/BUCKET/ACCESS_KEY_ID/SECRET_ACCESS_KEY).`,
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

var storageCredentialsCmd = &cobra.Command{
	Use:   "credentials <name>",
	Short: "Print export lines for a bucket's credentials",
	Long: `Prints shell export lines for using a bucket from your own tools
(aws CLI, mc, rclone, SDKs). The values come from the injected secrets.

Examples:
  eval "$(dibbla storage credentials mybucket -q)"
  aws --endpoint-url "$AWS_ENDPOINT_URL" s3 ls s3://mybucket`,
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
	storageCredentialsCmd.Flags().StringVar(&storageCredsDeployment, "deployment", "", "Deployment the bucket's secrets are scoped to (default: org-global)")
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
	name := args[0]

	cfg := config.Load()
	requireToken(cfg)

	prefix := "STORAGE_" + storage.EnvName(name) + "_"
	values := map[string]string{}
	for _, suffix := range []string{"ENDPOINT", "BUCKET", "ACCESS_KEY_ID", "SECRET_ACCESS_KEY"} {
		sec, err := secrets.GetSecret(cfg.APIURL, cfg.APIToken, prefix+suffix, storageCredsDeployment, "")
		if err != nil {
			fmt.Printf("%s Failed to read secret %s: %v\n", platform.Icon("❌", "[X]"), prefix+suffix, err)
			if storageCredsDeployment == "" {
				fmt.Println("\nIf the bucket is scoped to a deployment, pass --deployment <alias>.")
			}
			os.Exit(1)
		}
		values[suffix] = sec.Value
	}

	exports := []string{
		"export AWS_ENDPOINT_URL=" + shellQuote(values["ENDPOINT"]),
		"export AWS_ACCESS_KEY_ID=" + shellQuote(values["ACCESS_KEY_ID"]),
		"export AWS_SECRET_ACCESS_KEY=" + shellQuote(values["SECRET_ACCESS_KEY"]),
		"export DIBBLA_BUCKET=" + shellQuote(values["BUCKET"]),
	}

	if storageCredsQuiet {
		fmt.Println(strings.Join(exports, "\n"))
		return
	}

	fmt.Printf("%s Credentials for bucket '%s':\n", platform.Icon("🔗", "[>]"), name)
	fmt.Println()
	for _, l := range exports {
		fmt.Printf("  %s\n", l)
	}
	fmt.Println()
	fmt.Println("Load them into your shell:")
	fmt.Printf("  eval \"$(dibbla storage credentials %s -q)\"\n", name)
	fmt.Println()
	fmt.Println("Then e.g.:")
	fmt.Printf("  aws --endpoint-url \"$AWS_ENDPOINT_URL\" s3 ls \"s3://$DIBBLA_BUCKET\"\n")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
