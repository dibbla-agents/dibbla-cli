package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// FileName is the only name the platform looks for, at the app root.
const FileName = "dibbla-checks.yaml"

// The server's error vocabulary, reused verbatim so the words a user reads
// locally are the words the deploy would have given them.
const (
	CodeInvalid             = "APPLICATION_CHECKS_INVALID"
	CodeUnsupported         = "APPLICATION_CHECKS_VERSION_UNSUPPORTED"
	CodeDescriptionRequired = "APPLICATION_CHECKS_DESCRIPTION_REQUIRED"
	CodeInlineSecret        = "APPLICATION_CHECKS_INLINE_SECRET"
)

// Finding is one reason the file would be rejected.
type Finding struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Report is the outcome of a local validation.
//
// Unverified is the field that keeps this honest. It lists the rules this copy
// cannot decide without the server — never as failures, always as "not checked
// here" — so a green result is never read as a promise.
type Report struct {
	Present      bool      `json:"present"`
	Path         string    `json:"path,omitempty"`
	Valid        bool      `json:"valid"`
	CheckIDs     []string  `json:"check_ids,omitempty"`
	Findings     []Finding `json:"findings,omitempty"`
	Unverified   []string  `json:"unverified,omitempty"`
	SourceCommit string    `json:"schema_source_commit,omitempty"`
}

// unverifiedRules are the loader rules that genuinely need deployment context.
// They are stated in full rather than summarised: a vague "some things were not
// checked" says nothing a reader can act on.
var unverifiedRules = []string{
	"route names are matched against the deployment's real routes only on the server",
	"secret_ref names are resolved against the app's secrets only on the server",
	"referenced files (a semantic check's judge rubric) are read and size-bounded only on the server",
	"the org's Application Checks capability and the app's own enable switch are server state",
}

// Validate reads dibbla-checks.yaml at root and reports what can be decided
// locally. An absent file is not a failure: most apps ship none.
func Validate(root string) (Report, error) {
	path := filepath.Join(root, FileName)
	source, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Report{}, nil
	}
	if err != nil {
		return Report{}, err
	}
	return ValidateSource(path, source), nil
}

// ValidateSource validates bytes already in hand. path is used for reporting
// only.
func ValidateSource(path string, source []byte) Report {
	report := Report{Present: true, Path: path, SourceCommit: SourceCommit, Unverified: unverifiedRules}

	var document any
	if err := yaml.Unmarshal(source, &document); err != nil {
		report.Findings = append(report.Findings, Finding{Code: CodeInvalid, Detail: "invalid YAML: " + err.Error()})
		return report
	}
	encoded, err := json.Marshal(normalizeYAML(document))
	if err != nil {
		report.Findings = append(report.Findings, Finding{Code: CodeInvalid, Detail: "invalid document: " + err.Error()})
		return report
	}

	var probe probeFile
	readable := json.Unmarshal(encoded, &probe) == nil

	if readable && probe.Version != nil && *probe.Version != 1 {
		report.Findings = append(report.Findings, Finding{
			Code:   CodeUnsupported,
			Detail: fmt.Sprintf("version %d is unsupported; v1 is the only schema this CLI knows", *probe.Version),
		})
		return report
	}

	// The targeted question first, in the same order and the same words the
	// server uses, so a file with many checks names the one at fault instead of
	// reporting a oneOf mismatch against an index the author never wrote.
	if readable {
		for i, check := range probe.Checks {
			if check.Description != nil && strings.TrimSpace(*check.Description) != "" {
				continue
			}
			report.Findings = append(report.Findings, Finding{
				Code: CodeDescriptionRequired,
				Detail: describeSubject(i, check.ID) +
					": description is required — write one sentence about why this check exists and what it means that it failed, so whoever it wakes knows what broke",
			})
			break
		}
	}

	if len(report.Findings) == 0 {
		if err := validateAgainstSchema(encoded); err != nil {
			report.Findings = append(report.Findings, Finding{Code: CodeInvalid, Detail: err.Error()})
		}
	}

	// Two loader rules that JSON Schema structurally cannot see, and that both
	// bite in practice. Everything else semantic stays on the server.
	if readable && len(report.Findings) == 0 {
		report.Findings = append(report.Findings, localSemanticFindings(probe.Checks)...)
	}

	if readable {
		for _, check := range probe.Checks {
			if check.ID != "" {
				report.CheckIDs = append(report.CheckIDs, check.ID)
			}
		}
		sort.Strings(report.CheckIDs)
	}
	report.Valid = len(report.Findings) == 0
	return report
}

func describeSubject(index int, id string) string {
	if strings.TrimSpace(id) != "" {
		return fmt.Sprintf("check %q", id)
	}
	return fmt.Sprintf("the check at position %d", index+1)
}

// probeFile is the narrow view this package needs of the document: enough to
// name a check, and to see the two loader rules JSON Schema cannot express.
// Anything it cannot read is left to the schema.
type probeFile struct {
	Version *int         `json:"version"`
	Checks  []probeCheck `json:"checks"`
}

type probeCheck struct {
	ID            string      `json:"id"`
	Description   *string     `json:"description"`
	Kind          string      `json:"kind"`
	IdentityGrant string      `json:"identity_grant"`
	Steps         []probeStep `json:"steps"`
}

type probeStep struct {
	Click *struct {
		Control string `json:"control"`
	} `json:"click"`
	Fill *struct {
		Control string `json:"control"`
		Value   struct {
			Literal *string `json:"literal"`
		} `json:"value"`
	} `json:"fill"`
	Request *struct {
		Headers map[string]struct {
			Literal *string `json:"literal"`
		} `json:"headers"`
	} `json:"request"`
}

// localSemanticFindings mirrors exactly two of the loader's semantic rules —
// the two JSON Schema cannot express and that are known to bite:
//
//   - a browser journey that can mutate needs an identity_grant. This is the
//     rule that once put a file the loader rejects into the published guide,
//     because schema validation reported it clean.
//   - a literal value on a credential-looking header or control must be a
//     secret_ref instead.
//
// Nothing else semantic is mirrored. Every rule that needs deployment context
// is listed in Unverified instead: guessing at it here would produce a local NO
// on a file the server accepts, which is the one failure mode this must not
// have.
func localSemanticFindings(checks []probeCheck) []Finding {
	var findings []Finding
	for i, check := range checks {
		subject := describeSubject(i, check.ID)
		mutationCapable := false
		for _, step := range check.Steps {
			if step.Click != nil {
				mutationCapable = true
			}
			if step.Fill != nil {
				mutationCapable = true
				if step.Fill.Value.Literal != nil && sensitiveName(step.Fill.Control) {
					findings = append(findings, Finding{
						Code:   CodeInlineSecret,
						Detail: fmt.Sprintf("%s: fill control %q must use secret_ref, not a literal value", subject, step.Fill.Control),
					})
				}
			}
			if step.Request != nil {
				names := make([]string, 0, len(step.Request.Headers))
				for name := range step.Request.Headers {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					if step.Request.Headers[name].Literal != nil && sensitiveName(name) {
						findings = append(findings, Finding{
							Code:   CodeInlineSecret,
							Detail: fmt.Sprintf("%s: header %q must use secret_ref, not a literal value", subject, name),
						})
					}
				}
			}
		}
		if check.Kind == "browser_journey" && mutationCapable && check.IdentityGrant == "" {
			findings = append(findings, Finding{
				Code:   CodeInvalid,
				Detail: subject + ": click/fill browser actions require identity_grant for an isolated fixture",
			})
		}
	}
	return findings
}

// sensitiveName is the server's rule, character for character.
func sensitiveName(name string) bool {
	name = strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	return strings.Contains(name, "authorization") || strings.Contains(name, "cookie") ||
		strings.Contains(name, "password") || strings.Contains(name, "secret") ||
		strings.Contains(name, "token") || strings.Contains(name, "api-key")
}

var (
	compiled    *jsonschema.Schema
	compileOnce sync.Once
	compileErr  error
)

func validateAgainstSchema(document []byte) error {
	compileOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		compiler.Draft = jsonschema.Draft2020
		if err := compiler.AddResource("application-checks-v1.json", bytes.NewReader(schemaV1)); err != nil {
			compileErr = err
			return
		}
		compiled, compileErr = compiler.Compile("application-checks-v1.json")
	})
	if compileErr != nil {
		return fmt.Errorf("compile application checks schema: %w", compileErr)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode application checks JSON: %w", err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("application checks schema v1: %w", err)
	}
	return nil
}

// normalizeYAML turns yaml.v3's map[any]any into JSON-marshalable shapes.
func normalizeYAML(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = normalizeYAML(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[fmt.Sprint(k)] = normalizeYAML(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = normalizeYAML(v)
		}
		return out
	default:
		return value
	}
}
