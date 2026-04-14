package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	rePythonFence      = regexp.MustCompile("(?m)```python\\b")
	rePyFence          = regexp.MustCompile("(?m)```py\\b")
	rePythonMPip       = regexp.MustCompile(`\bpython3?\s+-m\s+pip\s+install\b`)
	rePipInstall       = regexp.MustCompile(`\bpip3?\s+install\b`)
	rePythonMPytest    = regexp.MustCompile(`\bpython3?\s+-m\s+pytest\b`)
	rePytestQ          = regexp.MustCompile(`\bpytest\s+-q\b`)
	rePytest           = regexp.MustCompile(`\bpytest\b`)
	rePythonRun        = regexp.MustCompile(`\bpython3?\b`)
	reVenv             = regexp.MustCompile(`\bvenv\b`)
	rePyExt            = regexp.MustCompile(`\.py\b`)
	reInlinePythonCode = regexp.MustCompile("`python`")
	reInlinePipCode    = regexp.MustCompile("`pip`")
	rePipUninstall     = regexp.MustCompile(`\bpip\s+uninstall\b`)
	rePipShow          = regexp.MustCompile(`\bpip\s+show\b`)
	rePipFreeze        = regexp.MustCompile(`\bpip\s+freeze\b`)
	rePipWord          = regexp.MustCompile(`\bpip\b`)
	rePip3Word         = regexp.MustCompile(`\bpip3\b`)
	reGoRunPip         = regexp.MustCompile(`\bgo run-pip\b`)
	reGoRunGoRunPip    = regexp.MustCompile(`\bgo run go run-pip\b`)
	reGoGetUpgradePip  = regexp.MustCompile(`\bgo get\s+--upgrade\s+pip\b`)
	reGoGetPipUpgrade  = regexp.MustCompile(`\bgo get\s+pip\s+--upgrade\b`)
)

func rewrite(s string) string {
	out := s

	// Code fences
	out = rePythonFence.ReplaceAllString(out, "```go")
	out = rePyFence.ReplaceAllString(out, "```go")

	// Command-level replacements
	out = rePythonMPip.ReplaceAllString(out, "go get")
	out = rePipInstall.ReplaceAllString(out, "go get")
	out = rePythonMPytest.ReplaceAllString(out, "go test ./...")
	out = rePytestQ.ReplaceAllString(out, "go test ./...")
	out = rePytest.ReplaceAllString(out, "go test")
	out = rePythonRun.ReplaceAllString(out, "go run")
	out = rePipUninstall.ReplaceAllString(out, "go clean -modcache # replaced pip uninstall")
	out = rePipShow.ReplaceAllString(out, "go list -m")
	out = rePipFreeze.ReplaceAllString(out, "go list -m all")
	out = rePip3Word.ReplaceAllString(out, "go")
	out = rePipWord.ReplaceAllString(out, "go get")
	out = reGoRunGoRunPip.ReplaceAllString(out, "golang-go")
	out = reGoRunPip.ReplaceAllString(out, "golang-go")
	out = reGoGetUpgradePip.ReplaceAllString(out, "go get -u ./...")
	out = reGoGetPipUpgrade.ReplaceAllString(out, "go get -u ./...")

	// Common terminology in docs
	out = reInlinePythonCode.ReplaceAllString(out, "`go`")
	out = reInlinePipCode.ReplaceAllString(out, "`go get`")
	out = reVenv.ReplaceAllString(out, "go modules")
	out = rePyExt.ReplaceAllString(out, ".go")

	// Prefer Go naming in narrative text for this migration.
	out = strings.ReplaceAll(out, "Python", "Go")
	out = strings.ReplaceAll(out, "python", "go")
	out = strings.ReplaceAll(out, "PIP", "GO GET")
	out = strings.ReplaceAll(out, "Pip", "Go Get")
	out = strings.ReplaceAll(out, "pip-install", "go-get-install")

	return out
}

func isTarget(path string) bool {
	p := filepath.ToSlash(path)
	if !strings.Contains(p, "/custom/") {
		return false
	}
	return strings.HasSuffix(p, ".md") && strings.Contains(p, "/references/")
}

func main() {
	root := filepath.FromSlash("internal/promptlayer/embedded_defaults/skills/_global/custom")
	changed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isTarget(path) {
			return nil
		}
		orig, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		next := rewrite(string(orig))
		if next == string(orig) {
			return nil
		}
		// Normalize to LF to keep markdown diffs cleaner.
		next = strings.ReplaceAll(next, "\r\n", "\n")
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			return err
		}
		changed++
		fmt.Println(path)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("updated_files=%d\n", changed)
}
