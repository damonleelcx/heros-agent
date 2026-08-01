package adminops

import (
	"context"
	"errors"
	"sort"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/distribution"
	"github.com/heros-foreal/agentd/internal/release"
)

// release.go is the operator's view of what the platform ships to strangers' laptops (P26 wave 26c).
//
// # Why this exists, stated as the incident it came from
//
// P20 shipped a signing pipeline, five install channels and a self-update path — and rotated the
// signing key mid-flight after the private half turned up in a plaintext tool transcript. "Which key
// is active, which are retired and when, and which published artefacts were signed with a retired one"
// was an incident question with no surface behind it. It has one now.
//
// # Two asymmetries carry the whole file
//
// **A sequence is not a state.** Publish, verify and smoke happen in ORDER, and a release that
// publishes green and smokes red is precisely the state that reaches a stranger's laptop. So the read
// model reports where the sequence STOPPED, not only its final outcome.
//
// **Queued is not failed.** A retired GitHub runner label queues until timeout rather than failing —
// measured in P20, when `macos-13` was retired and the job sat until it timed out. Rendering that as
// *failed* sends an engineer to debug a build that never ran, which is the most expensive wrong answer
// this surface could give.
//
// # 🔴 No key material, ever
//
// A key is an identifier and a fingerprint. The surface offers no generation, no export, and no
// operation whose output is key material — see `TestNoKeyMaterialReachesTheReleaseSurface`, which
// scans the whole read model for anything key-shaped rather than trusting the fields to be right.

// VerifyState is an artefact's signature/checksum verification. Three values: *not yet checked* is
// neither a pass nor a failure, and collapsing it into either is a claim nobody made.
type VerifyState string

const (
	// VerifyVerified: the checksum and the manifest signature both checked out.
	VerifyVerified VerifyState = "verified"
	// VerifyFailed: a check ran and did not pass.
	VerifyFailed VerifyState = "failed"
	// VerifyNotYet: no check has run against this artefact.
	VerifyNotYet VerifyState = "not_yet_verified"
)

// VerifyStates lists the three for a surface rendering a legend.
func VerifyStates() []VerifyState { return []VerifyState{VerifyVerified, VerifyFailed, VerifyNotYet} }

// SmokeState is the post-publish smoke result for one platform image.
type SmokeState string

const (
	// SmokePassed: the smoke job ran and passed.
	SmokePassed SmokeState = "passed"
	// SmokeFailed: the smoke job ran and failed. Somebody should look at the build.
	SmokeFailed SmokeState = "failed"
	// SmokeQueuedUntilTimeout: the job NEVER STARTED — its runner label queued until the workflow timed
	// out. 🔴 This is not a failure and must never render as one: a retired runner label produces
	// exactly this, and reading it as *failed* sends an engineer to debug a build that never ran. It is
	// a measured lesson from P20's `macos-13`, not a hypothetical.
	SmokeQueuedUntilTimeout SmokeState = "queued_until_timeout"
	// SmokeNotRun is the ABSENCE of a smoke run, and it is deliberately not one of the three: a release
	// whose sequence stopped before smoke has no smoke result to render, and the surface says where the
	// sequence stopped instead of inventing a fourth outcome.
	SmokeNotRun SmokeState = ""
)

// SmokeStates lists the three real outcomes. SmokeNotRun is excluded on purpose — it is an absence.
func SmokeStates() []SmokeState {
	return []SmokeState{SmokePassed, SmokeFailed, SmokeQueuedUntilTimeout}
}

// PublishStep is one step of the publish → verify → smoke sequence.
type PublishStep string

const (
	StepPublish PublishStep = "publish"
	StepVerify  PublishStep = "verify"
	StepSmoke   PublishStep = "smoke"
	// StepComplete means the sequence ran to the end. It is a value rather than an empty string so a
	// surface's switch is exhaustive and "finished" cannot be mistaken for "no information".
	StepComplete PublishStep = "complete"
)

// PublishSequence lists the steps in order.
func PublishSequence() []PublishStep { return []PublishStep{StepPublish, StepVerify, StepSmoke} }

// ArtefactRecord is what the release pipeline recorded about one published artefact.
type ArtefactRecord struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	// Published is false when this platform got no artefact for this version. A missing platform is
	// SHOWN as absent rather than omitted, because an omitted row makes an incomplete release look
	// complete.
	Published    bool        `json:"published"`
	Verification VerifyState `json:"verification"`
	Smoke        SmokeState  `json:"smoke,omitempty"`
	SmokeDetail  string      `json:"smoke_detail,omitempty"`
}

// ReleaseRecord is one published version, as the pipeline recorded it.
type ReleaseRecord struct {
	Version      string           `json:"version"`
	Channel      string           `json:"channel"`
	PublishedAt  string           `json:"published_at"`
	SigningKeyID string           `json:"signing_key_id"`
	Artefacts    []ArtefactRecord `json:"artefacts"`
}

// ReleaseSource is what the deployment supplies: the releases its pipeline actually published.
//
// It is an interface rather than a table (design D7 — no new table in this phase). A deployment with
// no source reports that plainly; it does not render an empty page that looks like a working one.
type ReleaseSource interface {
	Releases() []ReleaseRecord
	// Describe names the source on the surface, so an operator can tell a real record from a fixture.
	Describe() string
}

// ArtefactRow is one artefact as the operator reads it, with its key's identity attached.
type ArtefactRow struct {
	Version  string `json:"version"`
	Channel  string `json:"channel"`
	Platform string `json:"platform"`
	Name     string `json:"name"`
	// Published false renders as *absent*, and the version it belongs to is not presented as complete.
	Published bool `json:"published"`
	// SigningKeyID and KeyFingerprint IDENTIFY the key. 🔴 There is no field here that could hold key
	// material, which is the point: the type cannot carry it, so no renderer can leak it.
	SigningKeyID   string `json:"signing_key_id"`
	KeyFingerprint string `json:"key_fingerprint"`
	// SignedWithRetiredKey marks an artefact in the field whose signature came from a key that has
	// since left the trust root — the P20 incident question, answerable from the console.
	SignedWithRetiredKey bool        `json:"signed_with_retired_key"`
	KeyRetiredAt         string      `json:"key_retired_at,omitempty"`
	KeyRetiredWhy        string      `json:"key_retired_why,omitempty"`
	Verification         VerifyState `json:"verification"`
	Smoke                SmokeState  `json:"smoke,omitempty"`
	SmokeDetail          string      `json:"smoke_detail,omitempty"`
}

// SequenceRow reports where one release's publish → verify → smoke sequence stopped.
type SequenceRow struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
	// StoppedAt is the step that did not complete, or StepComplete. An operator can tell a NOT-YET-RUN
	// step from a FAILED one because the two produce different values here and different reasons.
	StoppedAt PublishStep `json:"stopped_at"`
	Reason    string      `json:"reason"`
	// Completed lists the steps that finished, in order, so the progression is legible rather than
	// inferred from a single final state.
	Completed []PublishStep `json:"completed"`
}

// SigningKeyRow is one key's identity and role. No key material — identifier and fingerprint only.
type SigningKeyRow struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	// Role is `active` or `retired`. `accepted` (a key in a rotation's overlap window) renders as
	// active-for-verification with its own note, because a reader must not conclude that a key which
	// still verifies has been withdrawn.
	Role      string `json:"role"`
	Note      string `json:"note,omitempty"`
	RetiredAt string `json:"retired_at,omitempty"`
	// SignedReleases names the published releases this key signed. EMPTY is a real answer.
	SignedReleases []string `json:"signed_releases,omitempty"`
}

// ChannelRow is one install channel and what it serves.
type ChannelRow struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Delivered is whether a user can install from this channel TODAY. A generated manifest is not a
	// channel a customer can use.
	Delivered bool   `json:"delivered"`
	Blocker   string `json:"blocker,omitempty"`
	// Verification states how this channel establishes the bytes are ours. Per channel, because it
	// genuinely differs — and because "verified" with no mechanism is a claim this project does not make.
	Verification string   `json:"verification"`
	Versions     []string `json:"versions,omitempty"`
}

// ReleaseView is the operator's release and trust read model.
type ReleaseView struct {
	Channels  []ChannelRow    `json:"channels"`
	Artefacts []ArtefactRow   `json:"artefacts"`
	Sequences []SequenceRow   `json:"sequences"`
	Keys      []SigningKeyRow `json:"keys"`
	// ReadOnly is stated on the wire. This surface halts nothing, unpublishes nothing and re-signs
	// nothing: it shows a problem it cannot act on, which is this phase's deliberate boundary.
	ReadOnly bool `json:"read_only"`
	// Degraded and Detail report an unreadable source, distinct from "no releases".
	Degraded bool   `json:"degraded"`
	Detail   string `json:"detail,omitempty"`
	Source   string `json:"source"`
}

// ReleaseService serves the release read model. READ-ONLY: no method here changes anything.
type ReleaseService struct {
	exec    *Executor
	records ReleaseSource
}

// NewReleaseService wires the read model. A nil source is legal and honest: the view then reports that
// no release record is available rather than rendering an empty page as a working one.
func NewReleaseService(exec *Executor, records ReleaseSource) (*ReleaseService, error) {
	if exec == nil {
		return nil, errors.New("adminops: the release read model needs the command path")
	}
	return &ReleaseService{exec: exec, records: records}, nil
}

// View returns the release and trust picture.
func (s *ReleaseService) View(ctx context.Context) (ReleaseView, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapReleaseRead, TargetGlobal)
	if err != nil {
		return ReleaseView{}, err
	}
	if _, err := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: "release oversight read", Result: "viewed",
		Evidence: map[string]string{"read_model": "release"}, CreatedAt: s.exec.Now(),
	}); err != nil {
		return ReleaseView{}, errors.New("adminops: release read refused — it could not be logged: " + err.Error())
	}

	// Empty rather than nil, for the same reason the other read models are: a nil slice marshals to
	// `null` and a client reading a length off it crashes.
	view := ReleaseView{
		ReadOnly: true, Keys: signingKeyRows(),
		Channels: []ChannelRow{}, Artefacts: []ArtefactRow{}, Sequences: []SequenceRow{},
		Source: "internal/distribution + the compiled trust root",
	}
	if s.records != nil {
		view.Source = s.records.Describe()
	}

	// Retired keys, indexed so an artefact can be matched against one. The P20 question — "which
	// published artefacts were signed with a retired key" — is this join and nothing more.
	retired := map[string]release.RetiredKey{}
	for _, k := range release.RetiredKeys() {
		retired[k.ID] = k
	}
	active, activeErr := release.ActiveKey()

	versionsByChannel := map[string][]string{}
	if s.records != nil {
		for _, rec := range s.records.Releases() {
			versionsByChannel[rec.Channel] = append(versionsByChannel[rec.Channel], rec.Version)
			view.Sequences = append(view.Sequences, sequenceOf(rec))
			for _, a := range rec.Artefacts {
				row := ArtefactRow{
					Version: rec.Version, Channel: rec.Channel, Platform: a.Platform, Name: a.Name,
					Published: a.Published, SigningKeyID: rec.SigningKeyID,
					Verification: a.Verification, Smoke: a.Smoke, SmokeDetail: a.SmokeDetail,
				}
				if rk, ok := retired[rec.SigningKeyID]; ok {
					row.SignedWithRetiredKey = true
					row.KeyFingerprint = rk.Fingerprint
					row.KeyRetiredAt = rk.RetiredAt
					row.KeyRetiredWhy = rk.Reason
				} else if activeErr == nil && rec.SigningKeyID == active.ID {
					row.KeyFingerprint = active.Fingerprint()
				}
				view.Artefacts = append(view.Artefacts, row)
			}
		}
	} else {
		view.Degraded = true
		view.Detail = "this deployment carries no release record — the publish pipeline's outcomes are " +
			"not readable from here. No release, verification or smoke result is shown, and none is inferred."
	}

	for _, c := range distribution.Channels() {
		versions := versionsByChannel[c.ID]
		sort.Strings(versions)
		view.Channels = append(view.Channels, ChannelRow{
			ID: c.ID, Label: c.Label, Delivered: c.Delivered(), Blocker: c.Blocker,
			Verification: c.Verification, Versions: versions,
		})
	}
	return view, nil
}

// sequenceOf reports where one release's publish → verify → smoke sequence stopped.
//
// The order is the whole point: a release that published green and smoked red is not "a release with a
// smoke problem", it is a release that REACHED USERS and then failed its check. Reporting only the
// final state would make those two look the same.
func sequenceOf(rec ReleaseRecord) SequenceRow {
	row := SequenceRow{Version: rec.Version, Channel: rec.Channel}

	var anyPublished, allPublished = false, true
	for _, a := range rec.Artefacts {
		if a.Published {
			anyPublished = true
		} else {
			allPublished = false
		}
	}
	if !anyPublished {
		row.StoppedAt, row.Reason = StepPublish, "no artefact was published for any platform"
		return row
	}
	row.Completed = append(row.Completed, StepPublish)
	if !allPublished {
		row.Reason = "published for some platforms and not others — this version is not complete"
	}

	for _, a := range rec.Artefacts {
		if !a.Published {
			continue
		}
		if a.Verification == VerifyFailed {
			row.StoppedAt = StepVerify
			row.Reason = "verification FAILED for " + a.Platform
			return row
		}
		if a.Verification == VerifyNotYet {
			row.StoppedAt = StepVerify
			row.Reason = "verification has not run for " + a.Platform + " — not yet checked is neither a pass nor a failure"
			return row
		}
	}
	row.Completed = append(row.Completed, StepVerify)

	for _, a := range rec.Artefacts {
		if !a.Published {
			continue
		}
		switch a.Smoke {
		case SmokeNotRun:
			row.StoppedAt = StepSmoke
			row.Reason = "the post-publish smoke has not run for " + a.Platform
			return row
		case SmokeQueuedUntilTimeout:
			// 🔴 Reported as its own stopping point, NOT as a failure. The job never started.
			row.StoppedAt = StepSmoke
			row.Reason = "the smoke job for " + a.Platform + " queued until timeout and never started — " +
				"this is not a failed build, and debugging the build would be debugging something that did not run"
			return row
		case SmokeFailed:
			row.StoppedAt = StepSmoke
			row.Reason = "the post-publish smoke FAILED for " + a.Platform +
				" — this release published successfully and is not successfully delivered"
			return row
		}
	}
	row.Completed = append(row.Completed, StepSmoke)
	row.StoppedAt = StepComplete
	if row.Reason == "" {
		row.Reason = "published, verified and smoked"
	}
	return row
}

// signingKeyRows renders the trust root and the rotation record — identifier and fingerprint only.
func signingKeyRows() []SigningKeyRow {
	var out []SigningKeyRow
	for _, k := range release.TrustRoot() {
		role := "accepted"
		if k.Role == release.RoleActive {
			role = "active"
		}
		out = append(out, SigningKeyRow{ID: k.ID, Fingerprint: k.Fingerprint(), Role: role, Note: k.Note})
	}
	for _, k := range release.RetiredKeys() {
		out = append(out, SigningKeyRow{
			ID: k.ID, Fingerprint: k.Fingerprint, Role: "retired", Note: k.Reason,
			RetiredAt: k.RetiredAt, SignedReleases: k.SignedReleases,
		})
	}
	return out
}
