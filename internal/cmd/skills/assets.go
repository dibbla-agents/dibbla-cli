package skills

import "embed"

//go:generate sh -c "rm -rf assets/dibbla && mkdir -p assets/dibbla && cp ../../../.claude/skills/dibbla/*.md assets/dibbla/"

//go:embed assets/dibbla
var dibblaSkillFS embed.FS

const dibblaSkillRoot = "assets/dibbla"
