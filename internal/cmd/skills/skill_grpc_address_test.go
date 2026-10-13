package skills

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// DIB-1430: the skill's `mcp:` example set `GRPC_SERVER_ADDRESS:
// grpc.dibbla.com:443` in `dibbla.yaml`. A manifest's `environment:` outranks
// every env source a person can change afterwards (`apps update -e`, secrets),
// so an agent copying that example pinned the worker to Dibbla cloud — on a
// self-hosted install it dialled our production server and nothing the person
// set could move it. The platform supplies the installation's own address
// (`DIBBLA_GRPC_SERVER_ADDRESS`, read by the SDK when `GRPC_SERVER_ADDRESS` is
// unset), so a deployed worker sets none.
//
// Only fenced code is checked — the text an agent copies:
//   - a `GRPC_SERVER_ADDRESS:` mapping (a manifest or compose `environment:`)
//   - `-e` / `--env GRPC_SERVER_ADDRESS=…` (a deploy or `apps update` flag)
//   - `WithGrpcServerAddress("…")` naming anything but a loopback address; in
//     code the option outranks the env var and the platform's address alike.
var (
	grpcAddressMappingRe = regexp.MustCompile(`^\s*(-\s*)?GRPC_SERVER_ADDRESS\s*:`)
	grpcAddressEnvFlagRe = regexp.MustCompile(`(^|\s)(-e|--env)[\s=]+GRPC_SERVER_ADDRESS=`)
	grpcAddressOptionRe  = regexp.MustCompile(`WithGrpcServerAddress\("([^"]*)"\)`)
)

func grpcAddressViolation(line string) bool {
	if grpcAddressMappingRe.MatchString(line) || grpcAddressEnvFlagRe.MatchString(line) {
		return true
	}
	for _, m := range grpcAddressOptionRe.FindAllStringSubmatch(line, -1) {
		addr := m[1]
		if !strings.HasPrefix(addr, "localhost:") && !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "[::1]:") {
			return true
		}
	}
	return false
}

func TestSkill_DeployedWorkerSetsNoGrpcAddress(t *testing.T) {
	docs := skillDocsForSecretScan(t)
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		inFence := false
		for i, line := range strings.Split(docs[n], "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence && grpcAddressViolation(line) {
				t.Errorf("%s:%d: %q pins the gRPC address of a deployed app — the platform supplies the installation's own; leave GRPC_SERVER_ADDRESS unset (sdk-go.md § 8)", n, i+1, strings.TrimSpace(line))
			}
		}
	}

	// The rule itself, where an agent writing a worker or an `mcp:` line reads.
	for _, want := range []struct{ file, needle string }{
		{".claude/skills/dibbla/manifest.md", "**No `GRPC_SERVER_ADDRESS` either.**"},
		{".claude/skills/dibbla/sdk-go.md", "**Deployed on Dibbla: leave it unset.**"},
		{".claude/skills/dibbla/sdk-go.md", "Set no `GRPC_SERVER_ADDRESS`: the platform supplies the address"},
		{".claude/skills/dibbla/pipelines.md", "**A deployed worker registers with the installation it runs on.**"},
	} {
		if !strings.Contains(docs[want.file], want.needle) {
			t.Errorf("%s no longer contains %q", want.file, want.needle)
		}
	}
}

func TestGrpcAddressScan_Rules(t *testing.T) {
	for _, c := range []struct {
		line string
		bad  bool
	}{
		{"      GRPC_SERVER_ADDRESS: grpc.dibbla.com:443", true},
		{"      - GRPC_SERVER_ADDRESS: grpc.example.com:443", true},
		{"dibbla deploy . -e GRPC_SERVER_ADDRESS=grpc.dibbla.com:443", true},
		{"dibbla apps update my-worker --env GRPC_SERVER_ADDRESS=grpc.example.com:443", true},
		{`    sdk.WithGrpcServerAddress("grpc.dibbla.com:443"),`, true},
		{`sdk.WithGrpcServerAddress("localhost:50051") // local runs only`, false},
		{"GRPC_SERVER_ADDRESS=localhost:50051 go run ./cmd/worker", false},
		{"      SERVER_NAME: my-tools", false},
	} {
		if got := grpcAddressViolation(c.line); got != c.bad {
			t.Errorf("grpcAddressViolation(%q) = %v, want %v", c.line, got, c.bad)
		}
	}
}
