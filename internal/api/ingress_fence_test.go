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

// runlinkStringConsts reads every `const Name = "…"` in `internal/runlink`.
//
// 🔴 This exists because the scan below was blind to MOST of what the transport addresses, and the
// blindness was invisible. Every path that matters is written `c.base + runlink.SomePath + …` — an
// IDENTIFIER, not a string literal — so `bin.Y.(*ast.BasicLit)` never matched it. The scan found four
// paths (`whoami`, `device/authorize`, `device/token`, `auth/password/signin`), all four of them the ones
// written inline, and it found none of `run-links`, `/api/v1/workflows/` or `/api/v1/proposals/`.
//
// The consequence is worth stating plainly, because it is bigger than the exemption this change deletes:
// the `strings.HasSuffix(path, "/") → continue` line was not what excluded the workflow and proposal
// paths from the fence. They were never in the set to begin with. Deleting the exemption alone would have
// changed nothing and would have LOOKED like the defect was closed.
func runlinkStringConsts(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", "runlink")
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	out := map[string]string{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, decl := range f.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						if v, err := strconv.Unquote(lit.Value); err == nil {
							out[name.Name] = v
						}
					}
				}
			}
		}
	}
	if len(out) < 3 {
		t.Fatalf("the runlink constant scan found %d string const(s) — it is not reading %s", len(out), dir)
	}
	return out
}

// transportPaths reads every platform path `internal/runlink/transport` addresses.
//
// It matches `c.base + X` — the one construction every call in that package uses to build a URL — where X
// is either a string literal or a `runlink.Name` constant this package can resolve. A call written any
// other way would be missed, so the count is asserted below: a scan that silently found nothing would
// make every assertion here pass vacuously.
func transportPaths(t *testing.T) []string {
	t.Helper()
	consts := runlinkStringConsts(t)
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
				// `c.base + "/api/v1/…"` or `c.base + runlink.SomePath`, possibly with further
				// concatenation for path segments; only the term immediately after the base is a fixed path.
				sel, ok := bin.X.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "base" {
					return true
				}
				value, ok := resolvePathTerm(bin.Y, consts)
				if !ok || !strings.HasPrefix(value, "/") {
					return true
				}
				seen[value] = true
				return true
			})
		}
	}
	// 6, not 3: the constant resolution above brings `run-links` and the two parameterised heads into the
	// set. A regression that re-broke the resolution would drop straight back to four and every assertion
	// here would pass again for the wrong reason — which is precisely how this fence spent its life.
	if len(seen) < 6 {
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

// resolvePathTerm reads one term of a URL concatenation as a fixed path, if it is one.
func resolvePathTerm(e ast.Expr, consts map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(x.Value)
		return v, err == nil
	case *ast.SelectorExpr:
		// `runlink.LinkPath` — a package-qualified constant this package can read.
		pkg, ok := x.X.(*ast.Ident)
		if !ok || pkg.Name != "runlink" {
			return "", false
		}
		v, ok := consts[x.Sel.Name]
		return v, ok
	}
	return "", false
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
		// 🔴 THE EXEMPTION IS GONE. It used to read:
		//
		//     if strings.HasSuffix(path, "/") { continue }
		//
		// …justified as "a prefix entry is addressed with further concatenation, so the fixed head is
		// reported rather than asserted". Nothing reported it. A path with a variable segment below it is
		// a path an `Exact` ingress rule cannot match, and the remedy is to give the route a FLAT shape —
		// not to widen the fence and not to publish a `Prefix` rule, which would publish every sibling
		// route beneath it (see TestAPrefixRuleWouldPublishItsSiblings).
		if strings.HasSuffix(path, "/") {
			t.Errorf("the CLI addresses %s, which has a caller-supplied segment below it.\n"+
				"  An Exact ingress rule cannot match it, and a Prefix rule would publish every route "+
				"beneath it. Publish it Exact, which means giving it a FLAT shape: move the identifier "+
				"into the request payload and address a path with no variable segment.", path)
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

// 🔴 THE PREFIX-CONSEQUENCE FENCE (P29 §1.6).
//
// The previous fence said "a public platform path is Exact, never Prefix" and gave one illustrative
// example in a comment. That is a rule a reviewer has to believe. This one COMPUTES the consequence: for
// any agentd-backed rule that is not Exact, it names every route in this package that rule publishes.
//
// The distinction matters because the argument against a prefix rule is quantitative. "Prefix is riskier"
// invites a trade; "this rule publishes these nine console-only routes, by name, and every
// `/api/v1/workflows/*` route anybody adds after today" does not. The nine were the actual alternative
// under review for P29, and the list is what made it a one-line decision instead of a discussion.
func TestAPrefixRuleWouldPublishItsSiblings(t *testing.T) {
	registered := registeredRoutes(t)
	for path, pathType := range ingressPaths(t) {
		if pathType == "Exact" {
			continue
		}
		siblings := routesUnderPrefix(registered, path)
		t.Errorf("deploy/k8s/overlays/prod/ingress.yaml routes %s to agentd with pathType %q.\n"+
			"  That rule publishes %d registered route(s) to the internet:\n    %s\n"+
			"  …and every route added under that prefix from now on, by default, forever. If one of these "+
			"is the route you wanted, give it a FLAT name and publish that name Exact.",
			path, pathType, len(siblings), strings.Join(siblings, "\n    "))
	}
}

// routesUnderPrefix names every registered route a Prefix rule at `prefix` would publish.
//
// Braces and all: `/api/v1/workflows/{workflow_id}/commit` is what the reader needs to see, because the
// question a reviewer is answering is "is THAT surface allowed on the internet", and a normalised form
// would hide which routes take a caller-supplied identifier.
func routesUnderPrefix(registered map[string]bool, prefix string) []string {
	var out []string
	for route := range registered {
		if strings.HasPrefix(route, prefix) {
			out = append(out, route)
		}
	}
	sort.Strings(out)
	return out
}

// 🔴 EXPAND-CONTRACT, asserted (P29 §1.7).
//
// The four parameterised routes the CLI used to address stay REGISTERED — so a CLI built before this
// change still works when it runs inside the cluster — and are never PUBLISHED. Both halves need a fence,
// and they fail in opposite directions:
//
//   - unregistered, and an older CLI inside the cluster breaks on the release that was supposed to be
//     compatible with it. Nothing else in the tree would notice; the flat routes serve everything the
//     current CLI does;
//   - published, and the identifier-in-the-path shape is back on the internet, which is the entire defect
//     this wave closed. Publishing one is what verified this test red.
//
// When the CLI floor moves and these are deleted, this test is deleted with them — it is the contract's
// expiry note, not a permanent rule.
func TestTheParameterisedRoutesAreRegisteredAndNeverPublished(t *testing.T) {
	parameterised := []string{
		"/api/v1/workflows/{workflow_id}/ir",
		"/api/v1/workflows/{workflow_id}/source/{source_revision}",
		"/api/v1/workflows/{workflow_id}/source/{source_revision}/discover",
		"/api/v1/proposals/{proposal_id}/verdict",
	}
	registered := registeredRoutes(t)
	published := ingressPaths(t)
	declared := map[string]bool{}
	for _, r := range PublicRoutes() {
		declared[r] = true
	}
	for _, route := range parameterised {
		if !registered[route] {
			t.Errorf("%s is no longer registered. It is the pre-P29 shape a deployed CLI still addresses "+
				"from inside the cluster; removing it is a CONTRACT change that waits for the CLI floor to "+
				"move, not a cleanup.", route)
		}
		if declared[route] {
			t.Errorf("%s is classified ExposurePublic. It carries a caller-supplied path segment, so no "+
				"Exact rule can match it and publishing it needs a Prefix rule — which is the defect P29 "+
				"closed. The flat replacement is what gets published.", route)
		}
		if _, ok := published[route]; ok {
			t.Errorf("deploy/k8s/overlays/prod/ingress.yaml routes %s to agentd. It must never be "+
				"published: the flat replacement carries the same traffic and can be matched Exact.", route)
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

// 🔴 SUBSTRATE PARITY (P29 §1.9). The product ships on two substrates and only one of them was checked.
//
// `deploy/k8s/overlays/prod/ingress.yaml` had six agentd rules and this file's fence watching them.
// `deploy/scripts/bootstrap-vm.sh` — the single-VM Compose install, which is what a self-hosting customer
// actually runs — published ONE: `/billing/webhook`. `heros login` reached Next.js and got a 404 on every
// box our own bootstrap script produced, and so did `heros link`, the device pair and password sign-in.
//
// The two substrates are read from ONE list here, deliberately. A test per substrate is a test somebody
// adds for the substrate they are working on; a loop over both is a test that fails twice when a public
// route is added and once when a substrate is forgotten.
func TestBothSubstratesPublishExactlyTheDeclaredPublicRoutes(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range PublicRoutes() {
		declared[r] = true
	}
	substrates := map[string]map[string]bool{
		"deploy/k8s/overlays/prod/ingress.yaml": ingressPathSet(t),
		"deploy/scripts/bootstrap-vm.sh":        setOf(composePlatformPaths(t)),
	}
	for name, published := range substrates {
		for route := range declared {
			if !published[route] {
				t.Errorf("%s does not publish %s, which publicroutes.go declares public.\n"+
					"  On that substrate the command that calls it answers 404 — from the console app, "+
					"not from the platform — and neither the build nor the deployment reports anything.",
					name, route)
			}
		}
		for route := range published {
			if !declared[route] {
				t.Errorf("%s publishes %s, which publicroutes.go does not declare public.", name, route)
			}
		}
	}
}

// composePlatformPaths reads the exact path list the generated Caddyfile publishes to agentd.
//
// Read from the SCRIPT rather than from a generated Caddyfile, because the Caddyfile is written at
// install time on the customer's box from the operator's domain and is not in the repository. The
// assignment is the artefact that exists here, so the assignment is what is checked.
func composePlatformPaths(t *testing.T) []string {
	t.Helper()
	script := filepath.Join("..", "..", "deploy", "scripts", "bootstrap-vm.sh")
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading %s: %v", script, err)
	}
	m := regexp.MustCompile(`(?m)^PLATFORM_PUBLIC_PATHS="([^"]*)"`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s has no PLATFORM_PUBLIC_PATHS assignment — either the Compose substrate stopped "+
			"declaring what it publishes, or this reader stopped finding it. Both are the same failure: "+
			"nothing is checking that substrate any more.", script)
	}
	paths := strings.Fields(string(m[1]))
	if len(paths) == 0 {
		t.Fatalf("%s declares an empty PLATFORM_PUBLIC_PATHS", script)
	}
	// The generated Caddyfile must actually USE the variable. A list nothing reads is a list that agrees
	// with this test and with nothing else.
	if !strings.Contains(string(raw), "@platform path $PLATFORM_PUBLIC_PATHS") {
		t.Errorf("%s declares PLATFORM_PUBLIC_PATHS but the generated Caddyfile does not use it", script)
	}
	return paths
}

func ingressPathSet(t *testing.T) map[string]bool {
	out := map[string]bool{}
	for p := range ingressPaths(t) {
		out[p] = true
	}
	return out
}

func setOf(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, i := range items {
		out[i] = true
	}
	return out
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
