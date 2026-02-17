package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/dibbla-agents/dibbla-cli/internal/db"
	"github.com/dibbla-agents/dibbla-cli/internal/platform"
	"github.com/dibbla-agents/dibbla-cli/internal/prompt"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(dbCmd)
	dbCmd.AddCommand(dbListCmd)
	dbCmd.AddCommand(dbCreateCmd)
	dbCmd.AddCommand(dbDeleteCmd)
	dbCmd.AddCommand(dbRestoreCmd)
	dbCmd.AddCommand(dbDumpCmd)

	dbDeleteCmd.Flags().BoolVarP(&dbDeleteYes, "yes", "y", false, "Skip confirmation prompt")
	dbDeleteCmd.Flags().BoolVarP(&dbDeleteQuiet, "quiet", "q", false, "Suppress progress and success output (errors only)")
	dbListCmd.Flags().BoolVarP(&dbListQuiet, "quiet", "q", false, "Only print database names, one per line (for scripting)")
	dbCreateCmd.Flags().StringVar(&dbCreateName, "name", "", "Name of the database to create")
	dbRestoreCmd.Flags().StringVarP(&dbRestoreFile, "file", "f", "", "Path to the dump file to restore (required)")
	dbRestoreCmd.MarkFlagRequired("file")
	dbDumpCmd.Flags().StringVarP(&dbDumpOutput, "output", "o", "", "Output file path (default: <name>.dump)")
}

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

var (
	dbDeleteYes   bool
	dbDeleteQuiet bool
	dbListQuiet   bool
	dbCreateName  string
	dbRestoreFile string
	dbDumpOutput  string
)

func requireToken(cfg *config.Config) {
	if !cfg.HasToken() {
		fmt.Printf("%s Error: DIBBLA_API_TOKEN is required\n", platform.Icon("❌", "[X]"))
		fmt.Println()
		fmt.Println("Set your API token in one of these ways:")
		fmt.Println("  1. Create a .env file with: DIBBLA_API_TOKEN=your_token")
		fmt.Println("  2. Export environment variable: export DIBBLA_API_TOKEN=your_token")
		fmt.Println()
		fmt.Println("Get your API token at: https://app.dibbla.com/settings/api-tokens")
		os.Exit(1)
	}
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

	fmt.Printf("%s Creating database '%s'...\n", platform.Icon("🌱", "[>]"), name)
	fmt.Println()

	cfg := config.Load()
	requireToken(cfg)

	created, err := db.CreateDatabase(cfg.APIURL, cfg.APIToken, name)
	if err != nil {
		fmt.Printf("%s Failed to create database: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	fmt.Printf("%s %s\n", platform.Icon("✅", "[OK]"), created.Message)
	fmt.Printf("  Database: %s\n", created.Database)
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
		if !prompt.AskConfirm(fmt.Sprintf("Are you sure you want to delete database '%s'? This action cannot be undone.", name)) {
			if !dbDeleteQuiet {
				fmt.Println("Deletion cancelled.")
			}
			os.Exit(0)
		}
	}

	var done chan struct{}
	if !dbDeleteQuiet {
		done = make(chan struct{})
		go func() {
			if platform.SupportsUnicode() {
				spinStates := []string{
					"\033[31m⠋\033[0m", "\033[31m⠙\033[0m", "\033[31m⠹\033[0m", "\033[31m⠸\033[0m",
					"\033[31m⠼\033[0m", "\033[31m⠴\033[0m", "\033[31m⠦\033[0m", "\033[31m⠧\033[0m",
					"\033[31m⠇\033[0m", "\033[31m⠏\033[0m",
				}
				i := 0
				for {
					select {
					case <-done:
						fmt.Printf("\r \r")
						return
					default:
						fmt.Printf("\r%s Deleting...", spinStates[i%len(spinStates)])
						i++
						time.Sleep(120 * time.Millisecond)
					}
				}
			} else {
				spinStates := []string{"|", "/", "-", "\\"}
				i := 0
				for {
					select {
					case <-done:
						fmt.Printf("\r \r")
						return
					default:
						fmt.Printf("\r[%s] Deleting...", spinStates[i%len(spinStates)])
						i++
						time.Sleep(120 * time.Millisecond)
					}
				}
			}
		}()
	}

	del, err := db.DeleteDatabase(cfg.APIURL, cfg.APIToken, name)
	if !dbDeleteQuiet {
		close(done)
	}
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

	done := make(chan struct{})
	go func() {
		if platform.SupportsUnicode() {
			spinStates := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r%s Restoring...", spinStates[i%len(spinStates)])
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		} else {
			spinStates := []string{"|", "/", "-", "\\"}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r[%s] Restoring...", spinStates[i%len(spinStates)])
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		}
	}()

	res, err := db.RestoreDatabase(cfg.APIURL, cfg.APIToken, name, dbRestoreFile)
	close(done)
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

	done := make(chan struct{})
	go func() {
		if platform.SupportsUnicode() {
			spinStates := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r%s Dumping...", spinStates[i%len(spinStates)])
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		} else {
			spinStates := []string{"|", "/", "-", "\\"}
			i := 0
			for {
				select {
				case <-done:
					fmt.Printf("\r \r")
					return
				default:
					fmt.Printf("\r[%s] Dumping...", spinStates[i%len(spinStates)])
					i++
					time.Sleep(120 * time.Millisecond)
				}
			}
		}
	}()

	err = db.DumpDatabase(cfg.APIURL, cfg.APIToken, name, f)
	close(done)
	if err != nil {
		f.Close()
		os.Remove(outPath)
		fmt.Printf("\r%s Failed to dump database: %v\n", platform.Icon("❌", "[X]"), err)
		os.Exit(1)
	}

	abs, _ := filepath.Abs(outPath)
	fmt.Printf("\r%s Dump saved to %s\n", platform.Icon("✅", "[OK]"), abs)
}
