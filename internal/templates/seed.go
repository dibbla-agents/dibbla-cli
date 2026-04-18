package templates

// Embedded returns a minimal fallback manifest baked into the binary for
// true offline disasters (no cache, no network). Keep this list small —
// the hosted manifest is the source of truth.
func Embedded() *Manifest {
	return &Manifest{
		Version: SupportedVersion,
		Templates: []Template{
			{
				ID:           "getting-started",
				Name:         "Simple Website",
				Description:  "Simple Go + Vite web site, ready to start building upon",
				Category:     "starter",
				BootstrapURL: "https://raw.githubusercontent.com/dibbla-agents/dibbla-public-templates/master/getting-started.dibbla-task.yaml",
				RepoURL:      "https://github.com/dibbla-agents/dibbla-public-templates.git",
				TemplatePath: "getting-started-1",
			},
			{
				ID:           "expense-reporter",
				Name:         "Expense Reporter",
				Description:  "Upload PDF receipts and let AI extract expense details into a Google Sheet",
				Category:     "starter",
				BootstrapURL: "https://raw.githubusercontent.com/dibbla-agents/dibbla-public-templates/master/expense-reporter.dibbla-task.yaml",
				RepoURL:      "https://github.com/dibbla-agents/dibbla-public-templates.git",
				TemplatePath: "expense-reporter-template-1",
			},
		},
	}
}
