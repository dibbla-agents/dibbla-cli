package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/spf13/cobra"
)

var complainCmd = &cobra.Command{
	Use:   "complain <message>",
	Short: "File a complaint",
	Long:  `Submit a complaint to the Dibbla team. All arguments are joined into a single message.`,
	Args:  cobra.MinimumNArgs(1),
	Run:   runComplain,
}

var complainListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your complaints",
	Long:  `List all complaints filed by your organization.`,
	Args:  cobra.NoArgs,
	Run:   runComplainList,
}

var complainDeleteYes bool

var complainDeleteCmd = &cobra.Command{
	Use:   "delete <complaint-id>",
	Short: "Delete a complaint",
	Long:  `Delete a complaint by its ID. Only your own complaints can be deleted.`,
	Args:  cobra.ExactArgs(1),
	Run:   runComplainDelete,
}

func init() {
	complainCmd.AddCommand(complainListCmd)
	complainCmd.AddCommand(complainDeleteCmd)
	complainDeleteCmd.Flags().BoolVarP(&complainDeleteYes, "yes", "y", false, "Skip confirmation prompt")
}

type complaintResponse struct {
	ID string `json:"id"`
}

func runComplain(cmd *cobra.Command, args []string) {
	// If the first arg is a subcommand, cobra handles it. This only runs
	// when args don't match a subcommand, so treat them as a message.
	message := strings.Join(args, " ")

	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run 'dibbla login' first.")
		os.Exit(3)
	}

	client := apiclient.NewClient(cfg.APIURL, cfg.APIToken, false)

	body := map[string]string{"message": message}
	resp, err := client.Post("/api/complaints", body)
	if err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var complaint complaintResponse
	if err := json.Unmarshal(resp.Body, &complaint); err != nil {
		fmt.Println("Complaint received. Thank you for your feedback!")
		return
	}

	fmt.Printf("Complaint %s received. Thank you for your feedback!\n", complaint.ID)
}

type complaintListItem struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
	CreatedAt string `json:"created_at"`
}

func runComplainList(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run 'dibbla login' first.")
		os.Exit(3)
	}

	client := apiclient.NewClient(cfg.APIURL, cfg.APIToken, false)

	resp, err := client.Get("/api/complaints")
	if err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var complaints []complaintListItem
	if err := json.Unmarshal(resp.Body, &complaints); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse response\n")
		os.Exit(1)
	}

	if len(complaints) == 0 {
		fmt.Println("No complaints found.")
		return
	}

	fmt.Printf("%-38s %-20s %-30s %s\n", "ID", "USER", "DATE", "MESSAGE")
	fmt.Printf("%-38s %-20s %-30s %s\n", "---", "----", "----", "-------")
	for _, c := range complaints {
		msg := c.Message
		if len(msg) > 60 {
			msg = msg[:57] + "..."
		}
		fmt.Printf("%-38s %-20s %-30s %s\n", c.ID, c.UserName, c.CreatedAt, msg)
	}
}

func runComplainDelete(cmd *cobra.Command, args []string) {
	id := args[0]

	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run 'dibbla login' first.")
		os.Exit(3)
	}

	if !complainDeleteYes {
		var confirm bool
		prompt := &survey.Confirm{
			Message: fmt.Sprintf("Delete complaint %s?", id),
			Default: false,
		}
		if err := survey.AskOne(prompt, &confirm); err != nil || !confirm {
			fmt.Println("Deletion cancelled.")
			return
		}
	}

	client := apiclient.NewClient(cfg.APIURL, cfg.APIToken, false)

	_, err := client.Delete("/api/complaints/" + id)
	if err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Complaint %s deleted.\n", id)
}
