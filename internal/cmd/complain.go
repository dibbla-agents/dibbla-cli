package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"strings"

	"github.com/dibbla-agents/dibbla-cli/internal/apiclient"
	"github.com/dibbla-agents/dibbla-cli/internal/config"
	"github.com/spf13/cobra"
)

var wittyResponses = []string{
	"Complaint filed successfully. Our team of highly trained complaint specialists has been notified. Your grievance is very important to us.",
	"Complaint received. A single tear has been shed in solidarity. We're on it.",
	"Loud and clear. Your complaint has been carved into our wall of accountability.",
	"Complaint logged. Rest assured, someone somewhere is already feeling mildly guilty about this.",
	"Filed under 'Things That Must Be Fixed.' You have excellent taste in complaints.",
	"Your complaint has been received with the gravity it deserves. The wheels of justice turn slowly, but they turn.",
}

var complainCmd = &cobra.Command{
	Use:   "complain <message>",
	Short: "File a complaint",
	Long:  `Submit a complaint to the Dibbla team. All arguments are joined into a single message.`,
	Args:  cobra.MinimumNArgs(1),
	Run:   runComplain,
}

func runComplain(cmd *cobra.Command, args []string) {
	message := strings.Join(args, " ")

	cfg := config.Load()
	if cfg.APIToken == "" {
		fmt.Fprintln(os.Stderr, "Not logged in. Run 'dibbla login' first.")
		os.Exit(3)
	}

	client := apiclient.NewClient(cfg.APIURL, cfg.APIToken, false)

	body := map[string]string{"message": message}
	_, err := client.Post("/api/complaints", body)
	if err != nil {
		if apiErr, ok := err.(*apiclient.APIError); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Message)
			os.Exit(apiclient.ExitCodeForStatus(apiErr.StatusCode))
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	response := wittyResponses[rand.Intn(len(wittyResponses))]
	fmt.Println(response)
}
