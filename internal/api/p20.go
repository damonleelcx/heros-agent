package api

import (
	"net/http"

	"github.com/heros-foreal/agentd/internal/distribution"
)

// p20.go is the install/distribution read model: GET /api/v1/install.
//
// # Why the console needs a surface for this at all
//
// Because "how do I install this, and can I trust it" is the first question a customer's engineer asks, and
// before this the answer lived in a README that nobody could check against the code. The console renders the
// same frozen contract the release pipeline enforces — the target matrix, the channel capability map and the
// trust posture — so a reader on the website and a reader running `heros doctor` cannot be told different
// things.
//
// # The two things this payload is careful about
//
//  1. It is TOTAL. Every target row, including the ones we do not build, and every channel, including the ones
//     whose manifest exists but which nobody can install from yet. This is the P13 coverage lesson: a table
//     listing only what works forces the reader to infer everything else from a blank, and a blank reads as
//     "should work — must be your setup".
//
//  2. The ratified POSTURE and the delivered TRUST are separate fields, and the delivered one is a POINTER.
//     Nil means no release's attestation is known to this server — which is the truth until a release is
//     published — and the console renders that as "not yet published" rather than as a posture. Collapsing the
//     two would let the console announce "notarized" the day the budget was approved, months before any
//     artifact was.
type installView struct {
	// MatrixVersion is the content hash of the frozen target matrix, so a console/CLI disagreement about
	// whether a platform is supported is diagnosable — both sides can name their table.
	MatrixVersion string `json:"matrix_version"`
	// DocumentedRelease is the release the install commands are pinned to.
	DocumentedRelease string `json:"documented_release"`
	// RatifiedPosture is the OS-trust DECISION (a spend commitment), never a claim about any artifact.
	RatifiedPosture string `json:"ratified_posture"`
	// Delivered is what a specific release actually delivered. Nil when this server knows of no published
	// release — rendered as an absence, never as a posture.
	Delivered *deliveredTrustView `json:"delivered,omitempty"`
	Targets   []targetView        `json:"targets"`
	Channels  []channelView       `json:"channels"`
}

type targetView struct {
	Key      string `json:"key"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	// Support is "shipped" or "limit". A limit row is a ROW, not an absence.
	Support string `json:"support"`
	// Runner is the native runner that builds it — empty on a limit row, and that emptiness is the
	// assertion that no job builds it.
	Runner string `json:"runner,omitempty"`
	// Limit and Answer are required together on a limit row: a limit with no answer is an apology.
	Limit    string   `json:"limit,omitempty"`
	Answer   string   `json:"answer,omitempty"`
	Channels []string `json:"channels"`
}

type channelView struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	OSes  []string `json:"oses"`
	// Delivered is whether a user can install from it TODAY. Not "we wrote a generator".
	Delivered bool `json:"delivered"`
	// Publication is the stable identifier the console branches on — published / pending-external-repo /
	// pending-upstream-pr — so the UI reads data rather than prose.
	Publication string `json:"publication"`
	// Blocker says what is missing, and is present exactly when Delivered is false.
	Blocker string `json:"blocker,omitempty"`
	// Verification states how this channel establishes that the bytes are ours. It differs per channel, and
	// "verified" with no mechanism is the claim this project does not make.
	Verification string `json:"verification"`
	ManagerOwned bool   `json:"manager_owned"`
	Install      string `json:"install"`
	Upgrade      string `json:"upgrade"`
	Uninstall    string `json:"uninstall"`
	Pin          string `json:"pin"`
}

// deliveredTrustView is one release's delivered trust properties — every claim, earned and not.
//
// Unearned claims are included deliberately: a payload carrying only the earned ones could not disclose a gap,
// and an undisclosed gap is how a user discovers Gatekeeper by being blocked by it.
type deliveredTrustView struct {
	Version      string      `json:"version"`
	SigningKeyID string      `json:"signing_key_id,omitempty"`
	Claims       []claimView `json:"claims"`
}

type claimView struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Earned bool   `json:"earned"`
}

// releaseAttestation is the published release's trust posture, when this server has been told one. It is a
// field rather than a fetch: the api layer does not reach out to a Release page, because a console that
// rendered a live third-party lookup would show a different answer every time GitHub was slow.
var releaseAttestation *distribution.Attestation

// SetReleaseAttestation records the published release's attestation, to be rendered on the install surface.
// Called by a deployment that has one; a deployment that does not leaves it nil, and the console says so.
func SetReleaseAttestation(a *distribution.Attestation) { releaseAttestation = a }

func installReadModel() installView {
	out := installView{
		MatrixVersion:     distribution.MatrixVersion(),
		DocumentedRelease: distribution.DocumentedRelease,
		RatifiedPosture:   string(distribution.ChosenPosture),
	}
	version := distribution.DocumentedRelease
	if releaseAttestation != nil {
		version = "v" + releaseAttestation.Version
		dv := deliveredTrustView{Version: releaseAttestation.Version, SigningKeyID: releaseAttestation.SigningKeyID}
		for _, c := range releaseAttestation.Claims() {
			dv.Claims = append(dv.Claims, claimView{ID: c.ID, Text: c.Text, Earned: c.Earned})
		}
		out.Delivered = &dv
	}
	v, err := distribution.ParseTag(version)
	if err != nil {
		v = distribution.DevVersion()
	}

	for _, t := range distribution.Targets() {
		arch := t.GOARCH
		if arch == "" {
			arch = "any"
		}
		out.Targets = append(out.Targets, targetView{
			Key: t.Key(), Platform: t.Platform, Arch: arch, Support: string(t.Support),
			Runner: t.Runner, Limit: t.Limit, Answer: t.Answer, Channels: t.Channels,
		})
	}
	for _, c := range distribution.Channels() {
		out.Channels = append(out.Channels, channelView{
			ID: c.ID, Label: c.Label, OSes: c.GOOSes,
			Delivered: c.Delivered(), Publication: string(c.Publication), Blocker: c.Blocker,
			Verification: c.Verification, ManagerOwned: c.ManagerOwned,
			Install:   distribution.Command(c.Install, v.Version),
			Upgrade:   distribution.Command(c.Upgrade, v.Version),
			Uninstall: distribution.Command(c.Uninstall, v.Version),
			Pin:       distribution.Command(c.Pin, v.Version),
		})
	}
	return out
}

// InstallReadModelForPreview exposes the read model to the console's preview seeder, so the browser-checkable
// preview is seeded from the CONTRACT rather than from a hand-written fixture. A hand-written one drifts, and a
// preview that drifts stops catching anything.
func InstallReadModelForPreview() any { return installReadModel() }

// handleP20Install serves the read model. It takes no tenant, no plan and no role — and that absence is the
// assertion: which platforms are supported and which channels exist are properties of the RELEASE, so no
// entitlement can move a row. A future contributor adding a plan input has to change this signature.
func (s *Server) handleP20Install(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, installReadModel())
}
