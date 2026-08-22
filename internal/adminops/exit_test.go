package adminops_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// exit_test.go is P26 wave 26f — the reconciliation the phase exits on.
//
// Two of these are the phase's own claims turned into assertions rather than sentences: that no existing
// role widened, and that no new table was created. The third is the headline metric, which is written so
// it can report that the phase FAILED.

// preP26Capabilities is the capability set as it stood BEFORE this change, written out by hand.
//
// 🔴 Hand-written on purpose. Deriving it from the current code would make the assertion tautological —
// it would compare `Capabilities` to itself and pass on any widening. This list is the historical fact
// the assertion is against, and editing it to make a test pass is editing the evidence.
var preP26Capabilities = map[adminrbac.Capability][]adminrbac.Role{
	adminrbac.CapTenantRead:          {adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapJobRead:             {adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapImpersonateRead:     {adminrbac.RoleSupport, adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapBillingRead:         {adminrbac.RoleBillingOps, adminrbac.RoleSuperadmin},
	adminrbac.CapBillingCorrect:      {adminrbac.RoleBillingOps, adminrbac.RoleSuperadmin},
	adminrbac.CapEntitlementOverride: {adminrbac.RoleBillingOps, adminrbac.RoleSuperadmin},
	adminrbac.CapJobRetry:            {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapJobCancel:           {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapRegistryAdmin:       {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapKillSwitch:          {adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapTenantSuspend:       {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapTenantQuota:         {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapRoleGrant:           {adminrbac.RoleSuperadmin},
	adminrbac.CapGDPRExecute:         {adminrbac.RoleSuperadmin},
	adminrbac.CapCrossTenantRead:     {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapAuditRead:           {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
	adminrbac.CapImpersonateElevate:  {adminrbac.RoleBillingOps, adminrbac.RolePlatformSRE, adminrbac.RoleSuperadmin},
}

// TestNoExistingRoleWidened defends P26 task 8.2.
//
// The reading it enforces, stated because the two halves of the requirement look contradictory: for
// every capability that existed BEFORE this change, the set of roles holding it is unchanged. A NEW
// capability may be held where it was deliberately granted — a new capability held nowhere would be
// unreachable, which is the one thing D8 is not asking for. What is forbidden is a role picking up an
// existing power because a new page made it convenient.
func TestNoExistingRoleWidened(t *testing.T) {
	for capability, want := range preP26Capabilities {
		got := adminrbac.HoldersOf(capability)
		if len(got) != len(want) {
			t.Fatalf("capability %s is now held by %v; before P26 it was held by %v. No existing role may "+
				"gain a capability it did not hold (P26 §8.2, design D8).", capability, got, want)
		}
		held := map[adminrbac.Role]bool{}
		for _, r := range got {
			held[r] = true
		}
		for _, r := range want {
			if !held[r] {
				t.Fatalf("capability %s LOST holder %s — a role narrowing is also a change this phase did "+
					"not intend", capability, r)
			}
		}
	}
	// And the only capabilities that are NEW are ones a phase argued for.
	//
	// P26's three, plus P30's two. The list grows by a deliberate edit, which is the point: a
	// capability appearing here without a line in this map is a capability nobody argued for, and this
	// test is where that argument gets made.
	newOnes := map[adminrbac.Capability]bool{
		adminrbac.CapDeliveryRead: true, adminrbac.CapReleaseRead: true, adminrbac.CapAxisRead: true,
		// P30. `agent.read` goes to Platform-SRE, who runs the machinery and must be able to see what
		// the analysis agent costs; `agent.admin` goes to Superadmin ALONE, because publishing a
		// definition changes what the platform infers about every customer's source and setting a
		// placement to `platform` makes it read that source under a platform-held credential.
		adminrbac.CapAgentRead: true, adminrbac.CapAgentAdmin: true,
	}
	for _, c := range adminrbac.Capabilities {
		if _, existed := preP26Capabilities[c]; existed {
			continue
		}
		if !newOnes[c] {
			t.Fatalf("capability %s appeared without being one of P26's three declared additions", c)
		}
		if len(adminrbac.HoldersOf(c)) == 0 {
			t.Fatalf("new capability %s is granted to no role — deny-by-default made it unreachable", c)
		}
	}
}

// TestNoNewTableWasCreated defends P26 task 8.2's second half and design D7.
//
// Creating a table is a one-way door, and "build it now for future use" is refused. Where a read was not
// derivable this phase recorded `not-yet-readable` with the missing collection named — which is an
// honest gap with a cause, and directly actionable by the next phase.
//
// # Why this is an allowlist rather than a ceiling
//
// It used to assert that NO migration numbered above 19 existed, on the reasoning that 19 was the
// highest at P26's exit so anything above it had to be P26's. That proxy holds exactly until the next
// phase adds a migration for its own reasons — and then the fence fails for a change it was never aimed
// at, and the tempting fix is to raise the number, which retires the fence silently.
//
// So later migrations are allowed, one at a time, each named with WHOSE it is. A genuinely new P26 table
// still fails, because nobody would be able to add it here without writing down that P26 owns it.
func TestNoNewTableWasCreated(t *testing.T) {
	dir := filepath.Join("..", "..", "db", "migrations", "postgres")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	// The highest migration at P26's exit. Anything above it must be listed below with its owner.
	const highestAtP26Exit = 19
	// Migrations added AFTER P26, and the phase each belongs to. P26 owns none of them, which is the
	// claim this test exists to keep true.
	postP26 := map[string]string{
		"0042_p29_workflow_ir_coverage_version":   "P29 — one NULLABLE column on `workflow_ir`: the coverage-table version the per-node axis verdicts in `nodes_json` were computed against. It is a column rather than a per-node key because it is a PAYLOAD-level fact — one transmission's verdicts all came from one table — and writing it into every node's blob would allow two nodes in one document to disagree about which table they came from, a state the wire cannot produce and the reader has no rule for resolving. NULL means NOT REPORTED and there is deliberately NO backfill: every row already in that table was written by a CLI with no verdicts, and dating them to this build's table would suppress the STALE label that exists to catch exactly that mismatch. Not P26's: P26 reads no structure and creates nothing",
		"0043_p29_linked_transform":               "P29 — the transform RECEIPT (`heros apply --link-receipt`): what a change the customer generated on their own machine did, as per-node applied/refused outcomes and a diffstat. A new table because the GRAIN is new and unrepresentable in any existing one — `run_link` is per RUN and a transform is generated with no run at all, `workflow_ir` is per (workflow, REVISION) and two configurations at one revision are two transforms — and because `(tenant, config_hash, source_revision)` is exactly the key `/app/transforms/{config_hash}/{source_revision}` addresses, the surface that could not resolve for anything before it existed. 🔴 It holds three integers where a diff would go and there is no column one could occupy. Not P26's: P26 reads no transform and creates nothing",
		"0044_p30_proposal_generation_pass":       "P30 — what the last proposal-GENERATION pass found, per (tenant, workflow). `proposalgen` already returned a closed state and a sentence and both were discarded when the HTTP response was written, so the recommendation surface had one input: how many proposal rows exist. Zero rows rendered as `empty` and `empty` rendered as \"Nothing is pending.\" — the same three words for a workflow nobody has ever analysed and one that was analysed and is genuinely healthy, which are opposites (press the button versus you are done). A new table because the states worth recording are precisely the ones that write NO proposal row, so a column on `proposal` would have nowhere to live in exactly the cases it exists for; and because a pass is not pinned to a revision (`revision_mismatch` is one of its outcomes), a column on `workflow_ir` could not hold the state that reports a revision disagreement. One row per pair: a pass REPLACES its predecessor, because the surface asks what the platform currently believes rather than for a log. Not P26's: P26 runs no generation pass and creates nothing",
		"0045_p30_ir_fact_provenance":             "P30 — one NULLABLE column on `platform_workflow_graph`: the canonical SET of fact AUTHORS present in that row's stored view. Per-fact authorship lives in the document (`author` on every edge and label, D4), because a run-level boolean \"cannot answer who authored THIS edge, which is the only question an incident asks\"; this column is a derived INDEX over those facts so \"which graphs contain something the agent wrote\" is a WHERE clause instead of a full scan of customer documents, and a fence asserts derived-equals-stored so it cannot drift from what it indexes. NULL means `legacy` and there is deliberately NO backfill — every existing row predates authorship, and stamping `frontend` would both assert something about facts nobody examined and erase the very distinction the column creates. Creates no table. Not P26's: P26 reads no IR fact and creates nothing",
		"0046_p30_heros_agent":                    "P30 — the four tables the platform-side analysis agent needs: `heros_agent_version` (the immutable published Variant Spec, keyed by config_hash, with its rehearsal state), `heros_inference` (the PINNED result, whose UNIQUE (workflow_id, source_revision, agent_config_hash) IS design D2 — it makes \"the same revision always shows you the same graph\" a property of the store rather than a claim about a model), `heros_abstention` (FR3.4: not knowing is an OUTPUT, at per-declined-subject grain so it is queryable like one, which JSON on the inference row would not be), and `heros_spend` (per tenant per inference, its own table because the caps ask about a TENANT and the inference row is keyed by inference). Four grains, three of them unrepresentable in the others. Timestamps are int64 ms and no timestamp literal appears in the file. 🔴 No column can hold a provider key — `credential_ref` is a provider NAME resolved through providergateway, and the migration itself greps its own catalog for a key-shaped column and raises. Not P26's: P26 runs no inference and creates nothing",
		"0047_p30_tenant_placement":               "P30 — where each tenant's analysis RUNS (`platform | customer | disabled`, task 7.5). A TABLE rather than a column with a default, because THE ABSENCE OF A ROW IS THE VALUE: Q2 makes `disabled` the default and the console distinguishes `defaulted` (nobody has decided) from an explicit `disabled` (somebody reviewed this tenant and switched it off) — the same VALUE and different FACTS, and the one that tells an operator how much of the fleet anybody has actually looked at. A `DEFAULT 'disabled'` column would give every tenant an answer nobody gave, on the day of the migration. It carries the operator's REASON alongside the value because `platform` is what makes this platform read that tenant's source under a platform-held credential, and the audit log records the ACT while this records the current STATE. 🚫 No backfill, for the reason the table exists. Not P26's: P26 places nothing and creates nothing",
		"0050_p33_surface_assessment":             "P33 — the ASSESSMENT (`assessment`) and its exactly-nine FINDINGS (`assessment_finding`). Two tables because both grains are new: a run is per (tenant, workflow, source_revision, agent_config_hash) and a finding is per (run, AXIS), and the axis is half the finding's primary key — which is FR1 (`nine axes, always all nine, none omitted, none duplicated`) enforced by the database rather than by a validator somebody can bypass. The alternatives were written down and refused — findings as JSONB on one row would put DevOps's two required queries (`GROUP BY axis, state`, and the rate of assessments returning NINE `not_measured` findings) behind expression scans, and the second of those is the earliest signal that a language frontend or the sandbox broke, which is precisely the query that must keep working when nobody is looking at it; reusing `heros_inference` would put two different grains in one table, since most findings are STRUCTURAL and carry no pin at all; and a THIRD table for the eval-set report was refused because that report is a DOCUMENT — read only when rendering the one finding it belongs to, never grouped by, never joined — so it is JSONB on the finding row. 🔴 The four conditional requirements are CHECK constraints stated as equivalences (`(state = 'not_measured') = (missing_input IS NOT NULL)`), because the negative half is as load-bearing as the positive one: a `refused` row carrying a missing input renders in two different message shapes depending on which column a console reads first. 🚫 NO COLUMN CAN HOLD A COMPOSITE — there is no `score`, `grade`, `level` or `overall`, and the only numeric columns on `assessment` are the two spend figures, which are money and not quality; program ruling R4 is refused structurally, so a later phase proposing a composite cannot ship it as a value in an existing column. 🚫 No column can hold SOURCE TEXT (§7.4): `claim` is a sentence ABOUT the code and `evidence_locator` names a surface the platform already holds; there is deliberately no `snippet`, no `excerpt` and no `source_line`. Not P26's: P26 assesses nothing and creates nothing",
		"0049_p32_repo_intake":                    "P32 — the repository CONNECTION (`source_connection`), its append-only read LEDGER (`source_clone_record`), and two NULLABLE columns on `source_bundle`. Two tables because both grains are new and unrepresentable: a grant is per (tenant, workflow) and outlives every revision, while `source_bundle` is per (tenant, workflow, REVISION); a read is per CLONE, and many reads share one grant. The alternatives were written down and refused — a third table for cloned snapshots would be a second answer to \"what source is this tenant holding on our disks\", which is the question a deletion request asks and the one place two answers means a deletion misses half the data; and a JSONB blob would put the cascade's and the sweep's predicates behind expression scans nobody indexes correctly under pressure, on the one job that must keep working when nobody is looking at it. 🔴 The two columns are NULLABLE and the NULLs MEAN something: `connection_id IS NULL` is a pushed bundle and `expires_at_ms IS NULL` is no expiry, so every pre-existing row is untouched by both the cascade and the retention sweep — which is what makes Mode 1 unchanged by this migration. A `DEFAULT 0` on the expiry would have expired every bundle ever pushed at the moment it ran. 🚫 No column can hold a forge credential (`external_id` is the forge's own id for the grant, which names it and does not authenticate it) and no column can express a SCOPE — ADR-013 Option B is refused on the record, and a later phase proposing organization-wide access cannot ship it as a string in an existing column because there is no column it could arrive in. Not P26's: P26 reads no source and creates nothing",
		"0048_p30_agent_caps_and_stale":           "P30 — the token CEILINGS a cap check reads before a provider call (task 9.2), plus the STALE mark a stored inference carries when analysis is switched off for its tenant (task 9.5). A cap is its own table rather than a column on `heros_spend`, because the meter has one row per inference and a ceiling exists before any inference does — putting it on the meter means the check reads `no cap` for exactly the tenant nobody has spent anything on yet, which is the tenant a first runaway analysis lands on. The fleet ceiling lives here too under a `tenant_id = ''` sentinel: a second one-row table would be the same data in a second shape that can disagree about what `unset` means. 🔴 `max_tokens = 0` is refused at the schema, because zero is ambiguous between `spend nothing` and `no limit` and a checker reading it would have to guess — removing a cap is a DELETE. The stale columns are NULLABLE with no back-fill (NULL means not stale) and CHECKed against the two-value vocabulary, with a second CHECK making the reason and its timestamp travel together so a stale row can always answer `since when`. Not P26's: P26 spends nothing and analyses nothing",
		"0041_p28_password_identity":              "P28 — the two tables that let a person hold a credential of their own. Production runs the `configured` seam, whose entire mechanism is a JSON map of `{assertion: tenant_id}` injected from a Kubernetes secret, so obtaining a way in means an operator reading that secret out of the cluster and handing the string over — a shared, unrotatable, unattributable secret that member removal does not revoke, which makes P27's central promise false on the seam production actually runs. `user_password` holds an argon2id encoding (CHECKed, because `HashSecret` is SHA-256 and would look like ordinary reuse in review while leaving the table crackable at GPU speed) plus the lockout state, which is in the database because a lock a restart clears is not a lock. `identity_token` is the single-use confirmation and reset link; it is NOT two more `console_session` purposes, because that table's `tenant_id` is NOT NULL and a reset is scoped to a PERSON who may belong to two organizations or to none — and because `auth` resolved session purposes by DENYLIST, so a reset token in that table would silently have been a platform API credential. Not P26's: P26 reads neither and creates nothing",
		"0040_p27_device_authorization":           "P27 §13 — the terminal login. It is a TABLE rather than a per-process map because the CLI polls: it requests a code against one replica and polls against whichever the load balancer picks next, so a map means a login that succeeds or hangs depending on routing, intermittently, with nothing logged",
		"0020_p11_run_links":                      "P11 run linking — the durable Store that let the capability mount (P19 §11)",
		"0021_p11_workflow_ir":                    "P11 opt-in workflow structure — the store behind `heros link --with-ir`, and the data the pattern graph is finally drawn from",
		"0022_platform_discovery":                 "P1 platform-side discovery — the pushed source snapshot and the classified graph discovered from it, which is what lets the pattern graph carry LABELS rather than unclassified dots",
		"0026_delivery_route_mode":                "P12 — adds delivery_route's required `mode` (ci|app); 0025's shape could only hold routes Route.Validate rejects. It ALSO dropped `base_ref` on the false ground that base_ref is not a field of Route — true of the struct, false of the type, since Route.Target.Base is required by Target.Validate. 0027 restores it",
		"0028_repair_account_constraint_guards":   "P7 — repairs 0024's two `account` CHECK guards, which test `pg_constraint` by name only. That catalog is database-wide, so the guard is satisfied by a same-named constraint in ANOTHER schema and the constraint is silently skipped; the runner reads the ledger, so 0024 cannot be edited into a fix. A no-op wherever 0024's guard worked",
		"0032_proposal_refusal":                   "P5.5 — the transform's REFUSAL, which 0012's build_status CHECK has no value for. Recording a refusal as `unbuilt` makes it indistinguishable from a proposal nobody has compiled yet, which re-creates the disappearance BuildRefused was made a status to prevent — and the reason is the thing a reader acts on, so the reason IS the marker",
		"0031_proposal_spec":                      "P5.5 — the candidate Variant Spec. Design Decision 1 opens with `a proposal IS a candidate Variant Spec`, and 0012 stored everything about a proposal except what the change is; without it the codemod has nothing to apply and re-deriving one would compile a DIFFERENT change under an id a customer may already be verifying",
		"0030_proposal_presentation":              "P5.5 — the three fields a proposal CARD renders that 0012 has no column for: node_id, pattern and rationale. Nothing noticed because no Go code had ever read this table into a Card, so the schema and the read model were built in different phases against different assumptions — the same class of gap 0025 found on the same table",
		"0029_verdict_case_counts":                "P5.5 — the case COUNTS on `verdict`. 0012 stored case IDS only, which works when the whole loop runs in one place; a verdict REPORTED by the customer's CI carries counts and no ids (an id is customer-authored text and does not cross the P11 boundary), so every len()-derived reader would report that a change fixing four cases fixed none",
		"0027_delivery_route_base_ref":            "P12 — restores the `base_ref` 0026 dropped, without which a stored route reads back with an empty Target.Base, Prepare rejects it, and Service.Pending swallows the rejection as an empty pending list. The fence is the column round trip in internal/deliveryroute, not another column list",
		"0025_proposal_scope_and_routes":          "P5.5/P12 — tenant+workflow scope on `proposal` (0012 created it single-tenant and workflow-implicit, so the read model was unanswerable from it) plus the delivery_route registry, which forgedelivery.RouteRegistry has needed since P12 and which no migration ever created",
		"0024_billing_durable":                    "P7/P21 — the durable billing ledger and account store. Billing was unmounted because its only Ledger and account Store were in-memory, so mounting it would record a charge and forget it on restart; these are what remove that reason, and what gates registering the Stripe webhook",
		"0034_repair_settled_refs_for_audit_rows": "P7/P21 — repairs 0013's `billing_event_settled_has_refs`, a biconditional over the WHOLE table that demands a provider receipt on every `recorded` row. `plan_change` and `subscription_change` move no money and have no receipt (EventType.ChargeBearing() has always said so), so the entitlement sync's audit row — the FIRST step of granting a plan somebody just paid for — failed with 23514 and the webhook retried forever. Creates no table; the money rule is unchanged",
		"0033_billing_state_mirror":               "P21 — the mirrored provider billing state, the one P21 store that never had a table (0013 created `webhook_delivery`, so the dedupe half only ever needed Go code). It is what the payment-failed, past-due and card-on-file surfaces render from; held in a map it is lost on every restart, silently, because a process that starts with an empty mirror looks exactly like a fresh install — and the provider does not re-send an event it already acknowledged",
		"0037_release_record":                     "P20/P26 — the published-release record the Releases & trust page reads. `ReleaseSource`'s own comment says 'an interface rather than a table (D7 — no new table in this phase)', and D7 was a SCOPING rule for P26, not a claim the record should never be durable: with no implementation the page told an operator 'this deployment does not carry the release oversight surface' forever, on a platform that does publish releases. The seam stays; this is one implementation of it. `verified` is NULLABLE on purpose — NULL is 'not checked' and FALSE is 'checked and failed', and rendering them alike sends somebody hunting a compromise that never happened. Holds a key IDENTIFIER and a digest, never key material",
		"0036_admin_model_registry":               "P8/P21 — the operator model registry and its closed-period price snapshots. `model_entry` (0001) is the P10 prompt/model ENVELOPE store and has no column for a provider, a price reference or a deprecation, so there was nothing to reuse. `adminops.ModelRegistry` was an in-memory map behind a WRITE surface: an operator adds a model and its opaque price ref, the pod restarts, the work is gone — and because SUM is derived from those references, the restart silently changes what the platform believes a run cost. The second table IS non-retroactivity: a closed period keeps the references it closed with, which in a map expired with the process. Not P26's: P26 reads the registry through RegistryService and creates nothing",
		"0035_admin_factor_directory":             "P8/P22 — the operator MFA enrolment directory, the one admin-identity store that never had a table (0014 created admin_principal, admin_role_grant and admin_session, so those halves only ever needed Go code). On a federated deployment NewAuthenticatorFor requires a platform-verified factor, so EVERY sign-in reads it; held in a map it is lost on every restart, and because enrolling a factor needs a session and issuing a session needs a factor, that is a PERMANENT lockout rather than lost state. Not P26's: P26 reads this directory through OversightService and creates nothing",
		"0039_p27_console_session_purpose":        "P27 — separates the browser's COOKIE from the credential the console presents upstream. 0038 created `console_session` for both and they are the same shape — an opaque token, an organization, a person, an expiry, a revocation — and emphatically not the same thing. Without the column a stolen console cookie would authenticate directly against the platform API; today it reaches only the console, which holds the platform credential and exposes a closed set of routes. `auth` resolves ONLY `upstream` rows. Defaults to `upstream` because every row 0038 created was minted by the token exchange, and defaulting to `console` would log every existing scoped token out on upgrade. Not P26's: P26 reads no session row and creates nothing",
		"0038_p27_account_system":                 "P27 — the tenant becomes a row. A tenant was a key in a map `auth.Registry` built from the configuration file at boot, so onboarding a customer was a deploy and self-serve sign-up had nowhere to write its answer; meanwhile `delivery.tenant_id`, `workflow_ir.tenant_id` and six more columns had been foreign keys into nothing for phases. This adds the row they name plus the four records that were unrepresentable without it — a person (`platform_user`, prefixed because USER is reserved), their membership, an invitation, and a hashed credential that can be revoked — and a durable `console_session`, whose absence is why a console rollout is a mass logout and why P19's `replicas: 2` cannot work. Not P26's: P26 reads none of these and creates nothing",
		"0023_run_link_eval_evidence":             "P4/P4.5 — the EVIDENCE behind a linked run's scores (case and seed counts, the customer's own gate verdict, per-node cost/latency). Columns on run_link, not a new table; it is what makes the eval board and the scorecard mountable without the platform inventing a gate outcome",
	}
	numbered := regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)
	for _, e := range entries {
		m := numbered.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		var n int
		for _, r := range m[1] {
			n = n*10 + int(r-'0')
		}
		if n <= highestAtP26Exit {
			continue
		}
		if _, known := postP26[m[1]+"_"+m[2]]; !known {
			t.Fatalf("migration %s creates a table and names no owner. P26 creates NO new table: every "+
				"read derives from an existing store, and where one does not the ledger records "+
				"`not-yet-readable` naming the collection that would make it readable (design D7). If this "+
				"migration belongs to another phase, add it to postP26 with that phase — if it is P26's, "+
				"it must not exist.", e.Name())
		}
	}
}

// TestTheHeadlineMetricCanReportFailure defends P26 task 8.3.
//
// 🔴 The assertion is not that the number is good. It is that the measurement WORKS and can say the
// phase failed — a metric that can only report success is not a metric. So this drives all three
// verdicts through the real classifier over real audit records.
func TestTheHeadlineMetricCanReportFailure(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)

	// BEFORE: four impersonation sessions, three of them opened for a question a P26 surface now
	// answers. These go through the REAL impersonation service, so the reasons land in the real audit
	// chain by the real code path.
	before := []string{
		"ticket 41: the customer asks whether their delivery pull request was merged",
		"ticket 42: checking why this tenant's prompt change was refused — coverage question",
		"ticket 43: which version is this tenant running, signing key question after the rotation",
		"ticket 44: reproducing a rendering fault the customer reported on their own dashboard",
	}
	for _, reason := range before {
		if _, _, err := h.impersonation.Start(ctx, tenantAcme, reason, 5*time.Minute, adminops.Confirm()); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	baseline := adminops.MeasureDisplacement("before", h.audit)
	if baseline.Sessions != 4 {
		t.Fatalf("baseline counted %d sessions, want 4", baseline.Sessions)
	}
	if baseline.Displaceable != 3 {
		t.Fatalf("baseline classified %d sessions as displaceable, want 3 (by_subject=%v, unclassified=%d)",
			baseline.Displaceable, baseline.BySubject, baseline.Unclassified)
	}
	if baseline.Unclassified != 1 {
		t.Fatalf("baseline left %d reasons unclassified, want 1 — the remainder must be reported, not "+
			"assumed to be zero", baseline.Unclassified)
	}

	// The three verdicts, driven through the real comparator.
	unmoved := adminops.CompareDisplacement(baseline, baseline)
	if !strings.HasPrefix(unmoved.Verdict, "UNMOVED") {
		t.Fatalf("an unchanged ratio reports %q — it must say the surfaces did not displace what they "+
			"were built to displace", unmoved.Verdict)
	}
	improved := adminops.CompareDisplacement(baseline, adminops.DisplacementReading{
		Label: "after", Sessions: 4, Displaceable: 1, Unclassified: 3, Ratio: 0.25,
	})
	if !strings.HasPrefix(improved.Verdict, "DISPLACED") {
		t.Fatalf("a fallen ratio reports %q", improved.Verdict)
	}
	if improved.Delta >= 0 {
		t.Fatalf("a fallen ratio has delta %v, want negative", improved.Delta)
	}
	worse := adminops.CompareDisplacement(adminops.DisplacementReading{
		Label: "before", Sessions: 4, Displaceable: 1, Ratio: 0.25,
	}, baseline)
	if !strings.HasPrefix(worse.Verdict, "WORSE") {
		t.Fatalf("a risen ratio reports %q — the metric must be able to say this went the wrong way",
			worse.Verdict)
	}
	// And an absent baseline is not silently reported as a perfect score.
	none := adminops.CompareDisplacement(adminops.DisplacementReading{Label: "before"}, baseline)
	if !strings.HasPrefix(none.Verdict, "NO BASELINE") {
		t.Fatalf("an empty baseline reports %q — a ratio over an empty corpus is not a measurement",
			none.Verdict)
	}

	// The credited surfaces are the ones the ledger says exist, so the claim is checkable.
	for _, want := range []string{"/axes", "/billing", "/delivery", "/oversight", "/releases"} {
		var found bool
		for _, s := range unmoved.Surfaces {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("the metric credits %v, which does not include %s", unmoved.Surfaces, want)
		}
	}
}

// TestTheDisplacementClassifierReportsItsRemainder defends the honesty of the number itself.
func TestTheDisplacementClassifierReportsItsRemainder(t *testing.T) {
	h := newHarness(t)
	ctx := h.ctx(adminrbac.RoleSupport)
	if _, _, err := h.impersonation.Start(ctx, tenantAcme,
		"ticket 90: something the classifier has no term for at all", time.Minute, adminops.Confirm()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r := adminops.MeasureDisplacement("after", h.audit)
	if r.Unclassified != 1 || r.Displaceable != 0 {
		t.Fatalf("an unmatched reason was classified: %+v", r)
	}
	if r.Ratio != 0 {
		t.Fatalf("ratio = %v with nothing displaceable", r.Ratio)
	}
	// The subject list stays SHORT. A long one would inflate the number by claiming credit for lookups
	// these surfaces do not answer.
	if n := len(adminops.DisplaceableSubjects()); n > 6 {
		t.Fatalf("the displaceable-subject list has grown to %d entries — every addition claims credit "+
			"for a lookup, and the metric is only as honest as that list is short", n)
	}
	for _, s := range adminops.DisplaceableSubjects() {
		if s.Surface == "" || len(s.Terms) == 0 {
			t.Fatalf("displaceable subject %s names no surface or no terms", s.ID)
		}
	}
}
