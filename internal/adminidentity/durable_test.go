package adminidentity_test

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
)

// errWriter fails every write, so a test can assert the ORDER of the write-through rather than only
// that it happens.
type errWriter struct{ calls int }

var errPersist = errors.New("the database said no")

func (w *errWriter) PutPrincipal(adminidentity.Principal) error      { w.calls++; return errPersist }
func (w *errWriter) DisablePrincipal(string) error                   { w.calls++; return errPersist }
func (w *errWriter) EnrollFactor(adminidentity.EnrolledFactor) error { w.calls++; return errPersist }
func (w *errWriter) RecordSignCount(string, []byte, uint32) error    { w.calls++; return errPersist }

// recordingWriter accepts everything and remembers what it was asked to persist.
type recordingWriter struct {
	principals []adminidentity.Principal
	disabled   []string
	factors    []adminidentity.EnrolledFactor
}

func (w *recordingWriter) PutPrincipal(p adminidentity.Principal) error {
	w.principals = append(w.principals, p)
	return nil
}
func (w *recordingWriter) DisablePrincipal(id string) error {
	w.disabled = append(w.disabled, id)
	return nil
}
func (w *recordingWriter) EnrollFactor(f adminidentity.EnrolledFactor) error {
	w.factors = append(w.factors, f)
	return nil
}
func (w *recordingWriter) RecordSignCount(string, []byte, uint32) error { return nil }

// TestFailedPersistLeavesMemoryUntouched is the whole point of the write-through order.
//
// If memory were updated first, a failed persist would produce a principal who exists until the next
// restart and then does not — which is precisely the state the durable directory was added to remove,
// reintroduced in a form no health check reports. So the assertion is not "Put returned an error", it is
// "the store does not know about them either".
func TestFailedPersistLeavesMemoryUntouched(t *testing.T) {
	store := adminidentity.NewPrincipalStore()
	w := &errWriter{}
	if err := store.SetWriter(w); err != nil {
		t.Fatalf("SetWriter: %v", err)
	}
	err := store.Put(adminidentity.Principal{
		AdminID: "adm-superadmin", SSOSubject: "00uSUBJECT", Status: adminidentity.StatusActive,
		CreatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("Put succeeded against a writer that refused it")
	}
	if w.calls != 1 {
		t.Errorf("the writer was called %d times, want 1 — the durable write must be attempted", w.calls)
	}
	if _, ok := store.ByID("adm-superadmin"); ok {
		t.Error("the principal is in memory after the durable write failed — a restart would delete an " +
			"operator the process believes exists")
	}
	if _, err := store.BySubject("00uSUBJECT"); err == nil {
		t.Error("the subject index resolves after the durable write failed")
	}
}

// TestFailedFactorPersistLeavesMemoryUntouched is the same property on the enrolment directory, where
// the consequence is worse: an operator told "you are enrolled" who is locked out at the next restart.
func TestFailedFactorPersistLeavesMemoryUntouched(t *testing.T) {
	store := adminidentity.NewFactorStore()
	if err := store.SetWriter(&errWriter{}); err != nil {
		t.Fatalf("SetWriter: %v", err)
	}
	err := store.Enroll(adminidentity.EnrolledFactor{
		AdminID: "adm-superadmin", Kind: adminidentity.FactorTOTP,
		SecretName: adminidentity.TOTPSeedName("adm-superadmin"),
	})
	if err == nil {
		t.Fatal("Enroll succeeded against a writer that refused it")
	}
	if got := store.For("adm-superadmin"); len(got) != 0 {
		t.Errorf("the factor is in memory after the durable write failed: %d enrolled", len(got))
	}
}

// TestLoadDoesNotRePersist pins that a boot-time replay is a READ.
//
// Sending loaded rows back through Put/Enroll would re-write every row on every boot — and, worse, would
// make a replay indistinguishable from a write, so a read-only path would silently acquire the ability
// to mutate the directory.
func TestLoadDoesNotRePersist(t *testing.T) {
	w := &recordingWriter{}
	principals := adminidentity.NewPrincipalStore()
	if err := principals.SetWriter(w); err != nil {
		t.Fatalf("SetWriter: %v", err)
	}
	if err := adminidentity.LoadPrincipals(principals, []adminidentity.Principal{{
		AdminID: "adm-superadmin", SSOSubject: "00uSUBJECT", Status: adminidentity.StatusActive,
		CreatedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("LoadPrincipals: %v", err)
	}
	if len(w.principals) != 0 {
		t.Errorf("the replay persisted %d principal(s) — a boot-time load must not write", len(w.principals))
	}
	// …and the row is nonetheless readable, which is what the replay is for.
	if _, err := principals.BySubject("00uSUBJECT"); err != nil {
		t.Errorf("the replayed principal does not resolve by subject: %v", err)
	}

	factors := adminidentity.NewFactorStore()
	if err := factors.SetWriter(w); err != nil {
		t.Fatalf("SetWriter: %v", err)
	}
	if err := adminidentity.LoadFactors(factors, []adminidentity.EnrolledFactor{{
		AdminID: "adm-superadmin", Kind: adminidentity.FactorTOTP,
		SecretName: adminidentity.TOTPSeedName("adm-superadmin"),
	}}); err != nil {
		t.Fatalf("LoadFactors: %v", err)
	}
	if len(w.factors) != 0 {
		t.Errorf("the replay persisted %d factor(s)", len(w.factors))
	}
	if got := factors.For("adm-superadmin"); len(got) != 1 {
		t.Errorf("the replayed factor is not readable: %d enrolled", len(got))
	}
}

// TestSetWriterRefusesASecondBacking pins that a live store cannot be re-pointed.
//
// Swapping a backing mid-process would leave whatever the store already wrote in the previous one, with
// no place to notice — the directory would look complete and be split across two databases.
func TestSetWriterRefusesASecondBacking(t *testing.T) {
	store := adminidentity.NewPrincipalStore()
	if err := store.SetWriter(&recordingWriter{}); err != nil {
		t.Fatalf("first SetWriter: %v", err)
	}
	if err := store.SetWriter(&recordingWriter{}); err == nil {
		t.Error("a second durable backing was accepted")
	}
	if !store.Durable() {
		t.Error("Durable() is false after a writer was attached")
	}
}

// TestDurableIsFalseWithoutAWriter is what `adminlaunch` refuses a federated deployment on.
func TestDurableIsFalseWithoutAWriter(t *testing.T) {
	if adminidentity.NewPrincipalStore().Durable() {
		t.Error("a fresh principal store claims to be durable")
	}
	if adminidentity.NewFactorStore().Durable() {
		t.Error("a fresh factor store claims to be durable")
	}
}
