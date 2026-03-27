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

var feedbackCmd = &cobra.Command{
	Use:   "feedback <message>",
	Short: "Send feedback",
	Long:  `Submit feedback to the Dibbla team. All arguments are joined into a single message.`,
	Args:  cobra.MinimumNArgs(1),
	Run:   runFeedback,
}

var feedbackListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your feedback",
	Long:  `List all feedback filed by your organization.`,
	Args:  cobra.NoArgs,
	Run:   runFeedbackList,
}

var feedbackDeleteYes bool

var feedbackDeleteCmd = &cobra.Command{
	Use:   "delete <feedback-id>",
	Short: "Delete feedback",
	Long:  `Delete feedback by its ID. Only your own feedback can be deleted.`,
	Args:  cobra.ExactArgs(1),
	Run:   runFeedbackDelete,
}

func init() {
	feedbackCmd.AddCommand(feedbackListCmd)
	feedbackCmd.AddCommand(feedbackDeleteCmd)
	feedbackDeleteCmd.Flags().BoolVarP(&feedbackDeleteYes, "yes", "y", false, "Skip confirmation prompt")
}

type feedbackResponse struct {
	ID string `json:"id"`
}

func runFeedback(cmd *cobra.Command, args []string) {
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
	resp, err := client.Post("/api/feedback", body)
	if err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var fb feedbackResponse
	if err := json.Unmarshal(resp.Body, &fb); err != nil {
		fmt.Println("Feedback received. Thank you!")
		return
	}

	fmt.Printf("Feedback %s received. Thank you!\n", fb.ID)
}

type feedbackListItem struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	UserName  string `json:"user_name"`
	UserEmail string `json:"user_email"`
	CreatedAt string `json:"created_at"`
}

func runFeedbackList(cmd *cobra.Command, args []string) {
	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run 'dibbla login' first.")
		os.Exit(3)
	}

	client := apiclient.NewClient(cfg.APIURL, cfg.APIToken, false)

	resp, err := client.Get("/api/feedback")
	if err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var items []feedbackListItem
	if err := json.Unmarshal(resp.Body, &items); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse response\n")
		os.Exit(1)
	}

	if len(items) == 0 {
		fmt.Println("No feedback found.")
		return
	}

	fmt.Printf("%-38s %-20s %-30s %s\n", "ID", "USER", "DATE", "MESSAGE")
	fmt.Printf("%-38s %-20s %-30s %s\n", "---", "----", "----", "-------")
	for _, c := range items {
		msg := c.Message
		if len(msg) > 60 {
			msg = msg[:57] + "..."
		}
		fmt.Printf("%-38s %-20s %-30s %s\n", c.ID, c.UserName, c.CreatedAt, msg)
	}
}

func runFeedbackDelete(cmd *cobra.Command, args []string) {
	id := args[0]

	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run 'dibbla login' first.")
		os.Exit(3)
	}

	if !feedbackDeleteYes {
		var confirm bool
		prompt := &survey.Confirm{
			Message: fmt.Sprintf("Delete feedback %s?", id),
			Default: false,
		}
		if err := survey.AskOne(prompt, &confirm); err != nil || !confirm {
			fmt.Println("Deletion cancelled.")
			return
		}
	}

	client := apiclient.NewClient(cfg.APIURL, cfg.APIToken, false)

	_, err := client.Delete("/api/feedback/" + id)
	if err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Feedback %s deleted.\n", id)
}
