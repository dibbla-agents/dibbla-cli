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
		description: "Use the Dibbla CLI to scaffold projects, run task pipelines, deploy apps, and manage databases, secrets, and workflows.",
		embedFS:     dibblaSkillFS,
		root:        dibblaSkillRoot,
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
