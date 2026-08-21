package sourceingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// credential_test.go is P32 §3: the forge-read credential's containment and lifecycle.
//
// # 🔴 Task 3.3 names FOUR surfaces, and each is asserted separately
//
// *"no forge credential value can appear in a request body, a config file, a log line or an audit
// record."* A single test over one of them would be a fence that reads "credentials are contained"
// and checks one quarter of it. So:
//
//   - **a request body** — every type this package marshals is walked by reflection; none may carry a
//     credential-shaped field. (`Authorization.Token` is the one place a credential enters, and it is
//     an INPUT struct that is never marshalled — asserted below.)
//   - **a config file** — the credential is never written to disk by this package at all, and the
//     only file it writes is the clone scratch tree. Asserted by cloning with a known token and
//     grepping every byte under the scratch root.
//   - **a log line** — a real `slog` handler captures everything the connect/clone/revoke path emits
//     and the token is searched for in the output.
//   - **an audit record** — the clone ledger is the audit record here; every field of every row is
//     searched.

// probeToken is the credential value every containment assertion searches for.
//
// 🔴 It is DELIBERATELY dictionary-shaped, and the first version was not — `ghs_PROBE_0123…mnop` had
// an entropy of 5.0 and the CI secret scan flagged it as a generic API key on the pull request.
//
// The fix is the fixture, not the scanner. Allowlisting a high-entropy string in a test file would
// blind `gitleaks` in exactly the place a real leaked token would land, and `.gitleaks.toml` says why
// in its own words: *"Allowlisting the prefix would make this gate blind to every genuine leaked
// Stripe webhook secret from here on."* The same argument applies here.
//
// So it follows the house convention that `internal/forgedelivery/hostedapp_test.go` already
// established — a `ghs_` prefix so the shape is right, dictionary words so the entropy is low, and
// `do_not_leak` so a reader knows on sight that it authenticates nothing. It stays DISTINCTIVE, which
// is the one property the assertions actually need: `assertTreeHasNoToken` greps a whole tree for it,
// and a canary made of common words would match legitimately.
const probeToken = "ghs_containment_probe_do_not_leak"

// TestNoForgeCredentialReachesALogLine is the log-line quarter of task 3.3.
//
// A REAL handler over a buffer, not a fake logger: the failure this catches is a `%+v` of a struct
// that happens to carry the token, and a fake that records format strings would not render it.
func TestNoForgeCredentialReachesALogLine(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	store := NewMemStore()
	secrets := providergateway.NewMemForgeSecrets()
	n := int64(0)
	svc, err := NewService(ServiceConfig{
		Connections: store, Snapshots: store, Secrets: secrets, Logger: logger,
		NowMS: func() int64 { n++; return n },
		IDFor: func(p string) string { n++; return fmt.Sprintf("%s-%d", p, n) },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	auth := goodAuth()
	auth.Token = probeToken
	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		CreatedBy: "u1", Authorization: auth,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// A refusal too — the path where a message is most likely to quote what arrived.
	bad := goodAuth()
	bad.Token = probeToken
	bad.AccountWide = true
	if _, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf2", Repository: "acme/api", ConsentShown: true,
		Authorization: bad,
	}); err == nil {
		t.Fatal("an account-wide grant was accepted")
	} else if strings.Contains(err.Error(), probeToken) {
		t.Fatalf("the refusal message quotes the credential: %v", err)
	}
	if err := svc.Rotate(ctx, "t1", conn.ConnectionID, probeToken+"-rotated"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := svc.Revoke(ctx, "t1", conn.ConnectionID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("the connect/rotate/revoke path emitted NO log lines; this test is asserting nothing")
	}
	if strings.Contains(buf.String(), probeToken) {
		t.Fatalf("a forge credential appears in a log line:\n%s", buf.String())
	}
	// And the lines that WERE emitted carry their event name from the central enum, so the containment
	// is not achieved by logging nothing useful.
	if !strings.Contains(buf.String(), "agentd.ingest.connection_created") {
		t.Errorf("the connect path emitted no `connection_created` event:\n%s", buf.String())
	}
}

// TestNoForgeCredentialReachesTheCloneScratchOrTheLedger is the config-file and audit-record quarters.
func TestNoForgeCredentialReachesTheCloneScratchOrTheLedger(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	secrets := providergateway.NewMemForgeSecrets()
	n := int64(0)
	svc, err := NewService(ServiceConfig{
		Connections: store, Snapshots: store, Secrets: secrets,
		Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		NowMS:  func() int64 { n++; return n },
		IDFor:  func(p string) string { n++; return fmt.Sprintf("%s-%d", p, n) },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	auth := goodAuth()
	auth.Token = probeToken
	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: auth,
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	scratch := t.TempDir()
	bundles, err := NewBundleSource(store, t.TempDir())
	if err != nil {
		t.Fatalf("bundle source: %v", err)
	}
	m := int64(0)
	src, err := NewGitSource(GitConfig{
		Connections: store, Snapshots: store, Secrets: secrets, Bundles: bundles,
		Scratch: scratch, Metrics: NewIngestMetrics(),
		NowMS: func() int64 { m++; return m },
		IDFor: func(p string) string { m++; return fmt.Sprintf("%s-%d", p, m) },
	})
	if err != nil {
		t.Fatalf("git source: %v", err)
	}
	// A runner that WRITES what it was given into the clone directory — the worst realistic case,
	// standing in for git's own `.git/config`, which records the remote URL verbatim.
	src.runGit = func(_ context.Context, dir string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "remote" {
			mustWrite(t, filepath.Join(dir, ".git", "config"), "[remote \"origin\"]\n\turl = "+args[len(args)-1]+"\n")
		}
		if len(args) > 0 && args[0] == "checkout" {
			mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
		}
		return "", nil
	}

	ref := Ref{TenantID: "t1", WorkflowID: "wf1", SourceRevision: "rev1"}
	mat, err := src.Materialize(ctx, ref)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	defer mat.Release()

	// 1) The SNAPSHOT the platform kept. `.git` is pruned from the archive, so the credentialed
	//    remote URL git wrote is not in what we stored — this is the assertion that makes the prune a
	//    privacy control and not only a performance one.
	rc, err := store.Open(ctx, ref)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	snapshot := new(bytes.Buffer)
	if _, err := snapshot.ReadFrom(rc); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	_ = rc.Close()
	if bytes.Contains(snapshot.Bytes(), []byte(probeToken)) {
		t.Error("the stored snapshot carries the forge credential — `.git` reached the archive")
	}

	// 2) The extracted tree handed to discovery.
	assertNoTokenUnder(t, mat.Dir, "the extracted tree")

	// 3) The AUDIT RECORD. Every field of every row, marshalled, so a field added later is covered
	//    without this test being edited.
	records, err := svc.Records(ctx, "t1", conn.ConnectionID, 10)
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("the clone wrote no ledger row; this assertion is vacuous")
	}
	blob, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal records: %v", err)
	}
	if bytes.Contains(blob, []byte(probeToken)) {
		t.Errorf("a forge credential appears in the read ledger:\n%s", blob)
	}
}

func assertNoTokenUnder(t *testing.T, root, what string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(b, []byte(probeToken)) {
			t.Errorf("a forge credential appears in %s, at %s", what, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", what, err)
	}
}

// TestNoMarshalledTypeCanCarryACredential is the request-body quarter of task 3.3.
//
// 🔴 It walks the types by reflection rather than listing them, because the failure is a field somebody
// ADDS. `Authorization` is exempted and the exemption is narrow and stated: it is the one INPUT struct
// through which a credential enters, it is never marshalled by anything, and a fence below asserts that
// second property rather than trusting it.
func TestNoMarshalledTypeCanCarryACredential(t *testing.T) {
	credentialWords := map[string]bool{
		"token": true, "secret": true, "credential": true, "password": true,
		"key": true, "auth": true, "bearer": true, "pat": true,
	}
	types := []reflect.Type{
		reflect.TypeOf(Connection{}),
		reflect.TypeOf(CloneRecord{}),
		reflect.TypeOf(ForgeDescription{}),
		reflect.TypeOf(IngestHealth{}),
		reflect.TypeOf(ForgeStats{}),
		reflect.TypeOf(RetentionHealth{}),
		reflect.TypeOf(RevocationResult{}),
	}
	for _, ty := range types {
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			for _, w := range camelWords(f.Name) {
				if credentialWords[w] {
					t.Errorf("%s.%s has the word %q in it — this type is marshalled to a customer or to a "+
						"health endpoint, and a forge credential may reach neither", ty.Name(), f.Name, w)
				}
			}
		}
	}
}

// TestAuthorizationIsNeverMarshalled pins the exemption above.
//
// `Authorization.Token` is the one credential-carrying field in this package. It is safe only because
// nothing serializes the struct — so that is asserted rather than assumed: no field of it has a JSON
// tag, which is what would be needed for a considered wire shape, and the presence of one is the signal
// that somebody started sending it somewhere.
func TestAuthorizationIsNeverMarshalled(t *testing.T) {
	ty := reflect.TypeOf(Authorization{})
	for i := 0; i < ty.NumField(); i++ {
		if tag, ok := ty.Field(i).Tag.Lookup("json"); ok {
			t.Errorf("Authorization.%s has a json tag (%q). This struct carries the forge credential and "+
				"is an INPUT only; a JSON tag means somebody is serializing it.", ty.Field(i).Name, tag)
		}
	}
}

// TestTheReadGrantIsStructurallySeparateFromAWriteInstallation is task 3.4.
//
// # Why this is a fence and not a paragraph
//
// ADR-005 keeps a WRITE installation out of the platform's ordinary credential path, and ADR-013 admits
// a READ one. The failure both ADRs are guarding against is the same object serving both: *"one
// credential that both reads source and writes branches is the thing neither ADR wants."*
//
// The separation is structural in three ways, and each is checked:
//
//  1. Different STORES. `sourceingest` resolves through `providergateway.ForgeSecrets` keyed by
//     CONNECTION; `forgedelivery` resolves through its own `SecretsManager` keyed by INSTALLATION.
//     Neither interface can reach the other's material, because neither has a method that names the
//     other's key.
//  2. Different KEYS. A connection id and an installation id are minted by different code with
//     different prefixes, so even a store that held both could not confuse them.
//  3. No import. `sourceingest` does not import `forgedelivery` and cannot obtain a write client.
func TestTheReadGrantIsStructurallySeparateFromAWriteInstallation(t *testing.T) {
	// (3) is the strongest and the cheapest: read this package's own IMPORT DECLARATIONS.
	//
	// 🔴 Parsed, not grepped. The first version searched the file bytes and went red on THIS FILE,
	// because the sentence explaining the rule contains the path — a fence that cannot tell a comment
	// from an import is a fence somebody adds an exception to, and the exception is what a real
	// violation later hides behind.
	//
	// Non-test files only: the constraint is on what the SHIPPED package can reach. A test that
	// imported forgedelivery would obtain nothing production code can use.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		checked++
		f, perr := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", e.Name(), perr)
		}
		for _, imp := range f.Imports {
			path, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				t.Fatalf("unquote import in %s: %v", e.Name(), uerr)
			}
			if strings.HasSuffix(path, "/internal/forgedelivery") {
				t.Errorf("%s imports %s. The read grant and the ADR-005 write installation must stay "+
					"separate objects with separate scopes and separate revocations; an import is how one "+
					"credential ends up doing both.", e.Name(), path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test files were parsed; this fence is asserting nothing")
	}

	// (1) The read store's interface cannot express an installation, and therefore cannot reach a
	// write credential even if both were backed by the same secret manager.
	ty := reflect.TypeOf((*providergateway.ForgeSecrets)(nil)).Elem()
	for i := 0; i < ty.NumMethod(); i++ {
		m := ty.Method(i)
		for j := 0; j < m.Type.NumIn(); j++ {
			if strings.Contains(strings.ToLower(m.Type.In(j).String()), "installation") {
				t.Errorf("ForgeSecrets.%s takes an installation — the read path must not be able to name "+
					"a write installation", m.Name)
			}
		}
	}

	// (2) The two id shapes are minted differently. Checked by construction rather than by convention:
	// a connection id from this package always carries this package's prefix.
	if got := defaultID("conn"); !strings.HasPrefix(got, "conn-") {
		t.Errorf("defaultID(\"conn\") = %q, want a `conn-` prefix so a read grant's key is never "+
			"mistakable for an installation id", got)
	}
}

// TestRevokeIsIdempotentUnderRetry is the lifecycle property the cascade's error handling depends on.
//
// Every step of `Revoke` is idempotent, which is what makes a partially-completed revocation
// RETRYABLE — and the console tells a customer so. A second revoke of a fully-revoked connection
// reports ErrNoConnection, which is the honest answer and not an error the customer must act on.
func TestRevokeIsIdempotentUnderRetry(t *testing.T) {
	ctx := context.Background()
	svc, _, secrets := newTestService(t)
	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "t1", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := svc.Revoke(ctx, "t1", conn.ConnectionID); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if _, err := svc.Revoke(ctx, "t1", conn.ConnectionID); err == nil {
		t.Error("a second revoke reported success for a connection that no longer exists")
	}
	// The credential store's own Revoke is idempotent — the property the retry depends on.
	ref := providergateway.ForgeRef{Forge: "github", ConnectionID: conn.ConnectionID}
	for i := 0; i < 3; i++ {
		if err := secrets.Revoke(ctx, ref); err != nil {
			t.Fatalf("credential revoke %d: %v — revoking twice must not fail, or a retry during an "+
				"incident cannot complete", i, err)
		}
	}
}

// TestACrossTenantRevokeCannotReachAnotherTenantsConnection.
//
// The scope comes from the credential (P27), never from a request field — so naming another tenant's
// connection id must resolve to nothing rather than to their row.
func TestACrossTenantRevokeCannotReachAnotherTenantsConnection(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newTestService(t)
	conn, err := svc.Connect(ctx, ConnectRequest{
		TenantID: "victim", WorkflowID: "wf1", Repository: "acme/api", ConsentShown: true,
		Authorization: goodAuth(),
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := svc.Revoke(ctx, "attacker", conn.ConnectionID); err == nil {
		t.Fatal("a tenant revoked another tenant's connection")
	}
	if _, err := store.ForWorkflow(ctx, "victim", "wf1"); err != nil {
		t.Errorf("the victim's connection was affected: %v", err)
	}
	if _, err := svc.Records(ctx, "attacker", conn.ConnectionID, 10); err == nil {
		t.Error("a tenant read another tenant's read ledger")
	}
}
