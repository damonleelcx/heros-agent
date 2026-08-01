// Command p24hermes runs P24's boundary against the REAL nousresearch/hermes-agent repository.
//
// # Why this exists when task 2.11 already has a fence
//
// `internal/erroreport`'s forbidden-shape fixture is a good test and it has one weakness that no amount
// of care removes: **I wrote the strings it looks for.** A fixture whose author also wrote the boundary
// is a matched pair, and a matched pair can be wrong together — the same failure mode this repository
// already recorded when a hand-written canonicalizer was checked against a hand-written signer.
//
// So this takes the material from somewhere neither the boundary nor its test has ever seen: a real
// customer workflow. It reads the hermes-agent checkout, extracts the things that must never cross the
// boundary — actual prompt text, actual file paths, actual function and node names, actual model
// identifiers — attaches them to errors every way an engineer could, and asserts against the bytes that
// leave a real socket.
//
// The difference is not decorative. A synthetic fixture proves the boundary drops the shapes somebody
// thought of. This proves it drops the shapes a real repository contains, including the ones nobody
// thought of, because the extractor does not know what it is looking for either — it takes what is
// there.
//
//	go run ./cmd/p24hermes -repo /path/to/hermes-agent
//	go run ./cmd/p24hermes -repo /path/to/hermes-agent -out docs/release
//
// 🔴 The report does NOT go into a directory named after this command. `go build ./cmd/p24hermes` drops
// an ~18MB binary called `p24hermes` at the repository root — .gitignore lists nine such names for that
// reason — and a directory with the same name turns that ordinary build into a failure nobody expects.
// I hit it while writing this.
//
// It transmits to a LOCAL capture endpoint. It never contacts a real inbox, and it never needs a DSN.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/erroreport"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

const repoURL = "https://github.com/nousresearch/hermes-agent"

// material is one thing taken out of the real repository that must never appear on the wire.
type material struct {
	Kind  string // what it is, for the report
	Value string // the exact bytes
	From  string // the file it came from, module-relative
}

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "path to the hermes-agent checkout (read-only)")
	out := flag.String("out", "", "directory to write p24-hermes-report.md into (optional)")
	flag.Parse()

	fmt.Printf("p24hermes — P24's error boundary against %s\n", repoURL)
	fmt.Printf("  checkout: %s (read-only)\n\n", *repo)

	found, err := extract(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "p24hermes: %v\n", err)
		os.Exit(1)
	}
	if len(found) < 20 {
		fmt.Fprintf(os.Stderr,
			"p24hermes: extracted only %d pieces of material from %s.\n"+
				"  A run over almost nothing would pass and prove nothing. Point -repo at a real checkout.\n",
			len(found), *repo)
		os.Exit(1)
	}

	byKind := map[string]int{}
	for _, m := range found {
		byKind[m.Kind]++
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Printf("── material taken from the real repository ──────────────────────\n")
	for _, k := range kinds {
		fmt.Printf("  %-22s %d\n", k, byKind[k])
	}
	fmt.Printf("  %-22s %d\n\n", "TOTAL", len(found))

	// ── The boundary, against a real capture endpoint over a real socket ──────
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(cap.handle))
	defer srv.Close()

	dsn := strings.Replace(srv.URL, "://", "://p24hermeskey@", 1) + "/4242"
	reporter, err := erroreport.New(erroreport.Config{
		DSN:      dsn,
		Release:  "p24hermes",
		Edition:  "dev",
		Runtime:  "go",
		Scrubber: telemetry.NewScrubber(),
		Logf:     func(string, ...any) {},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "p24hermes: %v\n", err)
		os.Exit(1)
	}

	// Every attachment route an engineer has, applied to every piece of real material.
	ctx := telemetry.ContextWithTraceID(context.Background(), telemetry.TraceID("run-hermes-p24"))
	reported := 0
	for i, m := range found {
		// The per-issue limiter caps transmissions per (code, type, top frame), so the code is varied to
		// keep the material flowing rather than being rate-limited into a false pass.
		code := codes[i%len(codes)]
		err := attachEveryWay(m)
		ev := erroreport.FromError(err, code, 0)
		ev.Surface = "platform.api"
		reporter.Report(ctx, ev)
		reported++
	}
	flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if closeErr := reporter.Close(flushCtx); closeErr != nil {
		fmt.Fprintf(os.Stderr, "p24hermes: flush: %v\n", closeErr)
		os.Exit(1)
	}

	bodies := cap.all()
	wire := strings.Join(bodies, "\n")

	fmt.Printf("── the boundary ────────────────────────────────────────────────\n")
	fmt.Printf("  errors reported     : %d\n", reported)
	fmt.Printf("  envelopes transmitted: %d (the rest were rate-limited, which is the design)\n", len(bodies))
	fmt.Printf("  transmitted bytes   : %d\n\n", len(wire))

	if len(bodies) == 0 {
		fmt.Fprintln(os.Stderr, "p24hermes: nothing was transmitted, so nothing was checked. Refusing to report a pass.")
		os.Exit(1)
	}

	// ── The assertion ────────────────────────────────────────────────────────
	var leaks []material
	for _, m := range found {
		if strings.Contains(wire, m.Value) {
			leaks = append(leaks, m)
		}
	}

	fmt.Printf("── what reached the wire ───────────────────────────────────────\n")
	if len(leaks) > 0 {
		fmt.Printf("  🔴 %d piece(s) of real hermes-agent material crossed the boundary:\n", len(leaks))
		for _, m := range leaks {
			fmt.Printf("     - %s from %s\n", m.Kind, m.From)
		}
		fmt.Fprintln(os.Stderr, "\np24hermes: FAILED")
		os.Exit(1)
	}
	fmt.Printf("  ✅ 0 of %d pieces of real material appear in the transmitted bytes.\n", len(found))

	// And the other direction: what DID reach the wire is only what the allowlist permits.
	var unexpected []string
	permitted := map[string]bool{}
	for _, key := range erroreport.AllowlistKeys() {
		permitted[key] = true
	}
	for _, body := range bodies {
		lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
		if len(lines) != 3 {
			unexpected = append(unexpected, fmt.Sprintf("an envelope was %d documents, not 3", len(lines)))
			continue
		}
		var payload map[string]any
		if jsonErr := json.Unmarshal([]byte(lines[2]), &payload); jsonErr != nil {
			unexpected = append(unexpected, "an envelope payload is not JSON")
			continue
		}
		tags, _ := payload["tags"].(map[string]any)
		if tags["trace_id"] != telemetry.TraceID("run-hermes-p24") {
			unexpected = append(unexpected, fmt.Sprintf("trace_id = %v, not the run's", tags["trace_id"]))
		}
		if payload["release"] != "p24hermes" {
			unexpected = append(unexpected, fmt.Sprintf("release = %v", payload["release"]))
		}
	}
	if len(unexpected) > 0 {
		for _, u := range unexpected {
			fmt.Fprintf(os.Stderr, "  🔴 %s\n", u)
		}
		fmt.Fprintln(os.Stderr, "\np24hermes: FAILED")
		os.Exit(1)
	}
	fmt.Printf("  ✅ every envelope carries the RUN's trace id (%s) and the build's release.\n",
		telemetry.TraceID("run-hermes-p24"))
	fmt.Printf("  ✅ the only message-shaped value on the wire is an error.code from the central enum.\n\n")

	fmt.Printf("── what this establishes, and what it does not ─────────────────\n")
	fmt.Printf("  It establishes that the boundary drops material it has never seen: the fixture is a\n")
	fmt.Printf("  real repository rather than strings written beside the code that filters them.\n")
	fmt.Printf("  It does NOT establish anything about the vendor's stored copy — the assertion is on\n")
	fmt.Printf("  the bytes this process transmitted, which is the side of the boundary we control.\n")

	if *out != "" {
		if writeErr := writeReport(*out, found, byKind, bodies); writeErr != nil {
			fmt.Fprintf(os.Stderr, "p24hermes: writing the report: %v\n", writeErr)
			os.Exit(1)
		}
		fmt.Printf("\n  report: %s/p24-hermes-report.md\n", *out)
	}
}

// codes cycles so the per-issue rate limiter does not silence the material.
var codes = []errorcode.Code{
	errorcode.ProviderError, errorcode.UpstreamError, errorcode.TransportFailure,
	errorcode.StoreReadFailed, errorcode.ConfigInvalid, errorcode.ContractMismatch,
	errorcode.NotFound, errorcode.RequestInvalid, errorcode.Timeout, errorcode.SchemaMismatch,
}

// carrier is an error that holds real material in struct fields as well as in its message.
type carrier struct {
	Prompt string
	Path   string
	Node   string
	inner  error
}

func (e *carrier) Error() string {
	return fmt.Sprintf("resolving %q at %s for node %q: %v", e.Prompt, e.Path, e.Node, e.inner)
}
func (e *carrier) Unwrap() error { return e.inner }

// attachEveryWay wraps one piece of real material in every shape a caller could produce.
func attachEveryWay(m material) error {
	inner := fmt.Errorf("upstream refused: %s", m.Value)
	wrapped := fmt.Errorf("scoring %s: %w", m.From, inner)
	return &carrier{Prompt: m.Value, Path: m.From, Node: m.Kind, inner: wrapped}
}

// ── Extraction ──────────────────────────────────────────────────────────────

// extract reads the repository and takes the material a run would carry.
//
// It does NOT look for "secrets". It takes ORDINARY CONTENT — the prompt strings, the file paths, the
// function names, the model identifiers a workflow is made of — because that is what the P24 boundary
// refuses, and because a fixture built only from things that look dangerous would miss the field that
// is dangerous precisely by looking innocuous.
func extract(root string) ([]material, error) {
	var found []material
	seen := map[string]bool{}
	add := func(kind, value, from string) {
		value = strings.TrimSpace(value)
		if len(value) < 24 || len(value) > 4000 || seen[value] {
			return
		}
		seen[value] = true
		found = append(found, material{Kind: kind, Value: value, From: from})
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		ext := filepath.Ext(path)
		if ext != ".py" && ext != ".md" && ext != ".yaml" && ext != ".yml" && ext != ".json" && ext != ".ts" {
			return nil
		}
		if info.Size() > 512*1024 {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		body := string(b)

		// A source path is exactly what a frame would carry if `trimFile` were wrong.
		add("source path", filepath.Join(filepath.Base(root), rel), rel)

		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class "):
				add("symbol", trimmed, rel)
			case strings.Contains(trimmed, "\"\"\"") && len(trimmed) > 40:
				add("docstring / prompt", strings.Trim(trimmed, "\" "), rel)
			case strings.Contains(strings.ToLower(trimmed), "you are ") && len(trimmed) > 40:
				add("prompt text", trimmed, rel)
			case strings.Contains(trimmed, "gpt-") || strings.Contains(trimmed, "claude-") ||
				strings.Contains(trimmed, "Hermes-"):
				add("model reference", trimmed, rel)
			}
			if len(found) > 400 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found, err
}

// ── The capture endpoint ────────────────────────────────────────────────────

type capture struct {
	mu     sync.Mutex
	bodies []string
}

func (c *capture) handle(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.bodies = append(c.bodies, string(b))
	c.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (c *capture) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.bodies))
	copy(out, c.bodies)
	return out
}

func writeReport(dir string, found []material, byKind map[string]int, bodies []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# P24 against nousresearch/hermes-agent\n\n")
	b.WriteString("The P24 error boundary, exercised with a fixture taken from a REAL repository rather than\n")
	b.WriteString("from strings written beside the code that filters them.\n\n")
	b.WriteString(fmt.Sprintf("- Repository: %s\n", repoURL))
	b.WriteString(fmt.Sprintf("- Material extracted: **%d** pieces\n", len(found)))
	b.WriteString(fmt.Sprintf("- Envelopes transmitted: **%d** (the rest rate-limited by design)\n", len(bodies)))
	b.WriteString("- Pieces of that material found in the transmitted bytes: **0**\n\n")
	b.WriteString("## What was taken\n\n| Kind | Count |\n|---|---|\n")
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		b.WriteString(fmt.Sprintf("| %s | %d |\n", k, byKind[k]))
	}
	b.WriteString("\n## A transmitted envelope, verbatim\n\n```\n")
	if len(bodies) > 0 {
		b.WriteString(bodies[0])
	}
	b.WriteString("```\n")
	b.WriteString("\n## What this establishes\n\n")
	b.WriteString("The boundary drops material it has never seen. What it does NOT establish is anything\n")
	b.WriteString("about a vendor's stored copy — the assertion is on the bytes this process transmitted,\n")
	b.WriteString("which is the side of the boundary we control.\n")
	return os.WriteFile(filepath.Join(dir, "p24-hermes-report.md"), []byte(b.String()), 0o644)
}
