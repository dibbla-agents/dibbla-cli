package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The skill is read by AI agents, and an agent copies an example verbatim. An
// example that passes a secret's value on the command line is therefore not an
// illustration: it is an instruction to put that value into the agent's
// transcript, its shell history and whatever logs the session. Since DIB-1337
// secrets are write-only — no CLI command, API or tool hands a value back — and
// since DIB-1339 an env var may not stand in for one. The only way left for a
// value to pass through an agent is for the skill to tell it to type one, so
// these tests hold the skill to never doing that:
//
//   - rule "secrets-set-value": `dibbla secrets set NAME <value>` (or
//     `echo … | dibbla secrets set`) — the value belongs on the person's stdin.
//   - rule "secret-env-flag": `-e NAME=value` / `--env NAME=value` where the name
//     or the value looks like a secret — the platform refuses it, and an agent
//     that types it has already leaked it.
//   - rule "db-connect-printed": `dibbla db connect <name>` run bare in a shell
//     example — its output carries the person's API token, so an agent runs it
//     only inside `$(...)`.
//   - rule "storage-credentials-printed": `dibbla storage credentials <name>`
//     anywhere but `eval "$(dibbla storage credentials <name> -q)"` in a shell
//     example — its output is a bucket key of the person's own (DIB-1344), so
//     an agent loads it into the shell and never onto the screen.
//
// Only code is checked: fenced blocks and inline code spans, the text an agent
// copies. A line may name a bad form to say it is wrong; that is allowed when
// the same sentence (prose) or the line's own comment (fenced code) says
// "never" or "not". The allowance is that narrow on purpose: a whole paragraph
// mentioning "not" somewhere must not excuse an example in it.

// secretNameRe is deploy-api's envpolicy name heuristic (DIB-1339), verbatim, so
// the skill is held to the rule the server enforces.
var secretNameRe = regexp.MustCompile(`(^|_)(SECRET|SECRETS|PASSWORD|PASSWD|PASS|TOKEN|PRIVATE_KEY|API_KEY|ACCESS_KEY|SECRET_KEY|CREDENTIAL|CREDENTIALS|AUTH|DSN)($|_)|_KEY$`)

// credentialValueRes are the credential shapes deploy-api's envpolicy flags,
// loosened for documentation: an example writes `sk-xxx` or `postgres://...`
// where a real value would be longer, and the placeholder is exactly the
// instruction to paste a real one there.
var credentialValueRes = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY`),
	regexp.MustCompile(`^(sk|rk)[-_]`),
	regexp.MustCompile(`^ak_`),
	regexp.MustCompile(`^gh[pousr]_|^github_pat_`),
	regexp.MustCompile(`^xox[abprs]-`),
	regexp.MustCompile(`^AKIA[0-9A-Z]`),
	regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.`),
	// any URL carrying user:password@
	regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^/\s:@]+:[^@/\s]+@`),
	// a database connection string left as a placeholder
	regexp.MustCompile(`^(postgres|postgresql|mysql|mongodb(\+srv)?|redis|rediss|amqps?)://(\.\.\.|…)`),
}

func looksLikeCredential(v string) bool {
	v = strings.Trim(v, `"'`)
	for _, re := range credentialValueRes {
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

var (
	secretsSetRe   = regexp.MustCompile(`secrets\s+set\s+(\S+)(?:\s+(\S+))?`)
	secretsPipeRe  = regexp.MustCompile(`\b(echo|printf)\b[^|]*\|\s*dibbla\s+secrets\s+set\b`)
	secretNameTok  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*|<[^<>]+>|\$\{?[A-Za-z_][A-Za-z0-9_]*\}?)$`)
	envFlagRe      = regexp.MustCompile(`(?:^|\s)(?:-e|--env)(?:\s+|=)["']?([A-Za-z_][A-Za-z0-9_]*)=("[^"]*"|'[^']*'|\S*)`)
	dbConnectRe    = regexp.MustCompile(`dibbla\s+db\s+connect\b`)
	storageCredsRe = regexp.MustCompile(`dibbla\s+(?:storage|buckets)\s+credentials\b`)
	quietFlagRe    = regexp.MustCompile(`(?:^|\s)(?:-q|--quiet)(?:\s|$)`)
	evalOpenRe     = regexp.MustCompile(`\beval\s+"?\$\($`)
	negationRe     = regexp.MustCompile(`(?i)\b(never|not)\b`)
	personRe       = regexp.MustCompile(`(?i)\bperson\b`)
	shellFenceLang = map[string]bool{"bash": true, "sh": true, "shell": true, "zsh": true, "console": true}
)

// matchSecretsSetValue reports the first `secrets set NAME <value>` in code
// whose value is given on the command line.
func matchSecretsSetValue(code string) (string, bool) {
	if m := secretsPipeRe.FindString(code); m != "" {
		return m, true
	}
	for _, m := range secretsSetRe.FindAllStringSubmatch(code, -1) {
		name, next := m[1], m[2]
		if !secretNameTok.MatchString(name) {
			continue // prose such as "secrets set, the console", not a command
		}
		if valueArgAllowed(next) {
			continue
		}
		return m[0], true
	}
	return "", false
}

// valueArgAllowed reports whether the token after the secret's name leaves the
// value off the command line.
func valueArgAllowed(tok string) bool {
	switch {
	case tok == "":
		return true // end of the command: value on stdin
	case strings.HasPrefix(tok, "-"), strings.HasPrefix(tok, "[-"):
		return true // a flag, or an optional flag in a usage synopsis
	case tok == "[value]":
		return true // the usage synopsis names the argument; it supplies none
	case strings.ContainsAny(tok[:1], "#|;&)\\"):
		return true // a comment, a pipe, a separator or a line continuation
	case tok == "<":
		return true // `< file`: stdin from a file
	case strings.HasPrefix(tok, "<") && !strings.HasPrefix(tok, "<<") && !strings.Contains(tok, ">"):
		return true // `<file`; `<value>` is a placeholder and `<<<` a here-string
	}
	return false
}

// matchSecretEnvFlag reports the first `-e NAME=value` / `--env NAME=value` in
// code whose name or value looks like a secret.
func matchSecretEnvFlag(code string) (string, bool) {
	for _, m := range envFlagRe.FindAllStringSubmatch(code, -1) {
		if secretNameRe.MatchString(strings.ToUpper(m[1])) || looksLikeCredential(m[2]) {
			return strings.TrimSpace(m[0]), true
		}
	}
	return "", false
}

// matchBareDBConnect reports a `dibbla db connect` that is not the inside of a
// `$(...)`: one whose output — the person's API token — reaches the screen.
func matchBareDBConnect(code string) (string, bool) {
	for _, loc := range dbConnectRe.FindAllStringIndex(code, -1) {
		if !strings.HasSuffix(strings.TrimRight(code[:loc[0]], " \t"), "$(") {
			return code[loc[0]:loc[1]], true
		}
	}
	return "", false
}

// matchPrintedStorageCredentials reports a `dibbla storage credentials` that is
// not `eval "$(… -q)"`: anywhere else its output — a bucket key — lands on the
// screen, in a variable an agent may echo, or (without -q) is the decorated
// text rather than export lines.
func matchPrintedStorageCredentials(code string) (string, bool) {
	for _, loc := range storageCredsRe.FindAllStringIndex(code, -1) {
		if !evalOpenRe.MatchString(strings.TrimRight(code[:loc[0]], " \t")) {
			return code[loc[0]:loc[1]], true
		}
		rest := code[loc[1]:]
		if i := strings.Index(rest, ")"); i >= 0 {
			rest = rest[:i]
		}
		if !quietFlagRe.MatchString(rest) {
			return code[loc[0]:loc[1]], true
		}
	}
	return "", false
}

type secretViolation struct {
	file string
	line int
	rule string
	text string
}

func (v secretViolation) String() string {
	return fmt.Sprintf("%s:%d: %s: %q", v.file, v.line, v.rule, v.text)
}

// shellComment returns the comment part of a shell line: from a `#` at the
// start or after whitespace. Good enough for documentation, where a `#` inside
// a quoted argument does not occur in the commands these rules look at.
func shellComment(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "#") {
		return trimmed
	}
	if i := strings.Index(line, " #"); i >= 0 {
		return line[i:]
	}
	return ""
}

// scanSkillDoc applies the four rules to one markdown document.
func scanSkillDoc(name, content string) []secretViolation {
	var out []secretViolation
	lines := strings.Split(content, "\n")

	var (
		inFence    bool
		fenceMark  string
		fenceLang  string
		para       []int // line indexes of the current prose paragraph
		flushProse = func() {
			if len(para) > 0 {
				out = append(out, scanProse(name, lines, para)...)
				para = nil
			}
		}
	)
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			flushProse()
			inFence, fenceMark = true, trimmed[:3]
			fenceLang = strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "`~")))
			if f := strings.Fields(fenceLang); len(f) > 0 {
				fenceLang = f[0]
			}
			continue
		}
		if inFence {
			if strings.HasPrefix(trimmed, fenceMark) && strings.TrimSpace(strings.TrimLeft(trimmed, fenceMark[:1])) == "" {
				inFence = false
				continue
			}
			comment := shellComment(line)
			allowed := negationRe.MatchString(comment)
			if m, ok := matchSecretsSetValue(line); ok && !allowed {
				out = append(out, secretViolation{name, i + 1, "secrets-set-value", m})
			}
			if m, ok := matchSecretEnvFlag(line); ok && !allowed {
				out = append(out, secretViolation{name, i + 1, "secret-env-flag", m})
			}
			if shellFenceLang[fenceLang] && !strings.HasPrefix(trimmed, "#") && !personRe.MatchString(comment) {
				if m, ok := matchBareDBConnect(line); ok {
					out = append(out, secretViolation{name, i + 1, "db-connect-printed", m})
				}
				if m, ok := matchPrintedStorageCredentials(line); ok {
					out = append(out, secretViolation{name, i + 1, "storage-credentials-printed", m})
				}
			}
			continue
		}
		// Prose. A line that is itself a command gets the db-connect rule too.
		if strings.HasPrefix(trimmed, "dibbla db connect") && !personRe.MatchString(shellComment(line)) {
			out = append(out, secretViolation{name, i + 1, "db-connect-printed", strings.TrimSpace(line)})
		}
		if strings.HasPrefix(trimmed, "dibbla ") && storageCredsRe.MatchString(trimmed) && !personRe.MatchString(shellComment(line)) {
			if m, ok := matchPrintedStorageCredentials(trimmed); ok {
				out = append(out, secretViolation{name, i + 1, "storage-credentials-printed", m})
			}
		}
		if strings.TrimSpace(line) == "" {
			flushProse()
			continue
		}
		para = append(para, i)
	}
	flushProse()
	return out
}

// scanProse checks the inline code spans of one paragraph. Spans may wrap a
// line; the allowance looks at the sentence the span sits in, with every code
// span masked out so a word inside code never counts.
func scanProse(name string, lines []string, idx []int) []secretViolation {
	var b strings.Builder
	lineAt := make([]int, 0, 64) // byte offset where each paragraph line starts
	for k, i := range idx {
		if k > 0 {
			b.WriteByte('\n')
		}
		lineAt = append(lineAt, b.Len())
		b.WriteString(lines[i])
	}
	text := b.String()
	lineOf := func(off int) int {
		j := sort.Search(len(lineAt), func(j int) bool { return lineAt[j] > off }) - 1
		return idx[j] + 1
	}

	type span struct{ start, end int } // [start,end) of the content
	var spans []span
	masked := []byte(text)
	for i := 0; i < len(text); {
		if text[i] != '`' {
			i++
			continue
		}
		n := 0
		for i+n < len(text) && text[i+n] == '`' {
			n++
		}
		open := i + n
		closeAt := -1
		for j := open; j < len(text); {
			if text[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(text) && text[j+m] == '`' {
				m++
			}
			if m == n {
				closeAt = j
				break
			}
			j += m
		}
		if closeAt < 0 {
			i = open
			continue
		}
		spans = append(spans, span{open, closeAt})
		for k := i; k < closeAt+n; k++ {
			if masked[k] != '\n' {
				masked[k] = 'x'
			}
		}
		i = closeAt + n
	}

	sentence := func(s span) string {
		start := 0
		for _, sep := range []string{". ", ".\n", "! ", "? ", "|", "\n- ", "\n* ", "\n> "} {
			if k := strings.LastIndex(string(masked[:s.start]), sep); k >= 0 && k+len(sep) > start {
				start = k + len(sep)
			}
		}
		end := len(masked)
		for _, sep := range []string{". ", ".\n", "! ", "? ", "|", "\n- ", "\n* ", "\n> "} {
			if k := strings.Index(string(masked[s.end:]), sep); k >= 0 && s.end+k < end {
				end = s.end + k
			}
		}
		return string(masked[start:end])
	}

	var out []secretViolation
	for _, s := range spans {
		code := text[s.start:s.end]
		if m, ok := matchSecretsSetValue(code); ok && !negationRe.MatchString(sentence(s)) {
			out = append(out, secretViolation{name, lineOf(s.start), "secrets-set-value", m})
		}
		if m, ok := matchSecretEnvFlag(code); ok && !negationRe.MatchString(sentence(s)) {
			out = append(out, secretViolation{name, lineOf(s.start), "secret-env-flag", m})
		}
	}
	return out
}

// skillDocsForSecretScan returns every markdown file of the authored skill plus
// the root SKILL.md, keyed by a name that reads well in a failure.
func skillDocsForSecretScan(t *testing.T) map[string]string {
	t.Helper()
	docs := map[string]string{}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("read %s: %v", sourceDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sourceDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		docs[".claude/skills/dibbla/"+e.Name()] = string(data)
	}
	root, err := os.ReadFile("../../../SKILL.md")
	if err != nil {
		t.Fatalf("read root SKILL.md: %v", err)
	}
	docs["SKILL.md"] = string(root)
	return docs
}

// TestSkill_NeverHandsAnAgentASecretValue is the regression gate: no example in
// the published skill may tell an agent to type a secret's value, pass one as
// an env var, or print the person's API token.
func TestSkill_NeverHandsAnAgentASecretValue(t *testing.T) {
	docs := skillDocsForSecretScan(t)
	for _, must := range []string{"SKILL.md", ".claude/skills/dibbla/SKILL.md", ".claude/skills/dibbla/examples.md", ".claude/skills/dibbla/reference.md", ".claude/skills/dibbla/manifest.md"} {
		if _, ok := docs[must]; !ok {
			t.Fatalf("%s not found — the scan is not proving anything", must)
		}
	}
	names := make([]string, 0, len(docs))
	for n := range docs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, v := range scanSkillDoc(n, docs[n]) {
			t.Errorf("%s — an agent copies examples verbatim; keep the value on the person's side (see \"Secrets — rules for an AI agent\")", v)
		}
	}

	// Both skills carry the rules an agent reads before any example.
	for _, n := range []string{"SKILL.md", ".claude/skills/dibbla/SKILL.md"} {
		for _, needle := range []string{"## Secrets — rules for an AI agent", "--allow-secret-env", "ENV_SHADOWS_SECRET", "`.env.example`"} {
			if !strings.Contains(docs[n], needle) {
				t.Errorf("%s no longer contains %q", n, needle)
			}
		}
	}
}

// TestSecretScan_Rules proves each rule fails on the shapes it exists for, and
// stays quiet on the forms the skill uses on purpose — run through the same
// scanner, fences, prose and allowance included.
func TestSecretScan_Rules(t *testing.T) {
	fence := func(lang, body string) string { return "```" + lang + "\n" + body + "\n```\n" }
	cases := []struct {
		name string
		doc  string
		want string // rule expected, "" for none
	}{
		// secrets-set-value
		{"literal value in fence", fence("bash", `dibbla secrets set API_KEY "my-secret-value"`), "secrets-set-value"},
		{"bare value with -d", fence("bash", `dibbla secrets set NPM_TOKEN xxx -d myapp --service web`), "secrets-set-value"},
		{"placeholder value", fence("bash", `dibbla secrets set NPM_TOKEN_SECRET <token> -d myapp`), "secrets-set-value"},
		{"ellipsis value", fence("bash", `dibbla secrets set DATABASE_URL postgres://... -d myapp`), "secrets-set-value"},
		{"here-string", fence("bash", `dibbla secrets set API_KEY <<< "sk_live_x"`), "secrets-set-value"},
		{"echo pipe", fence("bash", `echo "my-secret-value" | dibbla secrets set API_KEY`), "secrets-set-value"},
		{"value in inline code", "Run `dibbla secrets set API_KEY sk-xxx -d myapp` first.\n", "secrets-set-value"},
		{"placeholder pair in table", "| Fix | run `dibbla secrets set <NAME> <value> -d <alias>` first |\n", "secrets-set-value"},
		{"not elsewhere in paragraph is no excuse", "This is not optional.\nSet it: `dibbla secrets set API_KEY abc`.\n", "secrets-set-value"},
		{"stdin form", fence("bash", `dibbla secrets set API_KEY -d myapp`), ""},
		{"stdin form, comment", fence("bash", `dibbla secrets set API_KEY            # paste it, then Ctrl-D`), ""},
		{"file redirect", fence("bash", `dibbla secrets set TLS_KEY -d myapp < tls.key`), ""},
		{"usage synopsis", "**Usage:** `dibbla secrets set <name> [value] [-d <alias>]`\n", ""},
		{"prose naming the command", "Ask the person (`dibbla secrets set`, the console).\n", ""},
		{"named as the wrong way", "**Never type a value:** not `dibbla secrets set NAME value`, not `-e`.\n", ""},
		{"wrapped sentence still counts", "An agent never supplies it — not as the\nargument (`dibbla secrets set API_KEY \"…\"`).\n", ""},

		// secret-env-flag
		{"secret name in -e", fence("bash", `dibbla deploy . --alias a -e API_KEY=secret -e NODE_ENV=production`), "secret-env-flag"},
		{"password URL in -e", fence("bash", `  -e DATABASE_URL="postgres://user:pass@host:5432/db" \`), "secret-env-flag"},
		{"db placeholder in -e", fence("bash", `  -e DATABASE_URL="postgres://..." \`), "secret-env-flag"},
		{"stripe key value", fence("bash", `dibbla apps update a -e STRIPE="sk_live_123"`), "secret-env-flag"},
		{"token name, --env= form", fence("bash", `dibbla run --env=GITHUB_TOKEN=ghp_abc`), "secret-env-flag"},
		{"docker password", "Use `docker run -e POSTGRES_PASSWORD=… postgres` locally.\n", "secret-env-flag"},
		{"key suffix", fence("bash", `dibbla deploy -e STRIPE_KEY=abc`), "secret-env-flag"},
		{"plain env", fence("bash", `dibbla apps update my-app -e LOG_LEVEL=debug -e NEW_VAR=value`), ""},
		{"generic synopsis", "`secrets import <file> [-e KEY=VAL]`\n", ""},
		{"computed db url", fence("bash", `  -e DATABASE_URL_MY_DB="$(dibbla db connect my_db -q)" my-app`), ""},
		{"env-file is not -e", fence("bash", `dibbla deploy . --env-file ../config/app.env`), ""},
		{"named as refused", "A name like `-e API_KEY=x` is never accepted from an agent.\n", ""},

		// db-connect-printed
		{"bare in bash", fence("bash", `dibbla db connect myapp -q`), "db-connect-printed"},
		{"psql without substitution", fence("sh", `psql $(echo x) && dibbla db connect myapp`), "db-connect-printed"},
		{"bare command line in prose", "dibbla db connect myapp\n", "db-connect-printed"},
		{"inside $()", fence("bash", `psql "$(dibbla db connect myapp -q)"`), ""},
		{"export", fence("bash", `export DATABASE_URL=$(dibbla db connect myapp -q)`), ""},
		{"for a person", fence("bash", `dibbla db connect myapp   # a person at a terminal only`), ""},
		{"comment line (CLI output)", fence("bash", `# DATABASE_URL_X: for a connection of your own, use "$(dibbla db connect x -q)" in the start command — it carries your API token, so not in this file`), ""},
		{"usage in prose", "**Usage:** `dibbla db connect <name> [-q]`\n", ""},
		{"yaml fence is not a shell", fence("yaml", `cmd: dibbla db connect myapp`), ""},

		// storage-credentials-printed (DIB-1344)
		{"storage key bare", fence("bash", `dibbla storage credentials my-uploads`), "storage-credentials-printed"},
		{"storage key bare -q", fence("bash", `dibbla storage credentials my-uploads -q`), "storage-credentials-printed"},
		{"buckets alias bare", fence("sh", `dibbla buckets credentials my-uploads -q`), "storage-credentials-printed"},
		{"substituted but echoed", fence("bash", `echo "$(dibbla storage credentials my-uploads -q)"`), "storage-credentials-printed"},
		{"into a variable", fence("bash", `KEYS=$(dibbla storage credentials my-uploads -q)`), "storage-credentials-printed"},
		{"eval without -q", fence("bash", `eval "$(dibbla storage credentials my-uploads)"`), "storage-credentials-printed"},
		{"bare storage key line in prose", "dibbla storage credentials my-uploads\n", "storage-credentials-printed"},
		{"eval -q", fence("bash", `eval "$(dibbla storage credentials my-uploads -q)" && aws s3 ls "s3://$DIBBLA_BUCKET"`), ""},
		{"eval --quiet, unquoted", fence("zsh", `eval $(dibbla storage credentials my-uploads --quiet)`), ""},
		{"storage key for a person", fence("bash", `dibbla storage credentials my-uploads   # a person at a terminal only`), ""},
		{"storage key usage in prose", "**Usage:** `dibbla storage credentials <name> [-q]`\n", ""},
		{"storage key in a yaml fence", fence("yaml", `cmd: dibbla storage credentials my-uploads`), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scanSkillDoc("case.md", c.doc)
			if c.want == "" {
				if len(got) != 0 {
					t.Errorf("flagged %v, want nothing", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("not flagged, want %s", c.want)
			}
			if got[0].rule != c.want {
				t.Errorf("rule %s, want %s (%v)", got[0].rule, c.want, got)
			}
			if got[0].line < 1 {
				t.Errorf("line %d, want a line number", got[0].line)
			}
		})
	}
}

// TestSecretScan_ReportsTheLine pins that a failure names the line the example
// is on, so the fix is one jump away.
func TestSecretScan_ReportsTheLine(t *testing.T) {
	doc := "# Title\n\nSome prose.\n\n```bash\ndibbla apps list\ndibbla secrets set API_KEY abc\n```\n\nThen `dibbla deploy -e API_TOKEN=x`.\n"
	got := scanSkillDoc("doc.md", doc)
	want := []struct {
		line int
		rule string
	}{{7, "secrets-set-value"}, {10, "secret-env-flag"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %d violations", got, len(want))
	}
	for i, w := range want {
		if got[i].line != w.line || got[i].rule != w.rule {
			t.Errorf("violation %d = %v, want line %d rule %s", i, got[i], w.line, w.rule)
		}
	}
}
