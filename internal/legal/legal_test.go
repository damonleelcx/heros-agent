package legal

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// legal_test.go covers the domain rules that decide whether a consent record is worth anything:
// server-side hash validation, the material/non-material asymmetry, and the two refusals the retention
// job makes.
//
// The storage-level guarantees — idempotency under a real unique constraint, append-only enforcement —
// are proved against a live Postgres in `store_pgproof_test.go`, because asserting them against an
// in-memory fake would be asserting them against the fake.

func manifest() Manifest {
	return Manifest{
		Schema: "heros.legal-manifest/v1",
		Kinds: map[Kind][]Version{
			KindTerms: {
				{Version: "1.1.0", EffectiveDate: "2026-08-01", Hash: strings.Repeat("b", 64), Route: "/legal/terms/v/1.1.0", Material: true, Supersedes: "1.0.0", Current: true},
				{Version: "1.0.0", EffectiveDate: "2026-07-31", Hash: strings.Repeat("a", 64), Route: "/legal/terms/v/1.0.0", Material: true, Supersedes: "none"},
			},
			KindPrivacy: {
				{Version: "1.0.0", EffectiveDate: "2026-07-31", Hash: strings.Repeat("c", 64), Route: "/legal/privacy/v/1.0.0", Material: true, Supersedes: "none", Current: true},
			},
		},
	}
}

// ── The load-bearing check ────────────────────────────────────────────────────

func TestValidateRefusesAHashTheServerDidNotPublish(t *testing.T) {
	// 🔴 Without this, the consent record says whatever the browser said and its audit value is zero.
	_, err := manifest().Validate(KindTerms, "1.0.0", strings.Repeat("f", 64))
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("a hash the server never published was accepted: %v", err)
	}
	// The message must not echo the full hashes: a refusal travels into logs, and a log line carrying
	// both values makes the comparison somebody else's to make.
	if strings.Contains(err.Error(), strings.Repeat("f", 64)) {
		t.Error("the refusal echoed the full submitted hash into its message")
	}
}

func TestValidateAcceptsThePublishedTriple(t *testing.T) {
	v, err := manifest().Validate(KindTerms, "1.0.0", strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("the published triple was refused: %v", err)
	}
	if v.Version != "1.0.0" {
		t.Errorf("resolved %q, want 1.0.0", v.Version)
	}
}

func TestValidateKeepsItsThreeRefusalsApart(t *testing.T) {
	// "No such kind", "no such version" and "wrong hash for a real version" are three different
	// situations. The first two are a stale or malformed client; the third means somebody was shown
	// different text from the one they are accepting. Collapsing them would hide the only one that
	// matters.
	m := manifest()
	if _, err := m.Validate(Kind("marketing"), "1.0.0", strings.Repeat("a", 64)); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("an unknown kind did not produce ErrUnknownKind: %v", err)
	}
	if _, err := m.Validate(KindTerms, "9.9.9", strings.Repeat("a", 64)); !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("an unknown version did not produce ErrUnknownVersion: %v", err)
	}
}

// ── Which version is in force ─────────────────────────────────────────────────

func TestCurrentPrefersTheManifestsOwnStatement(t *testing.T) {
	v, err := manifest().Current(KindTerms, "2026-07-31")
	if err != nil {
		t.Fatal(err)
	}
	// The manifest marks 1.1.0 current even though its effective date is tomorrow relative to `asOf`.
	// The manifest comes from the same render pass that produced the pages, so it cannot disagree with
	// what a reader saw — and preferring it is what keeps the record and the page aligned.
	if v.Version != "1.1.0" {
		t.Errorf("current is %q, want the manifest's own answer 1.1.0", v.Version)
	}
}

func TestCurrentFallsBackToTheDateWhenTheManifestIsSilent(t *testing.T) {
	m := manifest()
	terms := append([]Version(nil), m.Kinds[KindTerms]...)
	for i := range terms {
		terms[i].Current = false
	}
	m.Kinds[KindTerms] = terms

	v, err := m.Current(KindTerms, "2026-07-31")
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "1.0.0" {
		t.Errorf("current is %q; on 2026-07-31 only 1.0.0 is effective", v.Version)
	}
}

func TestCurrentOrdersVersionsNumericallyNotAsStrings(t *testing.T) {
	// String order puts 1.10.0 before 1.9.0. This is the bug the comparison exists to not have, and it
	// only appears on the tenth minor version — long after anybody is looking.
	m := Manifest{Kinds: map[Kind][]Version{
		KindTerms: {
			{Version: "1.9.0", EffectiveDate: "2026-01-01", Hash: strings.Repeat("a", 64)},
			{Version: "1.10.0", EffectiveDate: "2026-02-01", Hash: strings.Repeat("b", 64)},
		},
	}}
	v, err := m.Current(KindTerms, "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "1.10.0" {
		t.Errorf("current is %q, want 1.10.0 — versions must order numerically", v.Version)
	}
}

// ── Pending, and the material / non-material asymmetry ────────────────────────

func accepted(kind Kind, version, hash string, superseded string) Acceptance {
	return Acceptance{
		TenantID: "t", PrincipalID: "p", DocumentKind: kind, DocumentVersion: version,
		ContentHash: hash, AcceptedAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		Method: MethodSignIn, SupersededBy: superseded,
	}
}

func TestPendingIsEmptyWhenTheCurrentVersionIsAccepted(t *testing.T) {
	m := manifest()
	pending := m.Pending([]Acceptance{
		accepted(KindTerms, "1.1.0", strings.Repeat("b", 64), ""),
		accepted(KindPrivacy, "1.0.0", strings.Repeat("c", 64), ""),
	}, "2026-08-01")
	if len(pending) != 0 {
		t.Errorf("nothing should be pending, got %d", len(pending))
	}
}

func TestASupersededAcceptanceLeavesTheKindPending(t *testing.T) {
	// This is a MATERIAL publication arriving: the prior acceptance was marked superseded, so the
	// principal is asked again.
	m := manifest()
	pending := m.Pending([]Acceptance{
		accepted(KindTerms, "1.0.0", strings.Repeat("a", 64), "some-uuid"),
		accepted(KindPrivacy, "1.0.0", strings.Repeat("c", 64), ""),
	}, "2026-08-01")
	if len(pending) != 1 || pending[0].Version != "1.1.0" {
		t.Fatalf("terms should be pending at 1.1.0, got %+v", pending)
	}
}

func TestANonMaterialPublicationAsksNothingOfAnybody(t *testing.T) {
	// 🔴 Decision 3, at the point where it bites. A typo fix publishes a new version and marks NOTHING
	// superseded — so a principal who accepted the previous one is not interrupted.
	//
	// The asymmetry is asserted by the SERVICE (PublishSupersession), because that is where it lives;
	// here we assert the consequence: an unsuperseded acceptance of the version still marked current
	// leaves nothing pending.
	m := manifest()
	pending := m.Pending([]Acceptance{
		accepted(KindTerms, "1.1.0", strings.Repeat("b", 64), ""),
		accepted(KindPrivacy, "1.0.0", strings.Repeat("c", 64), ""),
	}, "2026-08-01")
	if len(pending) != 0 {
		t.Errorf("a non-material publication interrupted somebody: %+v", pending)
	}
}

func TestAKindWithNoPublishedVersionIsNotPending(t *testing.T) {
	// Nothing to accept is not the same as something outstanding. Reporting it as pending would produce
	// a gate nobody can clear.
	m := Manifest{Kinds: map[Kind][]Version{
		KindTerms: {{Version: "1.0.0", EffectiveDate: "2026-07-31", Hash: strings.Repeat("a", 64), Current: true}},
	}}
	pending := m.Pending([]Acceptance{accepted(KindTerms, "1.0.0", strings.Repeat("a", 64), "")}, "2026-07-31")
	if len(pending) != 0 {
		t.Errorf("privacy has no published version and must not be pending: %+v", pending)
	}
}

// ── The service ───────────────────────────────────────────────────────────────

type fakeStore struct {
	rows      []Acceptance
	insertErr error
	deleted   int
	erased    int
	superseded int
}

func (f *fakeStore) Insert(_ context.Context, a Acceptance) (Acceptance, bool, error) {
	if f.insertErr != nil {
		return Acceptance{}, false, f.insertErr
	}
	for _, r := range f.rows {
		if r.TenantID == a.TenantID && r.PrincipalID == a.PrincipalID &&
			r.DocumentKind == a.DocumentKind && r.DocumentVersion == a.DocumentVersion {
			return r, false, nil
		}
	}
	f.rows = append(f.rows, a)
	return a, true, nil
}

func (f *fakeStore) ListForPrincipal(_ context.Context, tenantID, principalID string) ([]Acceptance, error) {
	var out []Acceptance
	for _, r := range f.rows {
		if r.TenantID == tenantID && r.PrincipalID == principalID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) MarkSuperseded(context.Context, Kind, string, string) (int, error) {
	f.superseded++
	return 1, nil
}

func (f *fakeStore) DeleteOlderThan(context.Context, time.Time) (int, error) {
	f.deleted++
	return 3, nil
}

func (f *fakeStore) EraseSubject(_ context.Context, tenantID, principalID string, at time.Time) (int, error) {
	n := 0
	for i := range f.rows {
		if f.rows[i].TenantID == tenantID && f.rows[i].PrincipalID == principalID {
			t := at
			f.rows[i].SubjectErasedAt = &t
			n++
		}
	}
	f.erased = n
	return n, nil
}

type failingSource struct{ err error }

func (f failingSource) Manifest(context.Context) (Manifest, error) { return Manifest{}, f.err }

func newService(store Store, src ManifestSource) *Service {
	fixed := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	n := 0
	return NewService(store, src, func() time.Time { return fixed }, func() string {
		n++
		return "id-" + string(rune('a'+n-1))
	})
}

func TestRecordStoresThePublishedHashNotTheSubmittedOne(t *testing.T) {
	store := &fakeStore{}
	svc := newService(store, StaticManifestSource{M: manifest()})
	// Submitted uppercase; the published value is lowercase. They compare equal, and the SERVER's copy
	// is what must land in the evidence.
	got, created, err := svc.Record(context.Background(), "t", "p", Request{
		DocumentKind: KindTerms, DocumentVersion: "1.0.0",
		ContentHash: strings.ToUpper(strings.Repeat("a", 64)), Method: MethodSignIn,
	})
	if err != nil || !created {
		t.Fatalf("record failed: %v (created=%v)", err, created)
	}
	if got.ContentHash != strings.Repeat("a", 64) {
		t.Errorf("stored %q, want the server's published lowercase hash", got.ContentHash)
	}
}

func TestRecordIsIdempotentAndReportsWhichBranchItTook(t *testing.T) {
	store := &fakeStore{}
	svc := newService(store, StaticManifestSource{M: manifest()})
	req := Request{DocumentKind: KindTerms, DocumentVersion: "1.0.0", ContentHash: strings.Repeat("a", 64), Method: MethodSignIn}

	if _, created, err := svc.Record(context.Background(), "t", "p", req); err != nil || !created {
		t.Fatalf("first acceptance: err=%v created=%v", err, created)
	}
	_, created, err := svc.Record(context.Background(), "t", "p", req)
	if err != nil {
		t.Fatalf("a repeat must SUCCEED, not error: %v", err)
	}
	if created {
		t.Error("a repeat reported created=true — a double-click would read as two decisions")
	}
	if len(store.rows) != 1 {
		t.Errorf("%d rows after a double submit, want 1", len(store.rows))
	}
}

func TestRecordFailsClosedWhenTheManifestCannotBeRead(t *testing.T) {
	// 🔴 If we cannot check what was published, we cannot record what was accepted. Recording it
	// unchecked would produce exactly the row whose audit value is zero.
	svc := newService(&fakeStore{}, failingSource{err: errors.New("console unreachable")})
	_, _, err := svc.Record(context.Background(), "t", "p", Request{
		DocumentKind: KindTerms, DocumentVersion: "1.0.0", ContentHash: strings.Repeat("a", 64), Method: MethodSignIn,
	})
	if !errors.Is(err, ErrManifestUnavailable) {
		t.Fatalf("an unreadable manifest did not fail closed: %v", err)
	}
}

func TestAFailedWriteIsNeverReportedAsRecorded(t *testing.T) {
	store := &fakeStore{insertErr: errors.New("connection reset")}
	svc := newService(store, StaticManifestSource{M: manifest()})
	got, created, err := svc.Record(context.Background(), "t", "p", Request{
		DocumentKind: KindTerms, DocumentVersion: "1.0.0", ContentHash: strings.Repeat("a", 64), Method: MethodSignIn,
	})
	if err == nil {
		t.Fatal("a failed write returned success — this is the failure the whole phase exists to prevent")
	}
	if created || got.ID != "" {
		t.Errorf("a failed write returned a populated acceptance: %+v (created=%v)", got, created)
	}
}

func TestReadIsFailOpenAndSaysWhenPendingIsUnknown(t *testing.T) {
	// Reading is fail-OPEN (Decision 4): the customer's own history is real and is returned. But
	// `Pending` must stay nil rather than empty — an empty list would silently clear the gate.
	store := &fakeStore{rows: []Acceptance{accepted(KindTerms, "1.0.0", strings.Repeat("a", 64), "")}}
	svc := newService(store, failingSource{err: errors.New("console unreachable")})
	history, err := svc.Read(context.Background(), "t", "p")
	if !errors.Is(err, ErrManifestUnavailable) {
		t.Fatalf("want ErrManifestUnavailable, got %v", err)
	}
	if len(history.Accepted) != 1 {
		t.Errorf("the history was withheld: %+v", history.Accepted)
	}
	if history.Pending != nil {
		t.Errorf("pending must be nil when unknown, got %+v", history.Pending)
	}
}

func TestPublishSupersessionActsOnlyOnAMaterialPublication(t *testing.T) {
	m := manifest()
	// 1.1.0 is declared material.
	store := &fakeStore{}
	svc := newService(store, StaticManifestSource{M: m})
	if _, err := svc.PublishSupersession(context.Background(), KindTerms, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if store.superseded != 1 {
		t.Error("a material publication did not supersede prior acceptances")
	}

	// Now the same call for a NON-material version must touch nothing.
	m.Kinds[KindTerms] = []Version{
		{Version: "1.1.1", EffectiveDate: "2026-08-02", Hash: strings.Repeat("d", 64), Material: false, Current: true},
	}
	store2 := &fakeStore{}
	svc2 := newService(store2, StaticManifestSource{M: m})
	n, err := svc2.PublishSupersession(context.Background(), KindTerms, "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || store2.superseded != 0 {
		t.Errorf("a non-material publication interrupted customers (n=%d, calls=%d)", n, store2.superseded)
	}
}

// ── The retention job's two refusals ──────────────────────────────────────────

func TestRetentionDeletesNothingWhenNoWindowIsConfigured(t *testing.T) {
	// 🔴 It does NOT fall back to a default. The window is a legal answer, not an engineering one, and a
	// deletion job that invents one deletes the wrong things confidently.
	store := &fakeStore{}
	svc := newService(store, StaticManifestSource{M: manifest()})
	res, err := svc.RetentionJob(context.Background(), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused == "" {
		t.Error("an unset window did not produce a stated refusal")
	}
	if store.deleted != 0 {
		t.Error("an unset window still reached the delete path")
	}
}

func TestRetentionDryRunNeverReachesTheDeletePath(t *testing.T) {
	// A deletion job whose first production run is also its first run ever is a defect waiting for a
	// quiet weekend. The dry run must be a mode, not a flag somebody remembers to pass.
	store := &fakeStore{}
	svc := newService(store, StaticManifestSource{M: manifest()})
	res, err := svc.RetentionJob(context.Background(), 7*24*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Removed != 0 {
		t.Errorf("a dry run reported a deletion: %+v", res)
	}
	if store.deleted != 0 {
		t.Error("a dry run called DeleteOlderThan")
	}
	if res.Cutoff.IsZero() {
		t.Error("a dry run must still report the cutoff it would have used")
	}
}

func TestRetentionRunsWithAConfiguredWindow(t *testing.T) {
	store := &fakeStore{}
	svc := newService(store, StaticManifestSource{M: manifest()})
	res, err := svc.RetentionJob(context.Background(), 7*365*24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 3 || store.deleted != 1 {
		t.Errorf("the live run did not delete: %+v", res)
	}
}

// ── Erasure keeps the evidence ────────────────────────────────────────────────

func TestErasureTombstonesTheSubjectAndKeepsTheEvidentiaryRow(t *testing.T) {
	store := &fakeStore{rows: []Acceptance{accepted(KindTerms, "1.0.0", strings.Repeat("a", 64), "")}}
	svc := newService(store, StaticManifestSource{M: manifest()})
	n, err := svc.EraseSubject(context.Background(), "t", "p")
	if err != nil || n != 1 {
		t.Fatalf("erase: n=%d err=%v", n, err)
	}
	row := store.rows[0]
	if !row.Erased() {
		t.Fatal("the subject was not tombstoned")
	}
	// The evidence survives, in full.
	if row.DocumentKind != KindTerms || row.DocumentVersion != "1.0.0" ||
		row.ContentHash != strings.Repeat("a", 64) || row.AcceptedAt.IsZero() {
		t.Errorf("erasure destroyed evidence: %+v", row)
	}
}

func TestTheAcceptanceRecordHasNowhereToPutPersonalData(t *testing.T) {
	// NFR9 asserted as a property of the TYPE, not as a habit.
	//
	// The migration has no email column and no free-text column, and this is the Go half of the same
	// rule. If somebody adds `Email`, `Name` or `Note` to Acceptance, this fails — which is the point at
	// which the conversation happens, rather than after the first erasure request, when the pressure
	// runs the other way.
	//
	// The check is on SUBSTRINGS rather than exact names, because the field that would actually get
	// added is `UserEmail` or `ContactName`, not `Email`.
	forbidden := []string{"email", "name", "address", "phone", "useragent", "ip", "note", "comment", "reason", "text"}
	// PrincipalID is the one identifier there is, and it is opaque by constraint. Allowed by exception,
	// named so the exception is visible.
	allowed := map[string]bool{"PrincipalID": true, "TenantID": true, "ID": true, "SupersededBy": true}

	typ := reflect.TypeOf(Acceptance{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i).Name
		if allowed[field] {
			continue
		}
		lower := strings.ToLower(field)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf(
					"Acceptance has a field %q, which looks like personal data. The consent row holds no "+
						"email, no name and no free text — that is what makes erasure a tombstone of the "+
						"subject rather than a choice between destroying evidence and retaining personal data.",
					field,
				)
			}
		}
	}
}
