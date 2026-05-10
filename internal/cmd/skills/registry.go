package skills

import (
	"embed"
	"fmt"
	"io/fs"
)

type skillEntry struct {
	id          string
	description string
	embedFS     embed.FS
	root        string
}

func (s *skillEntry) files() (fs.FS, error) {
	return fs.Sub(s.embedFS, s.root)
}

var registry = []skillEntry{
	{
		id:          "dibbla",
		description: "Use the Dibbla CLI to scaffold projects, run task pipelines, deploy apps, and manage databases, secrets, and workflows. Includes a platform compatibility reference for Dockerfile, .dibblaignore, and deploy-readiness questions.",
		embedFS:     dibblaSkillFS,
		root:        dibblaSkillRoot,
	},
	{
		id:          "dibbla-ai-gateway",
		description: "Point local AI coding assistants (Claude Code, opencode, Cursor, Cline, Windsurf, Zed) and ad-hoc curl scripts at the Dibbla AI gateway so every LLM call is captured under the user's Dibbla org. Covers `dibbla ai url|env|test`, per-tool config, and the optional `X-Dibbla-App` attribution header.",
		embedFS:     dibblaAIGatewaySkillFS,
		root:        dibblaAIGatewaySkillRoot,
	},
}

func all() []skillEntry {
	return registry
}

func find(id string) (*skillEntry, error) {
	for i := range registry {
		if registry[i].id == id {
			return &registry[i], nil
		}
	}
	return nil, fmt.Errorf("unknown skill %q (run 'dibbla skills list')", id)
}
