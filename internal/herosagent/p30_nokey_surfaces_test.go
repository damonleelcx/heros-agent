package herosagent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// P30 task 10.17 — the no-key fence across ALL FOUR surfaces the task names: the console's request
// schema, the storage schema, the logs, and the rendered output.
//
// # Why this file exists beside p30_nokey_test.go
//
// That one walks this package's Go types by reflection, which is the right check for the definition
// itself and cannot see anything else. D5's claim is bigger: "no field, column, log line or response
// may carry a key value". Three of those four live outside this package, and a fence that checked only
// the one it can reach would be reporting on a quarter of the promise.
//
// # 🔴 AUTO-DISCOVERING, not a whitelist — including the storage schema
//
// The columns are read out of the migration FILES rather than from a list of tables somebody maintains.
// A whitelist is satisfied by a table added tomorrow, which is exactly the case that matters: nobody
// adds a `provider_key` column on the day the fence is written. So this parses every `CREATE TABLE
// heros_*` and every `ALTER TABLE heros_* ADD COLUMN`, and asks of each column name whether a key could
// live in it.

// keyShapedName matches a name a credential would plausibly live in.
//
// 🔴 Name COMPONENTS, not substrings. `tokens_in` is a COUNT and `max_tokens` is a ceiling; a substring
// match on "token" flags both, and a fence that cries wolf on two legitimate columns is one somebody
// loosens. This is the same lesson §8's reflective check learned when it flagged `MaxTokens`.
var keyShapedName = regexp.MustCompile(
	`(?i)(^|_)(api_?key|apikey|secret|password|passwd|bearer|credential_value|private_key|access_key)(_|$)`)

func repoRootFromAgent(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// 🔴 SURFACE 1 — THE STORAGE SCHEMA. Every column of every `heros_*` table, discovered from the SQL.
func TestNoHerosColumnCouldCarryAKey(t *testing.T) {
	root := repoRootFromAgent(t)
	files, err := filepath.Glob(filepath.Join(root, "db", "migrations", "postgres", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}

	createRe := regexp.MustCompile(`(?is)CREATE TABLE IF NOT EXISTS\s+(heros_\w+)\s*\((.*?)\n\);`)
	alterRe := regexp.MustCompile(`(?i)ALTER TABLE\s+(heros_\w+)\s+ADD COLUMN(?:\s+IF NOT EXISTS)?\s+(\w+)`)

	found := map[string][]string{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := stripSQLComments(string(raw))
		for _, m := range createRe.FindAllStringSubmatch(src, -1) {
			table, body := m[1], m[2]
			for _, line := range strings.Split(body, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(strings.ToUpper(line), "CONSTRAINT") ||
					strings.HasPrefix(strings.ToUpper(line), "PRIMARY KEY") ||
					strings.HasPrefix(strings.ToUpper(line), "UNIQUE") ||
					strings.HasPrefix(strings.ToUpper(line), "CHECK") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				found[table] = append(found[table], strings.Trim(fields[0], ","))
			}
		}
		for _, m := range alterRe.FindAllStringSubmatch(src, -1) {
			found[m[1]] = append(found[m[1]], m[2])
		}
	}

	// 🔴 ANTI-VACUITY. A regex that stopped matching would report a clean schema, which is the same
	// output as a clean schema — so the fence asserts it FOUND the tables this phase created.
	for _, table := range []string{"heros_agent_version", "heros_inference", "heros_spend",
		"heros_tenant_placement", "heros_cap"} {
		if len(found[table]) == 0 {
			t.Fatalf("the column scan found no columns for %s. Either the migration moved or the scan "+
				"is broken — and a broken scan reports a clean schema, which is what a clean schema "+
				"reports too.", table)
		}
	}

	var offenders []string
	total := 0
	for table, columns := range found {
		for _, col := range columns {
			total++
			if keyShapedName.MatchString(col) {
				offenders = append(offenders, table+"."+col)
			}
		}
	}
	if total < 25 {
		t.Fatalf("only %d columns were discovered across the heros_* tables, which is too few for the "+
			"five tables this phase creates — the scan is not seeing the schema", total)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("these columns could hold a provider key: %s\n\n"+
			"  D5: the credential is a provider NAME resolved at use through the deployment's secrets "+
			"source. A column is not a place to put one — it puts plaintext keys in a database, in "+
			"backups, and in every dump anybody takes of it.", strings.Join(offenders, ", "))
	}
	t.Logf("scanned %d columns across %d heros_* tables", total, len(found))
}

// 🔴 SURFACE 2 — THE CONSOLE'S REQUEST SCHEMA and 🔴 SURFACE 4 — THE RENDERED OUTPUT.
//
// The operator console is where a key would be typed, so the fence is over the INPUTS the agent pages
// render as much as over the request types behind them. An `<input type="password">` on an agent
// surface is the exact affordance D5 exists to remove: "Level 1 on the ladder is not tradeable against
// the convenience of a text field."
func TestNoAgentConsoleSurfaceOffersAFieldForAKey(t *testing.T) {
	root := repoRootFromAgent(t)

	// The agent surfaces, both consoles. Discovered by walking rather than listed, so a page added
	// tomorrow is covered the day it lands.
	var pages []string
	for _, dir := range []string{
		filepath.Join(root, "web", "admin-console", "src", "app", "agent"),
		filepath.Join(root, "web", "console", "src", "app", "app", "workflows"),
	} {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".ts") {
				pages = append(pages, path)
			}
			return nil
		})
	}
	if len(pages) == 0 {
		t.Fatal("no agent console pages were found, so this fence is scanning nothing")
	}

	var offenders []string
	for _, page := range pages {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		rel, _ := filepath.Rel(root, page)

		// A password input on an agent surface. 🚫 There is no legitimate one: the credential axis
		// binds a provider NAME chosen from a list.
		if strings.Contains(src, `type="password"`) {
			offenders = append(offenders, rel+": renders a password input")
		}
		// A field whose NAME says key. Checked against the JSX attribute and the object key forms.
		for _, m := range regexp.MustCompile(`(?i)\b(name|id|key)="([\w-]+)"`).FindAllStringSubmatch(src, -1) {
			if keyShapedName.MatchString(strings.ReplaceAll(m[2], "-", "_")) {
				offenders = append(offenders, rel+": a field named "+m[2])
			}
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("an agent console surface offers somewhere to type a key:\n  %s\n\n"+
			"  The credential axis binds a provider NAME from a list. A text field for a value is the "+
			"affordance D5 exists to remove, and its presence is the thing to fence — not the behaviour "+
			"of whatever currently reads it.", strings.Join(offenders, "\n  "))
	}
	t.Logf("scanned %d agent console files", len(pages))
}

// 🔴 SURFACE 3 — THE LOGS. No log or error line in this package formats a resolved credential.
//
// The realistic failure is not `log.Printf("key=%s", key)`. It is `%v` on a struct that happens to
// carry one, or an error wrapping a provider client's own message — which is why `providergateway`
// scrubs credentials out of every error it returns, and why this checks that nothing here re-introduces
// one by formatting a value it obtained.
func TestNoLogOrErrorInThisPackageFormatsACredential(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	format := regexp.MustCompile(`(?i)(log\.\w+|fmt\.(Errorf|Sprintf|Printf)|t\.(Logf|Errorf))\(`)
	// Identifiers that hold a resolved secret in this package's neighbourhood. If one appears as an
	// ARGUMENT to a formatting call, the value reaches a string.
	secretIdent := regexp.MustCompile(`(?i)\b(cred\b|credential\b|creds\b|apiKey|secretValue|bearer)\b`)

	var offenders []string
	scanned := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			code := line
			if j := strings.Index(code, "//"); j >= 0 {
				code = code[:j]
			}
			if !format.MatchString(code) {
				continue
			}
			scanned++
			// 🔴 STRING LITERALS ARE STRIPPED BEFORE THE MATCH. Without it the fence flagged four lines
			// whose MESSAGE contains the word "credential" — `fmt.Errorf("the critic credential: %w", err)`
			// — while the arguments carried nothing. A fence that fires on its own explanatory prose is
			// one somebody deletes, and it is the same mistake the coverage fence made by matching
			// comments.
			if secretIdent.MatchString(stripGoStringLiterals(code)) {
				offenders = append(offenders, file+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}
	if scanned < 20 {
		t.Fatalf("only %d formatting calls were scanned in this package, which is too few — the scan is "+
			"not reading the source", scanned)
	}
	if len(offenders) > 0 {
		t.Errorf("a formatting call takes something credential-shaped as an argument:\n  %s\n\n"+
			"  The realistic failure is not a literal key in a format string — it is `%%v` on a value "+
			"that happens to carry one. `providergateway` scrubs credentials out of the errors it "+
			"returns; nothing here may put one back.", strings.Join(offenders, "\n  "))
	}
	t.Logf("scanned %d formatting calls", scanned)
}

// stripGoStringLiterals removes double-quoted and back-quoted spans, so a scan over a formatting call
// reads its ARGUMENTS rather than its message.
func stripGoStringLiterals(code string) string {
	var out strings.Builder
	inDouble, inBack, escaped := false, false, false
	for _, r := range code {
		switch {
		case escaped:
			escaped = false
		case inDouble && r == '\\':
			escaped = true
		case inDouble:
			if r == '"' {
				inDouble = false
			}
		case inBack:
			if r == '`' {
				inBack = false
			}
		case r == '"':
			inDouble = true
		case r == '`':
			inBack = true
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// stripSQLComments removes `--` line comments, so the column scan reads DDL rather than the prose that
// explains it. Without it, a comment mentioning `api_key` would be reported as a column.
func stripSQLComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
