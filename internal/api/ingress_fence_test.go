package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ingress_fence_test.go is the machine-enforced half of `publicroutes.go`.
//
// # What it catches, in one sentence
//
// A route the CLI calls from a customer's laptop that the checked-in ingress manifest does not carry —
// which is a 404 in production behind a green build, and has happened twice.
//
// # 🔴 The source of truth is the TRANSPORT, not a hand-maintained list
//
// The obvious fence is "classify every route in this package and check the public ones". It was written
// that way first and it is the wrong shape twice over: it demands a decision about seventy console-only
// routes that will never be published, and — worse — the classification is itself a hand-maintained list,
// which is the exact artefact that failed here. `runlink.PlatformPaths` had already drifted the same way:
// its own comment claims it is "every path this package is allowed to address", and P27 added two
// transports without adding two entries, because the test driving it enumerated a hand-written list of
// calls.
//
// So the question this file asks is derived rather than declared: **which paths does the transport
// actually address?** That set is read from the source of `internal/runlink/transport`, which is the only
// code in the product that speaks to the platform from OUTSIDE the cluster. Every path it addresses must
// be published, must be published Exact, and must be declared public in `publicroutes.go` — and a fourth
// transport added tomorrow is caught with no list to remember to update.
//
// The reverse direction is checked too, and it is not symmetry for its own sake: a path in the manifest
// that nothing declares public is a surface somebody published without meaning to, and NOTHING BREAKS when
// that happens. `/api/v1/device/approve` is the concrete case — it mints a credential naming a person, it
// is correctly unreachable from the internet, and the only thing keeping it that way is that nobody has
// added a line to a YAML file.

// transportPaths reads every platform path `internal/runlink/transport` addresses.
//
// It matches `c.base + "…"` — the one construction every call in that package uses to build a URL. A call
// written any other way would be missed, so the count is asserted below: a scan that silently found
// nothing would make every assertion here pass vacuously.
func transportPaths(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", "runlink", "transport")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	seen := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				bin, ok := n.(*ast.BinaryExpr)
				if !ok || bin.Op != token.ADD {
					return true
				}
				// `c.base + "/api/v1/…"`, possibly with further concatenation for path segments; only the
				// literal immediately after the base is a fixed path.
				sel, ok := bin.X.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "base" {
					return true
				}
				lit, ok := bin.Y.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil || !strings.HasPrefix(value, "/") {
					return true
				}
				seen[value] = true
				return true
			})
		}
	}
	if len(seen) < 3 {
		t.Fatalf("the transport scan found %d path(s) — it is not reading %s, so every assertion in this "+
			"file would pass for the wrong reason", len(seen), dir)
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ingressPaths reads the customer-hostname Ingress and returns the paths routed to `agentd`.
func ingressPaths(t *testing.T) map[string]string {
	t.Helper()
	manifest := filepath.Join("..", "..", "deploy", "k8s", "overlays", "prod", "ingress.yaml")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("reading %s: %v", manifest, err)
	}
	// A deliberately small reader rather than a YAML dependency: what is needed is the (path, pathType,
	// backend) triple, and every rule in that file is written on three adjacent lines. A parser would be
	// more general and would make this test depend on a library the production build does not use.
	pathRe := regexp.MustCompile(`^\s*- path:\s*(\S+)\s*$`)
	typeRe := regexp.MustCompile(`^\s*pathType:\s*(\S+)\s*$`)
	backendRe := regexp.MustCompile(`^\s*backend:.*name:\s*(\w+)`)

	lines := strings.Split(string(raw), "\n")
	out := map[string]string{}
	for i, line := range lines {
		m := pathRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		path, pathType, backend := m[1], "", ""
		for j := i + 1; j < len(lines) && j < i+4; j++ {
			if t2 := typeRe.FindStringSubmatch(lines[j]); t2 != nil {
				pathType = t2[1]
			}
			if b := backendRe.FindStringSubmatch(lines[j]); b != nil {
				backend = b[1]
			}
		}
		if backend == "agentd" {
			out[path] = pathType
		}
	}
	if len(out) == 0 {
		t.Fatalf("no agentd-backed path was found in %s — the reader is not reading it", manifest)
	}
	return out
}

// 🔴 The fence. Every path the CLI addresses must be reachable from the internet.
func TestEveryPathTheCLIAddressesIsPublished(t *testing.T) {
	published := ingressPaths(t)
	declared := map[string]bool{}
	for _, r := range PublicRoutes() {
		declared[r] = true
	}

	for _, path := range transportPaths(t) {
		// A prefix entry in the transport (a path with caller-supplied segments below it) is addressed
		// with further concatenation; the fixed head is what an Exact ingress rule would have to match, so
		// those are reported rather than asserted — see the trailing-slash convention in
		// `runlink.PlatformPaths`.
		if strings.HasSuffix(path, "/") {
			continue
		}
		if !declared[path] {
			t.Errorf("the CLI addresses %s and publicroutes.go does not declare it public.\n"+
				"  Classify it ExposurePublic, or the fence below cannot require an ingress entry for it.", path)
			continue
		}
		pathType, inManifest := published[path]
		if !inManifest {
			t.Errorf("the CLI addresses %s and deploy/k8s/overlays/prod/ingress.yaml does not route it.\n"+
				"  It answers 404 in production the moment that manifest is applied — a green build, a "+
				"healthy deployment, and a command nobody can run. Add:\n"+
				"          - path: %s\n            pathType: Exact\n"+
				"            backend: { service: { name: agentd, port: { number: 4321 } } }", path, path)
			continue
		}
		if pathType != "Exact" {
			// A Prefix rule publishes everything beneath it. `/api/v1/auth/password` as a prefix would
			// publish `reset` and `change` beside `signin`.
			t.Errorf("%s is published with pathType %q — a public platform path is Exact, never Prefix, "+
				"because a prefix rule publishes every route beneath it", path, pathType)
		}
	}
}

// The reverse: nothing is published that was not meant to be. Nothing BREAKS when this is wrong, which is
// exactly why it needs a test — the route simply becomes reachable, and stays that way.
func TestNothingIsPublishedThatIsNotDeclaredPublic(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range PublicRoutes() {
		declared[r] = true
	}
	for path := range ingressPaths(t) {
		if !declared[path] {
			t.Errorf("deploy/k8s/overlays/prod/ingress.yaml publishes %s, which publicroutes.go does not "+
				"declare public. Either it is an accidental exposure, or the classification is stale — both "+
				"need a person to decide, which is why this is not a warning.", path)
		}
	}
}

// A declared route that this package no longer registers is stale, and a stale entry is how a list stops
// describing the system it claims to describe.
func TestNoStaleRouteClassifications(t *testing.T) {
	registered := registeredRoutes(t)
	var stale []string
	for route := range routeExposure {
		if !registered[route] {
			stale = append(stale, route)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("publicroutes.go classifies routes this package no longer registers:\n  %s",
			strings.Join(stale, "\n  "))
	}
}

// 🔴 Every route in the two families P27 and P28 added is classified. Scoped to those prefixes rather than
// to the whole package on purpose: they are the routes reached WITHOUT the console's credential, so they
// are the ones where "should this be on the internet?" is a real question with a wrong answer. Extending
// this to every console-only route would demand seventy decisions that all have the same answer, and a
// list nobody reads is a list nobody maintains.
func TestEveryUnauthenticatedFamilyRouteIsClassified(t *testing.T) {
	families := []string{"/api/v1/device/", "/api/v1/auth/"}
	var undeclared []string
	for route := range registeredRoutes(t) {
		for _, family := range families {
			if !strings.HasPrefix(route, family) {
				continue
			}
			if _, ok := routeExposure[route]; !ok {
				undeclared = append(undeclared, route)
			}
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) > 0 {
		t.Fatalf("these credential-free routes are registered and not classified in publicroutes.go:\n  %s\n\n"+
			"Decide for each: ExposurePublic (a machine outside the cluster calls it — and then it MUST get "+
			"an Exact ingress entry) or ExposureInternal (only the console's server side calls it).",
			strings.Join(undeclared, "\n  "))
	}
}

// registeredRoutes extracts every `s.Mux.HandleFunc("METHOD /path", …)` literal in this package.
//
// Read from the SOURCE rather than from a live mux because `MountAccounts` needs a surface, `MountPayments`
// needs a source, and a dozen other mounts need their own dependencies — building a fully-mounted server
// here would mean constructing every capability in the product to ask it one question about strings, and a
// mount somebody forgot to call would silently shrink the answer.
func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing internal/api: %v", err)
	}
	routes := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "HandleFunc" {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				pattern, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				// "POST /api/v1/x" -> "/api/v1/x". Keyed by PATH: an ingress rule matches a path and knows
				// nothing about methods, so two methods on one path are one ingress decision.
				if i := strings.LastIndex(pattern, " "); i >= 0 {
					pattern = pattern[i+1:]
				}
				if strings.HasPrefix(pattern, "/") {
					routes[pattern] = true
				}
				return true
			})
		}
	}
	if len(routes) < 10 {
		t.Fatalf("the route scan found only %d route(s) — it is not reading this package", len(routes))
	}
	return routes
}
