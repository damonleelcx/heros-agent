package sourceingest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// connection_test.go covers the connection lifecycle: the breadth refusal per forge (§7.4), the
// revocation cascade (§7.3), rotation (§7.5), the four causes (2.9), retention (§7.6) and the per-forge
// metrics breakdown (§7.7).
//
// # 🔴 Every assertion here is a DOWNSTREAM read, not a return value
//
// `practice/state-mutation-and-error-surface` rule A: a test that stops at the function's return has
// asserted that a function returned, not that the state changed. So after a revoke this file re-reads
// the snapshot store, the credential store and the Source — three separate downstream paths — because
// the failure design D3 describes is exactly the one where `Revoke` returns nil and the tree is still
// readable.

// ── the breadth refusal, per forge (§7.4) ────────────────────────────────────────────────────────

// TestConnectRefusesAGrantBroaderThanOneRepository is ADR-013 Option B, enforced.
//
// Per forge, because §14 A2 says the grant is expressed differently on each and the whole hazard is a
// workspace-scoped grant recorded as though it were repository-scoped.
func TestConnectRefusesAGrantBroaderThanOneRepository(t *testing.T) {
	for _, forge := range Forges() {
		kind, err := ExpectedGrantKind(forge)
		if err != nil {
			t.Fatalf("%s has no adapter: %v", forge, err)
		}
		base := func() Authorization {
			return Authorization{Forge: forge, GrantKind: kind, Token: "t", Covers: []string{"acme/api"}}
		}

		t.Run(string(forge)+"/account-wide", func(t *testing.T) {
			a := base()
			a.AccountWide = true
			// 🔴 Covers is left NAMING THE RIGHT REPOSITORY. An account-wide grant that also happens to
			// list the requested repository is the realistic shape — GitHub reports the selected repos
			// on an "all repositories" installation too — and a check that only compared the list would
			// admit it.
			assertTooBroad(t, a.Validate("acme/api"))
		})

		t.Run(string(forge)+"/covers-another", func(t *testing.T) {
			a := base()
			a.Covers = []string{"acme/other"}
			assertTooBroad(t, a.Validate("acme/api"))
		})

		t.Run(string(forge)+"/covers-two", func(t *testing.T) {
			a := base()
			a.Covers = []string{"acme/api", "acme/web"}
			assertTooBroad(t, a.Validate("acme/api"))
		})

		t.Run(string(forge)+"/covers-nothing", func(t *testing.T) {
			a := base()
			a.Covers = nil
			// An empty list is refused rather than treated as "covers nothing, therefore harmless": a
			// forge that reports no coverage has told us nothing, and admitting an unknown reach is
			// exactly the case this refusal exists for.
			assertTooBroad(t, a.Validate("acme/api"))
		})

		t.Run(string(forge)+"/write-scope", func(t *testing.T) {
			a := base()
			a.Scopes = []string{"contents:write"}
			if err := a.Validate("acme/api"); err == nil || !strings.Contains(err.Error(), "write") {
				t.Fatalf("err = %v, want a refusal naming the write scope", err)
			}
		})

		t.Run(string(forge)+"/exact-match-is-admitted", func(t *testing.T) {
			// The control. Without it this whole table would still pass if Validate refused everything.
			if err := base().Validate("acme/api"); err != nil {
				t.Fatalf("a correctly-scoped %s grant was refused: %v", forge, err)
			}
		})
	}
}

func assertTooBroad(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrGrantTooBroad) {
		t.Fatalf("err = %v, want ErrGrantTooBroad", err)
	}
}

// TestEveryForgeHasAnAdapter keeps `Forges()` and `forgeAdapters` from drifting.
//
// A forge in the enum with no adapter would fail at 03:00 with a DNS error rather than at build time.
func TestEveryForgeHasAnAdapter(t *testing.T) {
	for _, f := range Forges() {
		d, err := DescribeForge(f)
		if err != nil {
			t.Errorf("%s is in Forges() and has no adapter: %v", f, err)
			continue
		}
		if d.Host == "" || d.GrantLabel == "" || d.Permission == "" || d.RevokeHint == "" {
			t.Errorf("%s's description is incomplete: %+v — the consent screen renders every one of these", f, d)
		}
		if !d.GrantKind.Valid() {
			t.Errorf("%s issues %q, which is not a grant kind", f, d.GrantKind)
		}
	}
	if len(CloneHosts()) != len(Forges()) {
		t.Errorf("CloneHosts() has %d entries and Forges() has %d — the egress allowlist is checked "+
			"against CloneHosts(), so a forge missing from it can never be reached",
			len(CloneHosts()), len(Forges()))
	}
}

// TestGrantKindMustMatchWhatTheForgeIssues pins §14 A2.
//
// A GitHub `access_token` where an App installation was expected is a fine-grained PAT wearing the
// App's label — a different object with a different revocation story, recorded as the one we decided on.
func TestGrantKindMustMatchWhatTheForgeIssues(t *testing.T) {
	svc, store, secrets := newTestService(t)
	_, err := svc.Connect(context.Background(), ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: Authorization{
			Forge: ForgeGitHub, GrantKind: GrantAccessToken, Token: "tok", Covers: []string{"acme/api"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "app_installation") {
		t.Fatalf("err = %v, want a refusal naming the kind GitHub actually issues", err)
	}
	// Downstream: nothing was written, in either store.
	if conns, _ := store.List(context.Background(), "t1"); len(conns) != 0 {
		t.Errorf("a refused connection left %d row(s) behind", len(conns))
	}
	if refs := secrets.Refs(); len(refs) != 0 {
		t.Errorf("a refused connection left %d credential(s) behind: %v", len(refs), refs)
	}
}

// TestConnectRefusesWithoutTheDisclosure is FR10 enforced at the WRITE.
//
// A check that lives only in the console is a check a second client does not have.
func TestConnectRefusesWithoutTheDisclosure(t *testing.T) {
	svc, store, secrets := newTestService(t)
	_, err := svc.Connect(context.Background(), ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api",
		ConsentShown:  false,
		Authorization: goodAuth(),
	})
	if err == nil || !strings.Contains(err.Error(), "disclosure") {
		t.Fatalf("err = %v, want a refusal naming the missing disclosure", err)
	}
	if conns, _ := store.List(context.Background(), "t1"); len(conns) != 0 {
		t.Error("a connection was created without its disclosure having been displayed")
	}
	if len(secrets.Refs()) != 0 {
		t.Error("a credential was stored for a connection that was refused")
	}
}

// ── the revocation cascade (§7.3) ────────────────────────────────────────────────────────────────

// TestRevokeDeletesTheGrantTheCredentialAndTheDerivedTrees is design D3, asserted through THREE
// downstream reads.
//
// 🔴 The point of this test is the second and third assertions. Deleting the grant row is one line and
// it is the assertion a careless version of this test would stop at — and the failure D3 names is
// precisely the one where the row is gone and the system keeps answering from a tree it is no longer
// authorized to hold.
func TestRevokeDeletesTheGrantTheCredentialAndTheDerivedTrees(t *testing.T) {
	ctx := context.Background()
	svc, store, secrets := newTestService(t)

	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		CreatedBy: "u1", Authorization: goodAuth(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Two derived snapshots, at two revisions — so the cascade is asserted over more than one row and
	// a `LIMIT 1` in the delete would show up.
	for _, rev := range []string{"rev1", "rev2"} {
		ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: rev}
		if err := store.PutDerived(ctx, ref, tinyArchive(t), conn.ConnectionID, 1_000_000); err != nil {
			t.Fatalf("put derived %s: %v", rev, err)
		}
	}
	// And one PUSHED bundle for a different workflow, which must SURVIVE. Mode 1 is not collateral.
	pushed := Ref{TenantID: "t1", WorkflowID: "wf-bundle", SourceRevision: "rev1"}
	if err := store.Put(ctx, pushed, tinyArchive(t)); err != nil {
		t.Fatalf("put pushed: %v", err)
	}

	res, err := svc.Revoke(ctx, "t1", conn.ConnectionID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if res.SnapshotsDeleted != 2 {
		t.Errorf("SnapshotsDeleted = %d, want 2", res.SnapshotsDeleted)
	}

	// 1) The grant row is gone.
	if _, err := store.ForWorkflow(ctx, "t1", "wf1"); !errors.Is(err, ErrNoConnection) {
		t.Errorf("ForWorkflow after revoke = %v, want ErrNoConnection", err)
	}
	// 2) The derived trees are ABSENT — read back through the store, not inferred from the count.
	for _, rev := range []string{"rev1", "rev2"} {
		ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: rev}
		if _, err := store.Open(ctx, ref); !errors.Is(err, ErrNoSource) {
			t.Errorf("the derived tree at %s is STILL READABLE after revocation (err = %v). "+
				"A revocation that can be answered from cache is not a revocation (design D3).", rev, err)
		}
	}
	// 3) The credential is gone.
	if refs := secrets.Refs(); len(refs) != 0 {
		t.Errorf("the forge credential survived revocation: %v", refs)
	}
	// 4) The pushed bundle SURVIVED. §7.11: Mode 1 loses nothing.
	if _, err := store.Open(ctx, pushed); err != nil {
		t.Errorf("revoking a connection deleted an unrelated PUSHED bundle: %v", err)
	}
}

// TestReadAfterRevokeIsErrNoSource closes the loop through the SOURCE, which is what a caller uses.
//
// The store-level assertions above prove the rows are gone; this one proves the property the spec
// actually states — *"a subsequent read returns ErrNoSource rather than a cached answer"*.
func TestReadAfterRevokeIsErrNoSource(t *testing.T) {
	ctx := context.Background()
	svc, store, secrets := newTestService(t)
	src := newTestGitSource(t, store, secrets, okClone(t))

	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "abc123"}

	m, err := src.Materialize(ctx, ref)
	if err != nil {
		t.Fatalf("materialize before revoke: %v", err)
	}
	m.Release()

	if _, err := svc.Revoke(ctx, "t1", conn.ConnectionID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := src.Materialize(ctx, ref); !errors.Is(err, ErrNoSource) {
		t.Fatalf("Materialize after revoke = %v, want ErrNoSource", err)
	}
}

// ── the four causes, and no fallback (2.9, §7.5) ─────────────────────────────────────────────────

// TestCloneFailuresReportExactlyOneOfFourCauses drives every cause through the real classifier.
//
// The git RUNNER is injected; the classification is not. A test that stubbed the classifier would be
// asserting that a map lookup works.
func TestCloneFailuresReportExactlyOneOfFourCauses(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want CloneCause
	}{
		{"github auth", "remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/acme/api.git/'", CauseCredentialRejected},
		{"gitlab 401", "fatal: unable to access 'https://gitlab.com/acme/api.git/': The requested URL returned error: 401", CauseCredentialRejected},
		{"terminal prompts disabled", "fatal: could not read Username for 'https://github.com': terminal prompts disabled", CauseCredentialRejected},
		{"repository gone", "remote: Repository not found.\nfatal: repository 'https://github.com/acme/api.git/' not found", CauseRepositoryNotFound},
		{"bitbucket 404", "fatal: unable to access 'https://bitbucket.org/acme/api.git/': The requested URL returned error: 404", CauseRepositoryNotFound},
		{"revision gone", "fatal: couldn't find remote ref deadbeef", CauseRevisionNotFound},
		{"unadvertised object", "error: Server does not allow request for unadvertised object deadbeef", CauseRevisionNotFound},
		// 🔴 upload-pack's OWN wording, which the first version of this table did not have — it was
		// written from GitHub's HTTPS messages, and this is what the git protocol says. The live
		// pgproof clone found it falling through to `network`. Kept here so the fast suite covers it
		// too, and so a future edit to the patterns cannot lose it without a red unit test.
		{"upload-pack has the repo and not the object",
			"fatal: git upload-pack: not our ref dededededededededededededededededededede\n" +
				"fatal: remote error: upload-pack: not our ref dededededededededededededededededededede",
			CauseRevisionNotFound},
		{"dns", "fatal: unable to access 'https://github.com/acme/api.git/': Could not resolve host: github.com", CauseNetwork},
		{"tls reset", "fatal: unable to access: OpenSSL SSL_read: Connection reset by peer", CauseNetwork},
	}
	seen := map[CloneCause]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyGitFailure(fmt.Errorf("exit status 128"), tc.out, "https://github.com/acme/api.git", "abc123")
			cause, ok := CauseOf(err)
			if !ok {
				t.Fatalf("err = %v, want a *CloneError", err)
			}
			if cause != tc.want {
				t.Errorf("cause = %q, want %q\ngit said: %s", cause, tc.want, tc.out)
			}
			seen[cause] = true
		})
	}
	// 🔴 All four must be REACHABLE. A classifier that could only ever produce three causes would pass
	// every case above and still make one of the console's four messages dead code.
	for _, c := range CloneCauses() {
		if !seen[c] {
			t.Errorf("no fixture produces %q — a cause nothing can reach is a message nobody will see", c)
		}
	}
}

// TestAnUnrecognisedGitFailureIsNetworkNotCredential pins the DEFAULT's direction.
//
// Calling an unknown failure `credential rejected` sends the customer to rotate a working token;
// calling it `network` sends an operator to look at the platform. The unknown case is ours.
func TestAnUnrecognisedGitFailureIsNetworkNotCredential(t *testing.T) {
	err := classifyGitFailure(fmt.Errorf("exit status 128"), "fatal: something nobody has seen before", "https://github.com/acme/api.git", "abc")
	cause, _ := CauseOf(err)
	if cause != CauseNetwork {
		t.Fatalf("cause = %q, want %q — an unrecognised failure must be ours until proven otherwise", cause, CauseNetwork)
	}
}

// TestARotatedCredentialDoesNotServeAnOlderSnapshot is §7.5 and design D4.
//
// 🔴 The setup matters: a snapshot for an EARLIER revision exists and is perfectly readable. The
// fallback this refuses is the two-line change that would serve it, and it would look like robustness.
func TestARotatedCredentialDoesNotServeAnOlderSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, store, secrets := newTestService(t)

	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// An older snapshot the platform legitimately holds.
	older := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "old111"}
	if err := store.PutDerived(ctx, older, tinyArchive(t), conn.ConnectionID, 1_000_000); err != nil {
		t.Fatalf("put older: %v", err)
	}

	// The forge now rejects the credential.
	rejecting := func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "fetch" {
			return "fatal: Authentication failed for 'https://github.com/acme/api.git/'", fmt.Errorf("exit status 128")
		}
		return "", nil
	}
	src := newTestGitSource(t, store, secrets, rejecting)

	newer := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "new222"}
	_, err = src.Materialize(ctx, newer)
	cause, ok := CauseOf(err)
	if !ok || cause != CauseCredentialRejected {
		t.Fatalf("Materialize = %v, want a credential_rejected CloneError", err)
	}
	// Downstream: the older snapshot is untouched AND was not served. Both halves matter — a cascade
	// bug that deleted it would also make this test pass if it only checked the error.
	if _, err := store.Open(ctx, older); err != nil {
		t.Errorf("the failed clone destroyed an older snapshot: %v", err)
	}
	if _, err := store.Open(ctx, newer); !errors.Is(err, ErrNoSource) {
		t.Errorf("a snapshot exists at the FAILED revision (err = %v) — something wrote one anyway", err)
	}

	// And the ledger records the failure with its cause.
	records, err := svc.Records(ctx, "t1", conn.ConnectionID, 10)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(records) != 1 || records[0].Outcome != Outcome(CauseCredentialRejected) {
		t.Fatalf("ledger = %+v, want one row whose outcome is the cause", records)
	}
}

// TestRotateReplacesTheCredentialWithoutTouchingTheTrees is task 3.2.
//
// A rotation is the same customer, the same repository, new material. Treating it as a re-connect would
// delete trees the customer never asked to lose.
func TestRotateReplacesTheCredentialWithoutTouchingTheTrees(t *testing.T) {
	ctx := context.Background()
	svc, store, secrets := newTestService(t)
	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "rev1"}
	if err := store.PutDerived(ctx, ref, tinyArchive(t), conn.ConnectionID, 1_000_000); err != nil {
		t.Fatalf("put derived: %v", err)
	}

	if err := svc.Rotate(ctx, "t1", conn.ConnectionID, "rotated-token"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// The NEW value is what a clone would use. Read through the custody closure, which is the only
	// way to read it — there is no accessor, which is the point.
	var got string
	if err := secrets.UseForgeToken(ctx,
		providergateway.ForgeRef{Forge: "github", ConnectionID: conn.ConnectionID},
		func(tok string) error { got = tok; return nil }); err != nil {
		t.Fatalf("use token: %v", err)
	}
	if got != "rotated-token" {
		t.Errorf("token = %q, want the rotated value", got)
	}
	if _, err := store.Open(ctx, ref); err != nil {
		t.Errorf("a rotation deleted a derived tree: %v", err)
	}
	if err := svc.Rotate(ctx, "t1", conn.ConnectionID, ""); err == nil {
		t.Error("rotating to an empty credential was accepted; the connection would fail at its next read")
	}
}

// ── retention (§7.6) ─────────────────────────────────────────────────────────────────────────────

// TestRetentionRemovesExpiredCloneSnapshotsAndNotPushedOnes is FR16 and §14 A4.
func TestRetentionRemovesExpiredCloneSnapshotsAndNotPushedOnes(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	now := int64(1_000_000)

	expired := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "old"}
	live := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "new"}
	pushed := Ref{TenantID: "t1", WorkflowID: "wf2", SourceRevision: "rev"}
	if err := store.PutDerived(ctx, expired, tinyArchive(t), "conn1", now-1); err != nil {
		t.Fatalf("put expired: %v", err)
	}
	if err := store.PutDerived(ctx, live, tinyArchive(t), "conn1", now+1); err != nil {
		t.Fatalf("put live: %v", err)
	}
	if err := store.Put(ctx, pushed, tinyArchive(t)); err != nil {
		t.Fatalf("put pushed: %v", err)
	}

	job := NewRetentionJob(RetentionConfig{Snapshots: store, NowMS: func() int64 { return now }})
	if h := job.Health(); h.Status != RetentionNeverRun {
		t.Errorf("before any run Status = %q, want %q — a process that has not swept yet is not degraded", h.Status, RetentionNeverRun)
	}

	n, err := job.RunOnce(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d, want 1 (only the expired clone snapshot)", n)
	}
	if _, err := store.Open(ctx, expired); !errors.Is(err, ErrNoSource) {
		t.Errorf("the expired snapshot is still readable: %v", err)
	}
	if _, err := store.Open(ctx, live); err != nil {
		t.Errorf("an UNexpired clone snapshot was swept: %v", err)
	}
	// 🔴 The pushed bundle has no expiry and must be untouched forever. Sweeping it would delete an
	// artifact the customer was told is kept until they delete it (§14 A4) — and would be the exact
	// shape of accident the migration's NULL-means-no-expiry comment exists to prevent.
	if _, err := store.Open(ctx, pushed); err != nil {
		t.Errorf("the retention sweep deleted a PUSHED bundle: %v", err)
	}

	h := job.Health()
	if h.Status != RetentionReady || h.LastSuccessMS != now {
		t.Errorf("health = %+v, want ready with last_success_ms = %d", h, now)
	}
	if h.WindowHours != int64(DefaultCloneRetention.Hours()) {
		t.Errorf("WindowHours = %d, want %d — the window is published so a monitor need not hard-code it",
			h.WindowHours, int64(DefaultCloneRetention.Hours()))
	}
}

// TestRetentionEscalatesAfterConsecutiveFailures is task 5.3.
//
// A job failing forever at WARN is a job nobody fixes.
func TestRetentionEscalatesAfterConsecutiveFailures(t *testing.T) {
	ctx := context.Background()
	job := NewRetentionJob(RetentionConfig{
		Snapshots: failingSnapshots{},
		NowMS:     func() int64 { return 1 },
	})
	for i := 1; i <= EscalateAfterConsecutiveFailures; i++ {
		if _, err := job.RunOnce(ctx); err == nil {
			t.Fatalf("run %d succeeded against a failing store", i)
		}
		want := RetentionDegraded
		if i >= EscalateAfterConsecutiveFailures {
			want = RetentionEscalated
		}
		if got := job.Health().Status; got != want {
			t.Errorf("after %d failure(s) Status = %q, want %q", i, got, want)
		}
	}
	// One success clears it. An escalation that could not be cleared would make the signal useless
	// after the first bad afternoon.
	job.snaps = NewMemStore()
	if _, err := job.RunOnce(ctx); err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	if h := job.Health(); h.Status != RetentionReady || h.ConsecutiveFailures != 0 {
		t.Errorf("after recovery health = %+v, want ready with no consecutive failures", h)
	}
}

// ── per-forge metrics (§7.7) ─────────────────────────────────────────────────────────────────────

// TestIngestMetricsAreBrokenOutPerForge asserts the BREAKDOWN, because the aggregate is what gets
// built if nobody checks.
func TestIngestMetricsAreBrokenOutPerForge(t *testing.T) {
	m := NewIngestMetrics()

	// A forge nobody has used must still appear, at zero. "GitHub is at 100% and Bitbucket is absent"
	// reads as "everyone uses GitHub" when it means the Bitbucket adapter has never been reached.
	h := m.Health()
	if len(h.PerForge) != len(Forges()) {
		t.Fatalf("PerForge has %d entries before any traffic, want %d — a forge that has never been "+
			"used must appear at zero", len(h.PerForge), len(Forges()))
	}
	for _, s := range h.PerForge {
		if len(s.ByCause) != len(CloneCauses()) {
			t.Errorf("%s.ByCause has %d keys, want %d — an absent key and a zero are indistinguishable "+
				"to a dashboard", s.Forge, len(s.ByCause), len(CloneCauses()))
		}
	}

	m.Observe(ForgeGitHub, "", 100, 250*time.Millisecond)
	m.Observe(ForgeGitHub, "", 200, 750*time.Millisecond)
	// Bitbucket is completely broken — the single-sample defect an aggregate hides.
	for i := 0; i < 3; i++ {
		m.Observe(ForgeBitbucket, CauseCredentialRejected, 0, 10*time.Millisecond)
	}

	h = m.Health()
	byForge := map[string]ForgeStats{}
	for _, s := range h.PerForge {
		byForge[s.Forge] = s
	}
	gh := byForge["github"]
	if gh.Succeeded != 2 || gh.Bytes != 300 || gh.DurationMaxMS != 750 {
		t.Errorf("github = %+v, want 2 successes / 300 bytes / max 750ms", gh)
	}
	bb := byForge["bitbucket"]
	if bb.Failed != 3 || bb.ByCause[string(CauseCredentialRejected)] != 3 {
		t.Errorf("bitbucket = %+v, want 3 credential_rejected failures", bb)
	}
	// 🔴 The escalation names the forge. This is the assertion that makes the breakdown load-bearing:
	// the aggregate here is 2/5 = 40%, which reads as "something is a bit wrong", while the breakdown
	// says "bitbucket has failed three times in a row and github is fine".
	if len(h.Escalated) != 1 || h.Escalated[0] != "bitbucket" {
		t.Errorf("Escalated = %v, want exactly [bitbucket]", h.Escalated)
	}
	if h.Total.Succeeded != 2 || h.Total.Failed != 3 {
		t.Errorf("Total = %+v, want 2 succeeded / 3 failed", h.Total)
	}
	// A success clears the streak for that forge only.
	m.Observe(ForgeBitbucket, "", 10, time.Millisecond)
	if h := m.Health(); len(h.Escalated) != 0 {
		t.Errorf("Escalated = %v after a success, want empty", h.Escalated)
	}
}

// ── the ledger (2.8) ─────────────────────────────────────────────────────────────────────────────

// TestTheLedgerDistinguishesAttendedFromUnattendedReads is FR9.
//
// 🔴 And it asserts the DEFAULT: a read with no read-context recorded as `scheduled`. An unattributed
// read recorded as person-initiated is a lie the ledger cannot be corrected from, so the fail-toward-
// disclosure direction is the only acceptable default.
func TestTheLedgerDistinguishesAttendedFromUnattendedReads(t *testing.T) {
	ctx := context.Background()
	svc, store, secrets := newTestService(t)
	src := newTestGitSource(t, store, secrets, okClone(t))

	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	person := WithReadContext(ctx, ActorPerson, "u1", "conversation c1")
	if m, err := src.Materialize(person, Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "r1"}); err != nil {
		t.Fatalf("attended read: %v", err)
	} else {
		m.Release()
	}
	// No read context at all — the scheduled path.
	if m, err := src.Materialize(ctx, Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "r2"}); err != nil {
		t.Fatalf("unattended read: %v", err)
	} else {
		m.Release()
	}

	records, err := svc.Records(ctx, "t1", conn.ConnectionID, 10)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("ledger has %d rows, want 2", len(records))
	}
	// Newest first.
	if records[0].Actor != ActorScheduled || records[0].Revision != "r2" {
		t.Errorf("newest record = %+v, want the unattended read of r2 recorded as scheduled", records[0])
	}
	if records[1].Actor != ActorPerson || records[1].ActorID != "u1" || records[1].Reason != "conversation c1" {
		t.Errorf("older record = %+v, want the attended read naming the person and the reason", records[1])
	}
	for _, r := range records {
		if r.Outcome != OutcomeSucceeded {
			t.Errorf("record %s outcome = %q, want succeeded", r.RecordID, r.Outcome)
		}
		if r.Entries == 0 || r.Bytes == 0 {
			t.Errorf("record %s recorded no entries or bytes: %+v — the per-forge size metric has no source", r.RecordID, r)
		}
	}
}

// TestASecondConnectionForOneWorkflowIsRefused pins the one-repository-per-workflow rule.
func TestASecondConnectionForOneWorkflowIsRefused(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t)
	req := ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	}
	if _, err := svc.Connect(ctx, req); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	req.Repository = "acme/other"
	req.Authorization.Covers = []string{"acme/other"}
	if _, err := svc.Connect(ctx, req); !errors.Is(err, ErrConnectionExists) {
		t.Fatalf("second connect = %v, want ErrConnectionExists — repointing is revoke plus create, "+
			"which is two deliberate acts and two ledger entries", err)
	}
}

// ── credential containment (task 3.3) ────────────────────────────────────────────────────────────

// TestNoForgeCredentialReachesAnOutputSurface is task 3.3.
//
// 🔴 The redaction is asserted on git's OUTPUT, which is where the credential actually appears: git
// echoes the remote URL in most of its failure messages, and that URL carries the token.
func TestNoForgeCredentialReachesAnOutputSurface(t *testing.T) {
	// Dictionary-shaped and distinctive, per the convention in credential_test.go — a 26-character
	// lowercase run is what the CI secret scan flags, and the fixture is the thing to fix.
	const secret = "ghs_redaction_probe_do_not_leak"
	url, err := CloneURL(ForgeGitHub, "acme/api", "inst-1", secret)
	if err != nil {
		t.Fatalf("clone url: %v", err)
	}
	if !strings.Contains(url, secret) {
		t.Fatal("the credentialed URL does not carry the token; this test is asserting nothing")
	}

	gitSaid := "fatal: unable to access '" + url + "': The requested URL returned error: 403\n" +
		"remote: repository " + url + " is unavailable"
	err = classifyGitFailure(fmt.Errorf("exit status 128"), gitSaid, "https://github.com/acme/api.git", "abc")

	// Every surface the error reaches: the message, the typed detail, and a %+v of the whole value.
	for _, surface := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%v", err)} {
		if strings.Contains(surface, secret) {
			t.Fatalf("the forge credential appears in an error surface:\n%s", surface)
		}
	}
	var ce *CloneError
	if !errors.As(err, &ce) {
		t.Fatal("not a CloneError")
	}
	if strings.Contains(ce.Detail, secret) {
		t.Fatalf("the credential is in CloneError.Detail: %s", ce.Detail)
	}
	// And the redaction did not destroy the useful part.
	if !strings.Contains(ce.Detail, "github.com/acme/api") {
		t.Errorf("the redaction removed the host and path too: %q — an operator needs to know WHICH "+
			"repository this was", ce.Detail)
	}
	if !strings.Contains(ce.Detail, "[redacted]") {
		t.Errorf("Detail = %q, want the redaction marker so a reader knows something was removed", ce.Detail)
	}
}

// TestForgeSecretsHasNoAccessorThatReturnsTheToken pins the custody shape.
//
// ADR-005 refused `GetToken(id) (string, error)` for the write kind. The read kind — the one used while
// nobody is watching — must not be weaker.
func TestForgeSecretsHasNoAccessorThatReturnsTheToken(t *testing.T) {
	ty := providergatewayForgeSecretsType()
	for i := 0; i < ty.NumMethod(); i++ {
		m := ty.Method(i)
		for j := 0; j < m.Type.NumOut(); j++ {
			if m.Type.Out(j).Kind().String() == "string" {
				t.Errorf("ForgeSecrets.%s returns a string — a method that HANDS OUT the token is one a "+
					"caller can forget to keep out of a log line", m.Name)
			}
		}
	}
}

// ── the mode router (D1, D4) ─────────────────────────────────────────────────────────────────────

// TestTheRouterNeverFallsBackToABundleAfterACloneFailure is design D4 at the routing layer.
//
// 🔴 This is the most likely place for the defect to be introduced, because the fallback is two lines
// and it would look like robustness.
func TestTheRouterNeverFallsBackToABundleAfterACloneFailure(t *testing.T) {
	ctx := context.Background()
	svc, store, secrets := newTestService(t)
	if _, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "rev1"}
	// A PUSHED bundle exists at exactly the revision being asked for. This is the bait.
	if err := store.Put(ctx, ref, tinyArchive(t)); err != nil {
		t.Fatalf("put bundle: %v", err)
	}

	failing := func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "fetch" {
			return "fatal: couldn't find remote ref rev1", fmt.Errorf("exit status 128")
		}
		return "", nil
	}
	bundles := newTestBundleSource(t, store)
	git := newTestGitSourceWith(t, store, secrets, failing, bundles)
	router := NewModeRouter(store, bundles, git)

	// 🔴 The bait is left in place, unexpired and perfectly readable. That is the whole test: a
	// pushed bundle at exactly the revision being asked for is the most tempting thing in the system
	// to serve, and a connected workflow must not be served it — not by the router falling back, and
	// not by GitSource's same-revision shortcut mistaking it for a tree this connection produced.
	//
	// This case found a real defect: the first version of `LiveSnapshot` did not take the connection,
	// so it answered YES about this bundle and the connection never cloned anything.
	_, merr := router.Materialize(ctx, ref)
	if cause, ok := CauseOf(merr); !ok || cause != CauseRevisionNotFound {
		t.Fatalf("Materialize = %v, want a revision_not_found CloneError — a connected workflow whose "+
			"clone failed must NOT be served a pushed bundle", merr)
	}
	// And the bait is still there, untouched — a failed clone does not destroy anything either.
	if _, err := store.Open(ctx, ref); err != nil {
		t.Errorf("the failed clone destroyed the unrelated pushed bundle: %v", err)
	}
}

// TestTheRouterUsesTheBundlePathWhenThereIsNoConnection is the control, and FR12.
func TestTheRouterUsesTheBundlePathWhenThereIsNoConnection(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	secrets := providergateway.NewMemForgeSecrets()
	ref := Ref{TenantID: "t1", WorkflowID: "wf-nobody-connected", SourceRevision: "rev1"}
	if err := store.Put(ctx, ref, tinyArchive(t)); err != nil {
		t.Fatalf("put: %v", err)
	}
	bundles := newTestBundleSource(t, store)
	git := newTestGitSourceWith(t, store, secrets, refusingClone, bundles)
	router := NewModeRouter(store, bundles, git)

	m, err := router.Materialize(ctx, ref)
	if err != nil {
		t.Fatalf("a workflow with no connection could not read its pushed bundle: %v", err)
	}
	defer m.Release()
	if _, err := os.Stat(filepath.Join(m.Dir, "a.go")); err != nil {
		t.Errorf("the extracted tree is missing its file: %v", err)
	}
	mode, err := router.ModeOf(ctx, "t1", "wf-nobody-connected")
	if err != nil || mode != ModeBundle {
		t.Errorf("ModeOf = %q, %v; want %q", mode, err, ModeBundle)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

func goodAuth() Authorization {
	return Authorization{
		Forge: ForgeGitHub, GrantKind: GrantAppInstallation, ExternalID: "inst-1",
		Token: "ghs_goodauth_probe_do_not_leak", Covers: []string{"acme/api"}, Scopes: []string{"contents:read", "metadata:read"},
	}
}

func newTestService(t *testing.T) (*Service, *MemStore, *providergateway.MemForgeSecrets) {
	t.Helper()
	store := NewMemStore()
	secrets := providergateway.NewMemForgeSecrets()
	n := int64(0)
	svc, err := NewService(ServiceConfig{
		Connections: store, Snapshots: store, Secrets: secrets,
		// A COUNTER, not a clock. A test with a second clock goes red on the calendar alone.
		NowMS: func() int64 { n++; return n },
		IDFor: func(prefix string) string { n++; return fmt.Sprintf("%s-%d", prefix, n) },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return svc, store, secrets
}

func newTestBundleSource(t *testing.T, store *MemStore) *BundleSource {
	t.Helper()
	b, err := NewBundleSource(store, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	return b
}

func newTestGitSource(t *testing.T, store *MemStore, secrets *providergateway.MemForgeSecrets, run gitRunner) *GitSource {
	t.Helper()
	return newTestGitSourceWith(t, store, secrets, run, newTestBundleSource(t, store))
}

func newTestGitSourceWith(t *testing.T, store *MemStore, secrets *providergateway.MemForgeSecrets, run gitRunner, bundles *BundleSource) *GitSource {
	t.Helper()
	n := int64(0)
	g, err := NewGitSource(GitConfig{
		Connections: store, Snapshots: store, Secrets: secrets, Bundles: bundles,
		Scratch: t.TempDir(), Metrics: NewIngestMetrics(),
		NowMS: func() int64 { n++; return n },
		IDFor: func(prefix string) string { n++; return fmt.Sprintf("%s-%d", prefix, n) },
	})
	if err != nil {
		t.Fatalf("git source: %v", err)
	}
	g.runGit = run
	return g
}

// okClone is a git runner that writes a small repository into the clone directory.
//
// It stands in for the network, not for the guard: the tree it produces is walked by the REAL
// `InspectTree` and archived by the REAL `archiveTree`, so everything after the fetch is production
// code. Stubbing further in would make this a test of its own fake.
func okClone(t *testing.T) gitRunner {
	t.Helper()
	return func(_ context.Context, dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "checkout" {
			mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
			mustWrite(t, filepath.Join(dir, "src", "app.ts"), "export const x = 1\n")
		}
		return "", nil
	}
}

func refusingClone(_ context.Context, _ string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "fetch" {
		return "fatal: Authentication failed", fmt.Errorf("exit status 128")
	}
	return "", nil
}

// tinyArchive is a valid one-file gzipped tar, so a stored snapshot can actually be extracted.
func tinyArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := "package a\n"
	if err := tw.WriteHeader(&tar.Header{Name: "a.go", Typeflag: tar.TypeReg, Mode: 0o640, Size: int64(len(body))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// failingSnapshots is a SnapshotStore whose sweep always fails, for the escalation fence.
type failingSnapshots struct{}

func (failingSnapshots) PutDerived(context.Context, Ref, []byte, string, int64) error { return nil }
func (failingSnapshots) LiveSnapshot(context.Context, Ref, string, int64) (bool, error) {
	return false, nil
}
func (failingSnapshots) DeleteByConnection(context.Context, string, string) (int, error) {
	return 0, fmt.Errorf("the store is unreachable")
}
func (failingSnapshots) DeleteExpired(context.Context, int64) (int, error) {
	return 0, fmt.Errorf("the store is unreachable")
}

// providergatewayForgeSecretsType returns the ForgeSecrets interface type for the custody fence.
//
// A helper rather than an inline expression because `reflect.TypeOf((*T)(nil)).Elem()` on an interface
// is exactly the incantation somebody rewrites wrongly, and the fence it feeds is the one that must not
// quietly start inspecting the wrong type.
func providergatewayForgeSecretsType() reflect.Type {
	return reflect.TypeOf((*providergateway.ForgeSecrets)(nil)).Elem()
}
