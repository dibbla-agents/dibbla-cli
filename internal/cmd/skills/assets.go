package skills

import "embed"

//go:generate sh -c "rm -rf assets/dibbla && mkdir -p assets/dibbla && cp ../../../.claude/skills/dibbla/*.md assets/dibbla/"

//go:embed assets/dibbla
var dibblaSkillFS embed.FS

const dibblaSkillRoot = "assets/dibbla"

//go:generate sh -c "rm -rf assets/dibbla-ai-gateway && mkdir -p assets/dibbla-ai-gateway && cp ../../../.claude/skills/dibbla-ai-gateway/*.md assets/dibbla-ai-gateway/"

//go:embed assets/dibbla-ai-gateway
var dibblaAIGatewaySkillFS embed.FS

const dibblaAIGatewaySkillRoot = "assets/dibbla-ai-gateway"
