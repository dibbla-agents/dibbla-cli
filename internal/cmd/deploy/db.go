package deploy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/db"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/spinner"
	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Manage Dibbla databases",
	Long:  `Provides commands to list, create, delete, dump, and restore managed databases.`,
}

var dbListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all managed databases",
	Long:  `Fetches and displays a list of all databases managed by the Dibbla platform.`,
	Run:   runDbList,
}

var dbCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new database",
	Long:  `Creates a new managed database. Provide the name as an argument or via --name.`,
	Args:  cobra.MaximumNArgs(1),
	Run:   runDbCreate,
}

var dbDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a database",
	Long:  `Deletes a specific database by name. This action cannot be undone.`,
	Args:  cobra.ExactArgs(1),
	Run:   runDbDelete,
}

var dbRestoreCmd = &cobra.Command{
	Use:   "restore <name>",
	Short: "Restore a database from a dump file",
	Long:  `Restores a database from an uploaded dump file (e.g. custom-format pg_dump archive).`,
	Args:  cobra.ExactArgs(1),
	Run:   runDbRestore,
}

var dbDumpCmd = &cobra.Command{
	Use:   "dump <name> [--output file.dump]",
	Short: "Dump a database",
	Long:  `Downloads a database dump as an application/octet-stream (custom-format pg_dump archive).`,
	Args:  cobra.ExactArgs(1),
	Run:   runDbDump,
}

var dbConnectCmd = &cobra.Command{
	Use:   "connect <name>",
	Short: "Print the connection string for a database",
	Long: `Prints a psql-compatible connection string for connecting to a database
via the Dibbla database proxy.

The proxy host and TLS mode are derived from DIBBLA_API_URL:
  api.dibbla.com  → db.dibbla.com  (sslmode=require)
  api.dibbla.net  → db.dibbla.net  (sslmode=disable, internal)
Override with DIBBLA_DB_HOST / DIBBLA_DB_PORT / DIBBLA_DB_SSLMODE.

Uses your current API token as the password.

Examples:
  dibbla db connect myapp
  psql $(dibbla db connect myapp --quiet)
  export DATABASE_URL=$(dibbla db connect myapp -q)`,
	Args: cobra.ExactArgs(1),
	Run:  runDbConnect,
}

var (
	dbDeleteYes        bool
	dbDeleteQuiet      bool
	dbListQuiet        bool
	dbConnectQuiet     bool
	dbCreateName       string
	dbCreateDeployment string
	dbRestoreFile      string
	dbDumpOutput       string
)

func init() {
	dbCmd.AddCommand(dbListCmd)
	dbCmd.AddCommand(dbCreateCmd)
	dbCmd.AddCommand(dbDeleteCmd)
	dbCmd.AddCommand(dbRestoreCmd)
	dbCmd.AddCommand(dbDumpCmd)
	dbCmd.AddCommand(dbConnectCmd)

	dbDeleteCmd.Flags().BoolVarP(&dbDeleteYes, "yes", "y", false, "Skip confirmation prompt")
	dbDeleteCmd.Flags().BoolVarP(&dbDeleteQuiet, "quiet", "q", false, "Suppress progress and success output (errors only)")
	dbListCmd.Flags().BoolVarP(&dbListQuiet, "quiet", "q", false, "Only print database names, one per line (for scripting)")
	dbCreateCmd.Flags().StringVar(&dbCreateName, "name", "", "Name of the database to create")
	dbCreateCmd.Flags().StringVar(&dbCreateDeployment, "deployment", "", "Scope the database and its DATABASE_URL secret to a specific deployment")
	dbRestoreCmd.Flags().StringVarP(&dbRestoreFile, "file", "f", "", "Path to the dump file to restore (required)")
	dbRestoreCmd.MarkFlagRequired("file")
	dbDumpCmd.Flags().StringVarP(&dbDumpOutput, "output", "o", "", "Output file path (default: <name>.dump)")
	dbConnectCmd.Flags().BoolVarP(&dbConnectQuiet, "quiet", "q", false, "Only print the connection string (for scripting)")
}

func runDbList(cmd *cobra.Command, args []string) {
	if !dbListQuiet {
		fmt.Printf("%s Retrieving databases...\n", platform.Icon("🌱", "[>]"))
		fmt.Println()
	}

	cfg := config.Load()
	requireToken(cfg)

	list, err := db.ListDatabases(cfg.APIURL, cfg.APIToken)
	if err != nil {
		fmt.Printf("%s Failed to list databases: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	if list.Total == 0 {
		if !dbListQuiet {
			fmt.Println("No databases found.")
		}
		return
	}

	if dbListQuiet {
		for _, name := range list.Databases {
			fmt.Println(name)
		}
		return
	}

	fmt.Printf("Found %d database(s):\n", list.Total)
	fmt.Println()
	for _, name := range list.Databases {
		fmt.Println("  ", name)
	}
}

func runDbCreate(cmd *cobra.Command, args []string) {
	name := dbCreateName
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		fmt.Printf("%s Error: database name is required (use argument or --name)\n", platform.Icon("❌", "[X]"))
		os.Exit(1)
	}

	if dbCreateDeployment != "" {
		fmt.Printf("%s Creating database '%s' (scoped to deployment '%s')...\n", platform.Icon("🌱", "[>]"), name, dbCreateDeployment)
	} else {
		fmt.Printf("%s Creating database '%s'...\n", platform.Icon("🌱", "[>]"), name)
	}
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	created, err := db.CreateDatabase(cfg.APIURL, cfg.APIToken, name, dbCreateDeployment)
	if err != nil {
		fmt.Printf("%s Failed to create database: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("%s %s\n", platform.Icon("✅", "[OK]"), created.Message)
	fmt.Printf("  Database: %s\n", created.Database)
	if created.SecretName != "" {
		fmt.Printf("  Secret:   %s (auto-created)\n", created.SecretName)
		if dbCreateDeployment != "" {
			fmt.Printf("\n  The secret is scoped to deployment '%s'.\n", dbCreateDeployment)
			fmt.Println("  It will be injected automatically when that deployment starts.")
		} else {
			fmt.Println("\n  This is a global secret available to all deployments in your org.")
			fmt.Println("  It will be injected automatically on every deploy.")
		}
	}
}

func runDbDelete(cmd *cobra.Command, args []string) {
	name := args[0]
	if !dbDeleteQuiet {
		fmt.Printf("%s Attempting to delete database '%s'...\n", platform.Icon("🗑️", "[DEL]"), name)
		fmt.Println()
	}

	cfg := config.Load()
	requireToken(cfg)

	if !dbDeleteYes {
		if !askConfirm(fmt.Sprintf("Are you sure you want to delete database '%s'? This action cannot be undone.", name)) {
			if !dbDeleteQuiet {
				fmt.Println("Deletion cancelled.")
			}
			os.Exit(0)
		}
	}

	stop := func() {}
	if !dbDeleteQuiet {
		stop = spinner.Start("Deleting", "\033[31m")
	}

	del, err := db.DeleteDatabase(cfg.APIURL, cfg.APIToken, name)
	stop()
	if err != nil {
		if !dbDeleteQuiet {
			fmt.Printf("\r")
		}
		fmt.Printf("%s Failed to delete database '%s': %v\n", platform.Icon("❌", "[X]"), name, err)
		os.Exit(1)
	}

	if !dbDeleteQuiet {
		fmt.Printf("\r%s %s\n", platform.Icon("✅", "[OK]"), del.Message)
	}
}

func runDbRestore(cmd *cobra.Command, args []string) {
	name := args[0]
	fmt.Printf("%s Restoring database '%s' from %s...\n", platform.Icon("🌱", "[>]"), name, dbRestoreFile)
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	stop := spinner.Start("Restoring", "")

	res, err := db.RestoreDatabase(cfg.APIURL, cfg.APIToken, name, dbRestoreFile)
	stop()
	if err != nil {
		fmt.Printf("\r%s Failed to restore database: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("\r%s %s\n", platform.Icon("✅", "[OK]"), res.Message)
}

func runDbDump(cmd *cobra.Command, args []string) {
	name := args[0]
	outPath := dbDumpOutput
	if outPath == "" {
		outPath = name + ".dump"
	}

	fmt.Printf("%s Dumping database '%s' to %s...\n", platform.Icon("🌱", "[>]"), name, outPath)
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	f, err := os.Create(outPath)
	if err != nil {
		fmt.Printf("%s Failed to create output file: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}
	defer f.Close()

	stop := spinner.Start("Dumping", "")

	err = db.DumpDatabase(cfg.APIURL, cfg.APIToken, name, f)
	stop()
	if err != nil {
		f.Close()
		os.Remove(outPath)
		fmt.Printf("\r%s Failed to dump database: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	abs, _ := filepath.Abs(outPath)
	fmt.Printf("\r%s Dump saved to %s\n", platform.Icon("✅", "[OK]"), abs)
}

func runDbConnect(cmd *cobra.Command, args []string) {
	name := args[0]

	cfg := config.Load()
	requireToken(cfg)

	host, port, sslmode := dbProxyEndpoint(cfg.APIURL, cfg.APIToken, os.Getenv)
	connStr := fmt.Sprintf("postgres://dibbla:%s@%s:%s/%s?sslmode=%s", cfg.APIToken, host, port, name, sslmode)

	if dbConnectQuiet {
		fmt.Print(connStr)
		return
	}

	fmt.Printf("%s Connection string for database '%s':\n", platform.Icon("🔗", "[>]"), name)
	fmt.Println()
	fmt.Printf("  %s\n", connStr)
	fmt.Println()
	fmt.Println("Quick connect:")
	fmt.Printf("  psql $(dibbla db connect %s -q)\n", name)
	fmt.Println()
	fmt.Println("Or export as an environment variable:")
	fmt.Printf("  export DATABASE_URL=$(dibbla db connect %s -q)\n", name)
}

// dbProxyInfo is the shape of GET /db/proxy-info.
type dbProxyInfo struct {
	Host    string `json:"host"`
	Port    string `json:"port"`
	SSLMode string `json:"sslmode"`
}

// dbProxyInfoFetcher returns the API-served proxy info. ok=false when the
// API isn't reachable or doesn't expose the endpoint, in which case the
// caller falls back to derivation.
type dbProxyInfoFetcher func(apiURL, token string) (info dbProxyInfo, ok bool)

// fetchDBProxyInfoOverHTTP is the production fetcher: calls
// <apiURL>/api/deploy/db/proxy-info with a short timeout. Returns ok=false
// on any network error, non-200 status, or unparseable body so callers
// fall back gracefully when run against an older deploy-api or when the
// api-gateway hasn't whitelisted the path.
func fetchDBProxyInfoOverHTTP(apiURL, token string) (dbProxyInfo, bool) {
	base := strings.TrimSpace(apiURL)
	if base == "" {
		return dbProxyInfo{}, false
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")
	req, err := http.NewRequest(http.MethodGet, base+"/api/deploy/db/proxy-info", nil)
	if err != nil {
		return dbProxyInfo{}, false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return dbProxyInfo{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return dbProxyInfo{}, false
	}
	var info dbProxyInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return dbProxyInfo{}, false
	}
	if info.Host == "" || info.Port == "" {
		return dbProxyInfo{}, false
	}
	return info, true
}

// dbProxyEndpoint resolves the database proxy host, port, and sslmode for
// `dibbla db connect`. It calls deploy-api for `/api/deploy/db/proxy-info`
// first (auth'd with the user's API token); if the call fails (network
// error, 404 on older servers, 401 on a missing/expired token, malformed
// body) it derives sensible defaults from the API URL. DIBBLA_DB_HOST /
// DIBBLA_DB_PORT / DIBBLA_DB_SSLMODE override any resolved value.
//
// Derivation fallback (when the API can't be reached):
//   - host: parse apiURL; if the hostname begins with "api.", swap that label
//     for "db." (api.dibbla.com → db.dibbla.com, api.dibbla.net → db.dibbla.net).
//     Otherwise fall back to "db.dibbla.com".
//   - port: 30432 (the legacy NodePort).
//   - sslmode: "disable" for known-internal hosts (api.dibbla.net, localhost,
//     127.0.0.1, ::1, hostnames ending in ".local"); "require" elsewhere.
func dbProxyEndpoint(apiURL, token string, getenv func(string) string) (host, port, sslmode string) {
	return dbProxyEndpointWith(apiURL, token, getenv, fetchDBProxyInfoOverHTTP)
}

// dbProxyEndpointWith is the testable form of dbProxyEndpoint: callers
// inject the fetcher.
func dbProxyEndpointWith(apiURL, token string, getenv func(string) string, fetch dbProxyInfoFetcher) (host, port, sslmode string) {
	if info, ok := fetch(apiURL, token); ok {
		host, port, sslmode = info.Host, info.Port, info.SSLMode
	} else {
		host, sslmode = deriveDBHostAndSSLMode(apiURL)
		port = "30432"
	}

	if v := getenv("DIBBLA_DB_HOST"); v != "" {
		host = v
	}
	if v := getenv("DIBBLA_DB_PORT"); v != "" {
		port = v
	}
	if v := getenv("DIBBLA_DB_SSLMODE"); v != "" {
		sslmode = v
	}
	return host, port, sslmode
}

func deriveDBHostAndSSLMode(apiURL string) (host, sslmode string) {
	const fallbackHost = "db.dibbla.com"

	apiHost := parseAPIHost(apiURL)
	if apiHost == "" {
		return fallbackHost, "require"
	}

	switch {
	case strings.HasPrefix(apiHost, "api."):
		host = "db." + strings.TrimPrefix(apiHost, "api.")
	default:
		host = fallbackHost
	}

	if isInternalAPIHost(apiHost) {
		sslmode = "disable"
	} else {
		sslmode = "require"
	}
	return host, sslmode
}

// parseAPIHost extracts the hostname from an API URL. Accepts bare hostnames
// ("api.dibbla.net") as well as full URLs ("https://api.dibbla.net").
func parseAPIHost(apiURL string) string {
	s := strings.TrimSpace(apiURL)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func isInternalAPIHost(host string) bool {
	if host == "" {
		return false
	}
	switch host {
	case "api.dibbla.net", "localhost", "127.0.0.1", "::1":
		return true
	}
	if strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}
