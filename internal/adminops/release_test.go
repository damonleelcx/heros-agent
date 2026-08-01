package adminops_test

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/release"
)

// release_test.go covers P26 wave 26c — release and trust oversight.
//
// Two assertions here are worth more than the rest put together:
// TestNoKeyMaterialReachesTheReleaseSurface, because a signing key has already leaked once in this
// project by being emitted into a transcript; and TestAQueuedSmokeRunIsNotRenderedAsAFailure, because
// a retired runner label queues rather than failing, and reading that as *failed* sends an engineer to
// debug a build that never ran.

// testReleases is a publish record with all three smoke outcomes and one artefact signed by a key that
// has since been retired.
type testReleases struct{}

func (testReleases) Describe() string { return "test publish record" }

func (testReleases) Releases() []adminops.ReleaseRecord {
	return []adminops.ReleaseRecord{
		{
			Version: "v1.0.0", Channel: "github-release", PublishedAt: "2026-07-30T00:00:00Z",
			SigningKeyID: "heros-release-2026c",
			Artefacts: []adminops.ArtefactRecord{
				{Platform: "linux/amd64", Name: "a.tar.gz", Published: true,
					Verification: adminops.VerifyVerified, Smoke: adminops.SmokePassed},
				{Platform: "darwin/arm64", Name: "b.tar.gz", Published: true,
					Verification: adminops.VerifyVerified, Smoke: adminops.SmokeQueuedUntilTimeout,
					SmokeDetail: "runner label macos-13 was retired"},
			},
		},
		{
			Version: "v1.0.1", Channel: "github-release", PublishedAt: "2026-07-31T00:00:00Z",
			SigningKeyID: "heros-release-2026c",
			Artefacts: []adminops.ArtefactRecord{
				{Platform: "linux/amd64", Name: "c.tar.gz", Published: true,
					Verification: adminops.VerifyVerified, Smoke: adminops.SmokeFailed},
				{Platform: "darwin/arm64", Name: "d.tar.gz", Published: false},
			},
		},
		{
			Version: "v0.9.0-rc.1", Channel: "github-release", PublishedAt: "2026-07-29T00:00:00Z",
			SigningKeyID: "heros-release-2026b",
			Artefacts: []adminops.ArtefactRecord{
				{Platform: "linux/amd64", Name: "e.tar.gz", Published: true, Verification: adminops.VerifyNotYet},
			},
		},
	}
}

func releaseView(t *testing.T, h *harness) adminops.ReleaseView {
	t.Helper()
	svc, err := adminops.NewReleaseService(h.exec, testReleases{})
	if err != nil {
		t.Fatalf("NewReleaseService: %v", err)
	}
	view, err := svc.View(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	return view
}

// TestNoKeyMaterialReachesTheReleaseSurface defends P26 task 4.4 and the requirement "No key material
// SHALL appear on any surface, and no operation SHALL produce it".
//
// 🔴 It scans the SERIALISED read model rather than checking that each field is named safely. A field
// added later called `public_key` would pass a field-by-field review and fail here. A signing key has
// already leaked once in this project by being printed to a terminal, and the lesson recorded then was
// that a key printed anywhere is a key in every log that place wrote to.
func TestNoKeyMaterialReachesTheReleaseSurface(t *testing.T) {
	h := newHarness(t)
	view := releaseView(t, h)

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(encoded)

	// The live trust root's actual public keys must appear NOWHERE, in full or in any long prefix.
	for _, k := range release.TrustRoot() {
		if k.Hex == "" {
			continue
		}
		if strings.Contains(body, k.Hex) {
			t.Fatalf("the release read model contains the full public key of %s", k.ID)
		}
		// Even a long prefix is a key-shaped blob on a screen. 32 hex characters is well past an
		// identifier and well into "somebody pasted a key".
		if len(k.Hex) >= 32 && strings.Contains(body, k.Hex[:32]) {
			t.Fatalf("the release read model contains a 32-character prefix of %s's public key", k.ID)
		}
	}
	// And nothing key-shaped at all: no run of 40+ hex characters anywhere in the model. A fingerprint
	// is 16; an identifier is words. Anything longer is a blob.
	if m := regexp.MustCompile(`[0-9a-f]{40,}`).FindString(body); m != "" {
		t.Fatalf("the release read model contains a %d-character hex blob (%s…) — a key is an "+
			"identifier and a fingerprint, never material", len(m), m[:16])
	}
	// PEM and OpenSSH shapes, in case a later change carries a key in a different encoding.
	for _, marker := range []string{"BEGIN ", "PRIVATE KEY", "ssh-ed25519", "-----"} {
		if strings.Contains(body, marker) {
			t.Fatalf("the release read model contains %q — a key encoding reached the surface", marker)
		}
	}

	// The key rows still IDENTIFY every key, or the assertion above would be satisfied by showing
	// nothing at all.
	if len(view.Keys) == 0 {
		t.Fatal("the surface shows no keys — 'no key material' must not be met by showing no keys")
	}
	var sawActive, sawRetired bool
	for _, k := range view.Keys {
		if k.ID == "" || k.Fingerprint == "" {
			t.Fatalf("key row %+v has no identifier or no fingerprint", k)
		}
		if k.Role == "active" {
			sawActive = true
		}
		if k.Role == "retired" {
			sawRetired = true
			if k.RetiredAt == "" {
				t.Fatalf("retired key %s has no rotation date", k.ID)
			}
			if k.Note == "" {
				t.Fatalf("retired key %s has no recorded reason — a key that left the trust root with no "+
					"reason is indistinguishable from one somebody deleted by accident", k.ID)
			}
		}
	}
	if !sawActive || !sawRetired {
		t.Fatalf("the surface does not show both the active key and the retired ones: active=%v retired=%v",
			sawActive, sawRetired)
	}
}

// TestTheReleaseSurfaceOffersNoOperationThatProducesKeyMaterial defends the second half of 4.4 — and
// task 4.9, that this surface halts, unpublishes and changes nothing.
func TestTheReleaseSurfaceOffersNoOperationThatProducesKeyMaterial(t *testing.T) {
	allowed := map[string]bool{"View": true}
	typ := reflect.TypeOf(&adminops.ReleaseService{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !allowed[name] {
			t.Fatalf("ReleaseService exposes %q. This surface reads: no method may halt a channel, "+
				"unpublish an artefact, re-sign, re-publish, re-run a smoke job, or generate or export "+
				"key material (P26 §4.4, §4.9).", name)
		}
	}
	h := newHarness(t)
	if !releaseView(t, h).ReadOnly {
		t.Fatal("the release read model does not declare itself read-only")
	}
}

// TestAQueuedSmokeRunIsNotRenderedAsAFailure defends P26 task 4.6.
//
// 🔴 A retired GitHub runner label queues until the workflow times out rather than failing — measured
// in P20 with `macos-13`. If this fails, the surface is telling an engineer to debug a build that
// never ran.
func TestAQueuedSmokeRunIsNotRenderedAsAFailure(t *testing.T) {
	if got := len(adminops.SmokeStates()); got != 3 {
		t.Fatalf("SmokeStates() has %d values, want 3", got)
	}
	h := newHarness(t)
	view := releaseView(t, h)

	var queued, failed int
	for _, a := range view.Artefacts {
		switch a.Smoke {
		case adminops.SmokeQueuedUntilTimeout:
			queued++
		case adminops.SmokeFailed:
			failed++
		}
	}
	if queued != 1 {
		t.Fatalf("the queued-until-timeout artefact reads as %d rows in that state, want 1 — it has been "+
			"collapsed into another outcome", queued)
	}
	if failed != 1 {
		t.Fatalf("failed smoke rows = %d, want exactly 1 — a queued run has been counted as a failure", failed)
	}
	// And the sequence's REASON has to say the job never started, or an operator reading the stopping
	// point still goes to the build.
	for _, s := range view.Sequences {
		if s.Version != "v1.0.0" {
			continue
		}
		if s.StoppedAt != adminops.StepSmoke {
			t.Fatalf("v1.0.0's sequence stopped at %q, want %q", s.StoppedAt, adminops.StepSmoke)
		}
		if !strings.Contains(s.Reason, "never started") {
			t.Fatalf("the queued-until-timeout reason does not say the job never started: %q", s.Reason)
		}
		// It must not ASSERT a failure. The word may appear while denying one ("this is not a failed
		// build"), which is the sentence an engineer actually needs; what must not appear is the
		// failure phrasing the genuinely-failed rows use.
		if strings.Contains(s.Reason, "FAILED") {
			t.Fatalf("the queued-until-timeout reason is phrased as a failure: %q", s.Reason)
		}
		if !strings.Contains(s.Reason, "not a failed build") {
			t.Fatalf("the queued-until-timeout reason does not tell the reader it is not a failed build: %q", s.Reason)
		}
	}
}

// TestArtefactVerificationHasThreeStates defends P26 task 4.5: unchecked is neither passed nor failed.
func TestArtefactVerificationHasThreeStates(t *testing.T) {
	if got := len(adminops.VerifyStates()); got != 3 {
		t.Fatalf("VerifyStates() has %d values, want 3", got)
	}
	h := newHarness(t)
	view := releaseView(t, h)
	var notYet int
	for _, a := range view.Artefacts {
		if a.Verification == adminops.VerifyNotYet {
			notYet++
			if a.Smoke != "" {
				t.Fatalf("%s is not yet verified but carries a smoke result — the sequence cannot have "+
					"reached smoke", a.Name)
			}
		}
	}
	if notYet != 1 {
		t.Fatalf("not-yet-verified artefacts = %d, want 1 — an unchecked artefact has been collapsed "+
			"into verified or failed", notYet)
	}
}

// TestTheSurfaceShowsWhereTheSequenceStopped defends P26 task 4.7.
func TestTheSurfaceShowsWhereTheSequenceStopped(t *testing.T) {
	h := newHarness(t)
	view := releaseView(t, h)

	byVersion := map[string]adminops.SequenceRow{}
	for _, s := range view.Sequences {
		byVersion[s.Version] = s
	}
	// v1.0.1 published, verified, and its smoke FAILED. It is not presented as successfully delivered.
	got := byVersion["v1.0.1"]
	if got.StoppedAt != adminops.StepSmoke {
		t.Fatalf("v1.0.1 stopped at %q, want %q", got.StoppedAt, adminops.StepSmoke)
	}
	if !strings.Contains(got.Reason, "not successfully delivered") {
		t.Fatalf("a green publish with a red smoke is not stated as such: %q", got.Reason)
	}
	// And the completed steps are legible, so a not-yet-run step is distinguishable from a failed one.
	if len(got.Completed) == 0 {
		t.Fatal("the sequence reports no completed steps — only its final state, which is what 4.7 forbids")
	}
	// v0.9.0-rc.1 never got past verification, and its stopping point says which.
	rc := byVersion["v0.9.0-rc.1"]
	if rc.StoppedAt != adminops.StepVerify {
		t.Fatalf("v0.9.0-rc.1 stopped at %q, want %q", rc.StoppedAt, adminops.StepVerify)
	}
	if !strings.Contains(rc.Reason, "not yet checked") {
		t.Fatalf("a not-yet-verified stop does not distinguish itself from a failure: %q", rc.Reason)
	}
}

// TestArtefactsSignedWithARetiredKeyAreIdentifiable defends P26 task 4.3 — the P20 incident question.
func TestArtefactsSignedWithARetiredKeyAreIdentifiable(t *testing.T) {
	h := newHarness(t)
	view := releaseView(t, h)

	var retiredSigned int
	for _, a := range view.Artefacts {
		if !a.SignedWithRetiredKey {
			continue
		}
		retiredSigned++
		if a.KeyRetiredAt == "" || a.KeyRetiredWhy == "" {
			t.Fatalf("%s is signed with a retired key but names no rotation date or reason", a.Name)
		}
	}
	if retiredSigned != 1 {
		t.Fatalf("artefacts signed with a retired key = %d, want 1 — the P20 incident question is "+
			"unanswerable from the console", retiredSigned)
	}
	// A retired key is NOT in the verify set. Serving the rotation history must never widen the set of
	// keys a running binary will accept.
	live := map[string]bool{}
	for _, k := range release.TrustRoot() {
		live[k.ID] = true
	}
	for _, k := range release.RetiredKeys() {
		if live[k.ID] {
			t.Fatalf("retired key %s is still in the trust root — the rotation record has widened the "+
				"set of keys a release signature is accepted from", k.ID)
		}
	}
}

// TestAPlatformWithNoArtefactIsVisibleAsAbsent defends the requirement that a partially-published
// version is not presented as complete.
func TestAPlatformWithNoArtefactIsVisibleAsAbsent(t *testing.T) {
	h := newHarness(t)
	view := releaseView(t, h)
	var absent int
	for _, a := range view.Artefacts {
		if !a.Published {
			absent++
		}
	}
	if absent != 1 {
		t.Fatalf("absent artefacts = %d, want 1 — a missing platform has been OMITTED rather than shown, "+
			"which makes an incomplete release look complete", absent)
	}
}

// TestAReleaseDeploymentWithNoRecordSaysSo defends the requirement that a not-yet-readable surface is
// not rendered as an empty working page.
func TestAReleaseDeploymentWithNoRecordSaysSo(t *testing.T) {
	h := newHarness(t)
	svc, err := adminops.NewReleaseService(h.exec, nil)
	if err != nil {
		t.Fatalf("NewReleaseService: %v", err)
	}
	view, err := svc.View(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if !view.Degraded {
		t.Fatal("a deployment with no release record reports a healthy, empty page")
	}
	if !strings.Contains(view.Detail, "not readable") {
		t.Fatalf("the detail does not say the record is unreadable: %q", view.Detail)
	}
	if len(view.Artefacts) != 0 {
		t.Fatal("artefacts were invented with no source")
	}
	// The channel contract is still shown: it is compiled in and true regardless of any publish record.
	if len(view.Channels) == 0 {
		t.Fatal("the channel contract is not shown")
	}
	_ = context.Background()
}
