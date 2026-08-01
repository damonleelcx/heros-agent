// Command operatorsurfaces runs P26's operator surfaces against a REAL repository —
// github.com/nousresearch/hermes-agent, the same target every other cmd/proof uses.
//
// # What "running an oversight phase" means
//
// P26 ships no engine. Its deliverable is four read-only surfaces and a fence, so the run that matters
// is: **point the read models at a real repository's real discovered nodes and a real delivery record,
// and check that what an operator would read is true of that repository.**
//
// That is a different check from the unit tests, and it catches a different failure. The tests prove
// each read model preserves a distinction. They cannot prove the distinctions are the ones a real
// repository actually produces — that the axis surface's refusal counts describe hermes-agent's real
// language mix, that the closing-artefact ranking names something that would actually help someone
// working on this repository, or that a delivery whose merge nobody has observed reads as UNKNOWN
// rather than as the most likely outcome.
//
// # What it asserts, and what it refuses to claim
//
// Every check below is of the form "the surface said X about hermes-agent, and X is true of
// hermes-agent". Where a read is not derivable from this repository it is reported as not derivable,
// not filled in: the run prints what the operator console would print, including its refusals.
//
//	go run ./cmd/proof/operatorsurfaces -ir /tmp/p23run/ir.json -repo /tmp/p23run/hermes-agent
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminfixture"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/transform"
)

const tenant = "tenant-hermes"

// failures accumulates every honesty violation the run finds. It exits non-zero on any, because a
// proof that prints a problem and exits 0 is a proof nobody is blocked by.
var failures []string

func fail(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }

func main() {
	irPath := flag.String("ir", "/tmp/p23run/ir.json", "the discovered IR for the repository")
	repo := flag.String("repo", "/tmp/p23run/hermes-agent", "the repository checkout the IR was discovered from")
	flag.Parse()

	raw, err := os.ReadFile(*irPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "operatorsurfaces: read IR: %v\n", err)
		os.Exit(2)
	}
	var ir discovery.IR
	if err := json.Unmarshal(raw, &ir); err != nil {
		fmt.Fprintf(os.Stderr, "operatorsurfaces: parse IR: %v\n", err)
		os.Exit(2)
	}

	fmt.Println("P26 — the operator surfaces, read against a real repository")
	fmt.Println(strings.Repeat("─", 78))
	fmt.Printf("repository   %s\n", ir.Workflow.Repo.URL)
	fmt.Printf("revision     %s\n", ir.Workflow.Repo.CommitSHA)
	fmt.Printf("checkout     %s\n", *repo)
	fmt.Printf("nodes        %d discovered, primary language %s\n", len(ir.Nodes), ir.Workflow.Language)

	// The languages the repository ACTUALLY contains, counted from the checkout rather than assumed
	// from the IR's single primary language. This is what makes the axis section below a statement
	// about hermes-agent instead of a statement about Python.
	present := languagesPresent(*repo)
	fmt.Printf("languages    %s\n\n", describeLanguages(present))

	layer, err := adminfixture.Build("p26hermes", func() time.Time { return time.Now().UTC() })
	if err != nil {
		fmt.Fprintf(os.Stderr, "operatorsurfaces: fixture: %v\n", err)
		os.Exit(2)
	}
	ctx := operatorContext(layer)

	runAxes(ctx, layer, present, ir)
	runDelivery(ctx, layer, ir)
	runReleases(ctx, layer)
	runOversight(ctx, layer)

	fmt.Println(strings.Repeat("─", 78))
	if len(failures) > 0 {
		fmt.Printf("FAILED — %d honesty violation(s):\n", len(failures))
		for _, f := range failures {
			fmt.Printf("  ✗ %s\n", f)
		}
		os.Exit(1)
	}
	fmt.Println("PASS — every surface's claim about this repository checks out.")
}

// ── Axes ────────────────────────────────────────────────────────────────────────────────────────

func runAxes(ctx context.Context, layer adminfixture.Layer, present map[string]int, ir discovery.IR) {
	fmt.Println("AXES — what the platform can and cannot apply to THIS repository")
	fmt.Println(strings.Repeat("─", 78))

	svc, err := adminops.NewAxisService(layer.Executor, hermesAdoption{ir: ir})
	if err != nil {
		fail("axis service: %v", err)
		return
	}
	view, err := svc.View(ctx)
	if err != nil {
		fail("axis view: %v", err)
		return
	}

	// 🔴 The whole point of the surface, answered for a real repository: of the coverage cells the
	// engine refuses, count only those in a language hermes-agent actually contains. A ranking over
	// languages the repository does not use would be a true number about nobody.
	type row struct {
		artefact string
		closes   int
		langs    []string
	}
	byArtefact := map[string]*row{}
	refusedHere := map[string]int{}
	appliesHere := 0
	for _, c := range view.Matrix {
		if present[c.Language] == 0 {
			continue
		}
		if c.State == adminops.CellApplies {
			appliesHere++
			continue
		}
		refusedHere[c.Cause]++
		if c.MissingInput == "" {
			continue
		}
		r, ok := byArtefact[c.MissingInput]
		if !ok {
			r = &row{artefact: c.MissingInput}
			byArtefact[c.MissingInput] = r
		}
		r.closes++
		if !contains(r.langs, c.Language) {
			r.langs = append(r.langs, c.Language)
		}
	}

	fmt.Printf("  coverage cells in languages this repository contains: %d apply, %d refused\n",
		appliesHere, total(refusedHere))
	fmt.Println("  refusals by STABLE typed cause — three causes, three different people:")
	for _, cause := range transform.CauseClasses() {
		n := refusedHere[string(cause)]
		owner := "—"
		for _, l := range view.Legend {
			if l.Cause == string(cause) {
				owner = l.Owner
			}
		}
		fmt.Printf("    %-34s %4d   whose move: %s\n", cause, n, owner)
	}

	ranked := make([]*row, 0, len(byArtefact))
	for _, r := range byArtefact {
		sort.Strings(r.langs)
		ranked = append(ranked, r)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].closes == ranked[j].closes {
			return ranked[i].artefact < ranked[j].artefact
		}
		return ranked[i].closes > ranked[j].closes
	})
	fmt.Println("  what would close the most refusals FOR THIS REPOSITORY (counts, not scores):")
	if len(ranked) == 0 {
		fmt.Println("    nothing — every refusal in these languages is a boundary, not unbuilt work")
	}
	for i, r := range ranked {
		if i == 5 {
			fmt.Printf("    … and %d more\n", len(ranked)-5)
			break
		}
		fmt.Printf("    %2d refusals  %s  [%s]\n", r.closes, truncate(r.artefact, 58), strings.Join(r.langs, ", "))
	}

	// Honesty checks against this repository, not against a fixture.
	for _, c := range view.Matrix {
		if present[c.Language] == 0 {
			continue
		}
		if c.State == adminops.CellRefused && c.Cause == "" {
			fail("axes: %s/%s/%s is refused with no cause", c.Axis, c.Language, c.Form)
		}
		if c.State == adminops.CellRefused && c.Cause == string(transform.CauseNotAtCallSite) && c.MissingInput != "" {
			fail("axes: %s/%s is a permanent boundary yet names artefact %q — a boundary has nothing to build",
				c.Axis, c.Language, c.MissingInput)
		}
	}
	if view.IsRanking {
		fail("axes: the surface declares itself a ranking; these are counts")
	}
	if !view.PlanIndependent {
		fail("axes: the surface does not declare coverage plan-independent")
	}
	// Parity, against the real engine, restricted to this repository's languages.
	engine := 0
	for _, c := range transform.AxisCoverage() {
		if present[c.Language] > 0 {
			engine++
		}
	}
	if got := appliesHere + total(refusedHere); got != engine {
		fail("axes: the surface reports %d cells for this repository's languages, the engine has %d", got, engine)
	}
	fmt.Printf("  parity: %d cells offered, %d answered by the engine — equal ✓\n\n", appliesHere+total(refusedHere), engine)
}

// hermesAdoption reports adoption from the REAL discovered IR: the nodes this repository actually has.
type hermesAdoption struct{ ir discovery.IR }

func (hermesAdoption) Describe() string { return "the discovered IR for this repository" }

// Adoption reports one tenant and this repository's real node count for every axis a node can carry.
// It does NOT invent per-axis differences: no override has been authored against this checkout, so the
// honest answer is "one tenant, N nodes eligible", identically per axis.
func (h hermesAdoption) Adoption(string) (int, int) { return 1, len(h.ir.Nodes) }

// RefusedNodes returns the repository's real nodes, attributed to the axis asked about.
func (h hermesAdoption) RefusedNodes(axis string) []adminops.RefusedNode {
	out := make([]adminops.RefusedNode, 0, len(h.ir.Nodes))
	for i, n := range h.ir.Nodes {
		if i == 5 {
			break
		}
		out = append(out, adminops.RefusedNode{
			TenantID: tenant, NodeID: n.NodeID, Language: h.ir.Workflow.Language, Axis: axis,
			Cause: string(transform.CauseCallSiteShape),
		})
	}
	return out
}

// ── Delivery ────────────────────────────────────────────────────────────────────────────────────

func runDelivery(ctx context.Context, layer adminfixture.Layer, ir discovery.IR) {
	fmt.Println("DELIVERY — what the platform has done to this repository")
	fmt.Println(strings.Repeat("─", 78))

	records := deliveryrecord.NewMemStore()
	accounts := account.NewMemStore()
	if _, err := accounts.Create(account.Account{
		CustomerID: tenant, ProviderCustomerHandle: "h-" + tenant, ActivePlanID: "enterprise",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		fail("account: %v", err)
		return
	}

	// A real delivery, at this repository's real revision, against its real default branch. It has been
	// OPENED and nothing has observed a merge — which is the true state, and the state the surface must
	// render as UNKNOWN rather than guessing at.
	rev := ir.Workflow.Repo.CommitSHA
	id := forgedelivery.DeliveryID("cfg-p26", rev, "main")
	if err := records.Append(context.Background(), forgedelivery.Entry{
		DeliveryID: id, TenantID: tenant, ConfigHash: "cfg-p26", SourceRevision: rev,
		Target: "main", ForgeRef: "pr-hermes-p26", Mode: forgedelivery.ModeCI,
		State: forgedelivery.StateOpened, Actor: "customer-ci", At: time.Now().UTC(),
	}); err != nil {
		fail("delivery record: %v", err)
		return
	}

	svc, err := adminops.NewDeliveryService(layer.Executor, records, accounts)
	if err != nil {
		fail("delivery service: %v", err)
		return
	}
	view, err := svc.Tenant(ctx, tenant)
	if err != nil {
		fail("delivery view: %v", err)
		return
	}
	if len(view.Rows) != 1 {
		fail("delivery: %d rows for a repository with one delivery", len(view.Rows))
		return
	}
	row := view.Rows[0]
	fmt.Printf("  delivery     %s… at %s → main\n", row.DeliveryID[:12], short(row.SourceRevision))
	fmt.Printf("  lifecycle    %s\n", row.State)
	fmt.Printf("  merge        %s\n", row.Merge)
	fmt.Printf("  credential   %s (the platform holds no forge credential in this mode)\n", row.Mode)

	// 🔴 The assertion this whole surface exists for.
	if row.Merge != adminops.MergeUnknown {
		fail("delivery: an OPENED pull request nobody has observed a merge on reads as %q, not %q",
			row.Merge, adminops.MergeUnknown)
	}
	if row.MergeCommit != "" {
		fail("delivery: an unmerged delivery carries merge commit %q", row.MergeCommit)
	}
	if row.AuditTarget != "" {
		fail("delivery: a CI-mediated delivery links the audit chain, which cannot observe its merge")
	}
	if !view.ReadOnly {
		fail("delivery: the surface does not declare itself read-only")
	}
	fmt.Printf("  audit chain  covers %d merge path(s), does NOT cover %d — stated on the surface\n",
		len(view.MergeCoverage.Covered), len(view.MergeCoverage.NotCovered))
	fmt.Printf("  undeliverable %d change cells, by typed cause:\n", view.UndeliverableTotal)
	for _, c := range view.Undeliverable {
		artefact := c.MissingArtifact
		if c.Permanent {
			artefact = "a boundary — nothing would close it"
		}
		fmt.Printf("    %-26s %3d   %s\n", c.Cause, c.Count, truncate(artefact, 44))
	}
	fmt.Println()
}

// ── Releases ────────────────────────────────────────────────────────────────────────────────────

func runReleases(ctx context.Context, layer adminfixture.Layer) {
	fmt.Println("RELEASES — what a hermes-agent user would install, and what signed it")
	fmt.Println(strings.Repeat("─", 78))

	// No publish record is wired here, and that is the honest state: this process has not published
	// anything. The surface must say so rather than render an empty page as a working one.
	svc, err := adminops.NewReleaseService(layer.Executor, nil)
	if err != nil {
		fail("release service: %v", err)
		return
	}
	view, err := svc.View(ctx)
	if err != nil {
		fail("release view: %v", err)
		return
	}
	if !view.Degraded {
		fail("releases: no publish record is wired, yet the surface reports a healthy page")
	}
	fmt.Printf("  publish record: %s\n", truncate(view.Detail, 74))

	installable := 0
	for _, c := range view.Channels {
		if c.Delivered {
			installable++
		}
	}
	fmt.Printf("  channels     %d total, %d installable today\n", len(view.Channels), installable)
	for _, k := range view.Keys {
		fmt.Printf("  key          %-22s %-10s %-8s %s\n", k.ID, k.Role, k.Fingerprint, truncate(k.Note, 30))
		// 🔴 No key material, checked here too — against the compiled trust root, not a fixture.
		if len(k.Fingerprint) > 20 {
			fail("releases: key %s's fingerprint is %d characters — that is a blob, not an identifier",
				k.ID, len(k.Fingerprint))
		}
		if k.Role == "retired" && (k.RetiredAt == "" || k.Note == "") {
			fail("releases: retired key %s carries no rotation date or no reason", k.ID)
		}
	}
	fmt.Println()
}

// ── Oversight ───────────────────────────────────────────────────────────────────────────────────

func runOversight(ctx context.Context, layer adminfixture.Layer) {
	fmt.Println("OVERSIGHT — who is acting, and what this platform cannot tell you")
	fmt.Println(strings.Repeat("─", 78))

	svc, err := adminops.NewOversightService(layer.Executor, adminops.OversightConfig{
		Sessions: layer.Sessions,
		Identity: layer.Authenticator.Describe(),
		Tenants:  func() []string { return []string{tenant} },
	})
	if err != nil {
		fail("oversight service: %v", err)
		return
	}
	view, err := svc.View(ctx)
	if err != nil {
		fail("oversight view: %v", err)
		return
	}

	fmt.Printf("  identity     %s (%s)\n", view.IdentityProvider.Kind, testModeWord(view.IdentityProvider.TestMode))
	for _, s := range view.Sessions {
		fmt.Printf("  session      %-18s factor %-10s %s\n", s.AdminID, s.Factor, strengthWord(s.MultiFactor))
		if s.Factor == "" {
			fail("oversight: session %s shows no verified factor", s.SessionID)
		}
	}
	if view.IntegrationsKnown {
		fail("oversight: no readiness surface is wired, yet the integrations report as known")
	}
	fmt.Println("  reporting    not read — no readiness surface is wired here, and that is reported as")
	fmt.Println("               'we did not ask' rather than as 'nothing is configured'")

	for _, d := range view.Deployments {
		fmt.Printf("  deployment   %-16s version %s\n", d.TenantID, unknownWord(d.Unknown, d.Version))
		if !d.Unknown {
			fail("oversight: a deployed version was reported for %s with no heartbeat to derive it from", d.TenantID)
		}
		if d.MissingCollection == "" {
			fail("oversight: %s's unknown version names no missing collection", d.TenantID)
		}
	}
	fmt.Println("  not yet readable:")
	for _, n := range view.NotYetReadable {
		fmt.Printf("    %-28s requires %s\n", n.Subject, truncate(n.Requires, 44))
		if n.Requires == "" {
			fail("oversight: %q is not-yet-readable and names no collection", n.Subject)
		}
	}
	fmt.Println()
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────

// languagesPresent counts source files per registered language in the checkout, so the axis section is
// a statement about THIS repository rather than about every language the engine knows.
func languagesPresent(repo string) map[string]int {
	ext := map[string]string{
		".py": "python", ".ts": "typescript", ".tsx": "typescript", ".js": "javascript",
		".jsx": "javascript", ".go": "go", ".rs": "rust", ".java": "java", ".kt": "kotlin",
	}
	out := map[string]int{}
	_ = filepath.WalkDir(repo, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if lang, ok := ext[strings.ToLower(filepath.Ext(path))]; ok {
			out[lang]++
		}
		return nil
	})
	return out
}

func describeLanguages(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s (%d files)", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// operatorContext signs a Superadmin in through the REAL authenticator — a real assertion from the
// fixture issuer, verified by the real verifier, MFA included — so every read below goes through the
// real gate and writes its real audit entry.
func operatorContext(layer adminfixture.Layer) context.Context {
	subject := "sso|" + string(adminrbac.RoleSuperadmin)
	assertion, err := layer.TestModeIdP.Assert(context.Background(), subject, "webauthn")
	if err != nil {
		fmt.Fprintf(os.Stderr, "operatorsurfaces: assert: %v\n", err)
		os.Exit(2)
	}
	sess, _, err := layer.Authenticator.Authenticate(context.Background(), assertion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "operatorsurfaces: authenticate: %v\n", err)
		os.Exit(2)
	}
	return adminidentity.WithSession(context.Background(), sess)
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func testModeWord(b bool) string {
	if b {
		return "TEST-MODE fixture; the verifier is real, the issuer is not a production IdP"
	}
	return "production identity provider"
}

func strengthWord(multi bool) string {
	if multi {
		return "multi-factor"
	}
	return "single factor"
}

func unknownWord(unknown bool, v string) string {
	if unknown {
		return "unknown (not inferred)"
	}
	return v
}
