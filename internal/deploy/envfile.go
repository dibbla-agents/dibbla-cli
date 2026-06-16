package deploy

import (
	"fmt"
	"strings"

	"github.com/joho/godotenv"
)

// MergeEnvFileAndFlags reads an optional godotenv file as the base layer, then
// overlays KEY=value flags on top (flags win per key; for a key repeated across
// flags the last one wins). An empty envFile means flags only; the returned map
// is non-nil and may be empty.
//
// The precedence mirrors `dibbla run` (internal/cmd/run/run.go buildEnv) so the
// behaviour is identical across run, deploy, apps update, and secrets import:
// the file is the base, individual -e flags override specific keys.
//
// It returns an error — before the caller acts on the result — when the file is
// missing/unreadable, godotenv rejects a line, or a flag is not of the form
// KEY=value. Callers can therefore fail closed (no partial deploy/import).
func MergeEnvFileAndFlags(envFile string, flags []string) (map[string]string, error) {
	env := map[string]string{}

	if envFile != "" {
		loaded, err := godotenv.Read(envFile)
		if err != nil {
			return nil, fmt.Errorf("loading env file %q: %w", envFile, err)
		}
		for k, v := range loaded {
			env[k] = v
		}
	}

	for _, kv := range flags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid env value %q (want KEY=value)", kv)
		}
		env[k] = v
	}

	return env, nil
}
