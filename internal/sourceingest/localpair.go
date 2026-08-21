package sourceingest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// localpair.go is Mode 3: the console PAIRS with a local agent that reads the repository in place.
//
// # Why a pairing and not a file picker (design D5)
//
// A browser affordance that reads a folder and posts it is Mode 1 wearing Mode 3's clothes. The
// customer would reasonably believe nothing left their machine — the control says "select a local
// repo" — and a UI whose data-handling outcome differs from what its label implies is a consent
// failure, not a shortcut. So the browser never touches the tree at all: it hands out a code, a person
// types it into a terminal that is already on the machine holding the repository, and the reading
// happens there.
//
// # What is actually transmitted, and what the pairing is FOR
//
// Nothing from the tree. The local agent runs discovery itself and sends the allowlisted structure
// payload the CLI already sends (`heros link --with-ir`) — symbols, files, line spans, model refs,
// tool counts, and never prompt text, source or a diff. `internal/runlink`'s construction-from-
// allowlist is what makes that true, and §7.9 asserts it with an egress capture rather than by reading
// this comment.
//
// The pairing exists so the CONSOLE can say something true: which machine reads this workflow, at which
// revision, and when it last did. Without it the console can only report that structure arrived from
// somewhere, which is the state P29's surfaces were already in.
//
// # 🚫 A pairing is not a credential and grants nothing
//
// It authorizes no read. The agent already holds the person's own credential from `heros login`; the
// pairing only ATTRIBUTES what that agent sends to a workflow, and it expires. A revoked pairing does
// not stop the agent reading the customer's own disk — nothing could, and nothing should pretend to.
// That asymmetry with Mode 2 is the whole point of Mode 3 and is stated in the console.

// PairingState is the closed vocabulary of where a pairing is.
type PairingState string

const (
	// PairingPending — the console issued a code and nobody has typed it yet.
	PairingPending PairingState = "pending"
	// PairingPaired — a local agent claimed the code and named itself.
	PairingPaired PairingState = "paired"
	// PairingExpired — the code was never claimed in time. A DISTINCT state rather than deletion,
	// because "your code expired, here is a new one" and "no such code" send a person to two different
	// places and only one of them is where the problem is.
	PairingExpired PairingState = "expired"
)

// PairingStates returns the three states, sorted. The console renders one message per member.
func PairingStates() []PairingState {
	return []PairingState{PairingExpired, PairingPaired, PairingPending}
}

// Valid reports membership.
func (p PairingState) Valid() bool {
	return p == PairingPending || p == PairingPaired || p == PairingExpired
}

// String makes PairingState printable.
func (p PairingState) String() string { return string(p) }

// PairingTTL is how long an unclaimed code lives.
//
// Ten minutes: long enough to switch windows, find the terminal and paste, and short enough that a code
// left on a screen in a meeting room is not a standing affordance. It is the same reasoning P27's
// device flow used, and deliberately the same number — two nearly-identical flows with two different
// timeouts is a support conversation nobody can hold.
const PairingTTL = 10 * time.Minute

// Pairing is one console↔agent pairing.
//
// 🚫 No field can hold a path, a credential or anything from the tree. `MachineName` is what the agent
// calls itself and `Revision` is a commit id — neither is content, and there is deliberately no
// `RepositoryPath`: a local filesystem path is the customer's own layout, it tells the platform nothing
// it needs, and having somewhere to put it is how it ends up transmitted.
type Pairing struct {
	PairingID  string       `json:"pairing_id"`
	TenantID   string       `json:"tenant_id"`
	WorkflowID string       `json:"workflow_id"`
	State      PairingState `json:"state"`
	// UserCode is what the console shows and the person types. Short, unambiguous, and single-use.
	UserCode string `json:"user_code"`
	// MachineName is what the agent calls itself, so the console can say WHICH machine. Customer-
	// supplied text, rendered by React's default escaping and never interpolated into anything.
	MachineName string `json:"machine_name,omitempty"`
	// Revision is the commit the agent reported reading. A revision id, never the code at it.
	Revision    string `json:"revision,omitempty"`
	CreatedAtMS int64  `json:"created_at_ms"`
	ClaimedAtMS int64  `json:"claimed_at_ms,omitempty"`
	ExpiresAtMS int64  `json:"expires_at_ms"`
}

// Refusals.
var (
	// ErrNoPairing: no pairing matches. Returned for an unknown code, an already-claimed one and a
	// wrong tenant alike — the same answer, deliberately, because distinguishing them would let
	// somebody probe which codes exist.
	ErrNoPairing = errors.New("sourceingest: no pending pairing matches that code")
	// ErrPairingExpired: the code was issued and its window closed. DISTINCT from ErrNoPairing, and
	// safe to distinguish because the caller already proved they hold the code.
	ErrPairingExpired = errors.New("sourceingest: that pairing code has expired — start a new one from the console")
)

// PairingStore is the durable side of the pairing flow.
//
// 🔴 Durable, not a map, and for the reason P27's device authorization is a table: the console POLLS.
// It starts a pairing against one replica and polls against whichever the load balancer picks next, so
// a map means a pairing that completes or hangs depending on routing — intermittently, with nothing
// logged.
type PairingStore interface {
	// CreatePairing records a pending pairing.
	CreatePairing(ctx context.Context, p Pairing) error
	// ClaimPairing moves a pending pairing to paired, recording the machine and revision. It is the
	// only write that changes state, and it must be atomic: two agents racing on one code must
	// produce one success and one ErrNoPairing.
	ClaimPairing(ctx context.Context, userCode, machineName, revision string, atMS int64) (Pairing, error)
	// PairingByID returns one pairing within a tenant, for the console's poll.
	PairingByID(ctx context.Context, tenantID, pairingID string, nowMS int64) (Pairing, error)
	// PairingsForTenant returns a tenant's pairings, newest first.
	PairingsForTenant(ctx context.Context, tenantID string, nowMS int64) ([]Pairing, error)
}

// Validate rejects a partial pairing.
func (p Pairing) Validate() error {
	switch {
	case p.PairingID == "":
		return fmt.Errorf("sourceingest: pairing has no pairing_id")
	case p.TenantID == "":
		return fmt.Errorf("sourceingest: pairing has no tenant_id")
	case p.WorkflowID == "":
		return fmt.Errorf("sourceingest: pairing has no workflow_id")
	case p.UserCode == "":
		return fmt.Errorf("sourceingest: pairing has no user code")
	case !p.State.Valid():
		return fmt.Errorf("sourceingest: %q is not a pairing state", p.State)
	case p.ExpiresAtMS <= 0:
		return fmt.Errorf("sourceingest: a pairing with no expiry is a standing affordance")
	}
	return nil
}

// StateAt reports the pairing's state as of nowMS, applying the expiry.
//
// 🔴 Expiry is computed at READ rather than written by a sweeper. A sweeper that has not run yet would
// make an expired code still claimable, which is the one property the TTL exists to provide — and a
// pairing flow whose safety depends on a background job having run recently is a flow that is unsafe
// exactly when the deployment is unhealthy.
func (p Pairing) StateAt(nowMS int64) PairingState {
	if p.State == PairingPending && nowMS >= p.ExpiresAtMS {
		return PairingExpired
	}
	return p.State
}

// pairingCodeAlphabet excludes the characters people mistype when reading a screen aloud.
//
// No `0`/`O`, no `1`/`I`/`L`, no `5`/`S`, no `2`/`Z`, no `8`/`B`. A code is typed by a person who is
// looking at one screen and typing into another, and a flow whose failure mode is "it says my code is
// wrong" teaches them the product is broken rather than that they typed an O for a zero.
const pairingCodeAlphabet = "ACDEFGHJKMNPQRTUVWXY34679"

// NewPairingCode returns a fresh `XXXX-XXXX` code.
//
// 🔴 `crypto/rand`, not `math/rand`, and no fallback on error. A guessable pairing code lets somebody
// claim a pairing that was meant for a person on another machine — and while the pairing grants no
// read, it does attribute a machine's transmissions to a workflow, which is a lie in the console's
// ledger. Forty bits of entropy over a ten-minute window is far more than the flow needs and costs
// nothing.
func NewPairingCode() string {
	const groups, per = 2, 4
	b := make([]byte, groups*per)
	if _, err := rand.Read(b); err != nil {
		panic("sourceingest: the system random source is unavailable: " + err.Error())
	}
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%per == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(pairingCodeAlphabet[int(v)%len(pairingCodeAlphabet)])
	}
	return sb.String()
}

// NormalizePairingCode makes a typed code comparable to a stored one.
//
// Upper-cased and stripped of everything that is not in the alphabet, so `acde-fghj`, `ACDEFGHJ` and
// `ACDE FGHJ` are all the same code. This is a UX decision with a security consequence worth stating:
// it widens what is accepted, and it widens it only across renderings of the SAME code — the entropy is
// unchanged, because the alphabet has no case-collapsing pairs.
func NormalizePairingCode(in string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(in)) {
		if strings.ContainsRune(pairingCodeAlphabet, r) {
			sb.WriteRune(r)
		}
	}
	out := sb.String()
	if len(out) == 8 {
		return out[:4] + "-" + out[4:]
	}
	return out
}

// LocalModeAvailability is what the console states BEFORE the pairing flow starts (FR15, §14 A1).
//
// # 🔴 Why this is a value and not a sentence in a TSX file
//
// FR15's requirement is that the console *"SHALL state which deployments it works against rather than
// failing at the end of the flow."* A sentence maintained in the console cannot be checked against the
// pin that actually decides. `Deployments` is served from `runlink.PlatformBaseURL` — the same constant
// `IsLinkTarget` enforces — so the screen and the enforcement cannot disagree.
type LocalModeAvailability struct {
	// Deployments are the deployment URLs the local bridge works against. Exactly one today.
	Deployments []string `json:"deployments"`
	// Available reports whether THIS deployment is one of them.
	Available bool `json:"available"`
	// Why states the limit in the customer's terms when it is not. Never empty when Available is
	// false: a capability that is off with no reason given is indistinguishable from one that is
	// broken.
	Why string `json:"why,omitempty"`
}

// PairingService is the pairing lifecycle.
type PairingService struct {
	store PairingStore
	nowMS func() int64
	newID func(prefix string) string
	code  func() string
}

// PairingConfig wires a PairingService.
type PairingConfig struct {
	Store PairingStore
	NowMS func() int64
	IDFor func(prefix string) string
	// Code is injected so a test can pin the code without a second clock or a guess. Production leaves
	// it nil and gets NewPairingCode.
	Code func() string
}

// NewPairingService builds the lifecycle.
func NewPairingService(cfg PairingConfig) (*PairingService, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("sourceingest: the pairing service needs a store")
	}
	s := &PairingService{store: cfg.Store, nowMS: cfg.NowMS, newID: cfg.IDFor, code: cfg.Code}
	if s.nowMS == nil {
		s.nowMS = func() int64 { return time.Now().UnixMilli() }
	}
	if s.newID == nil {
		s.newID = defaultID
	}
	if s.code == nil {
		s.code = NewPairingCode
	}
	return s, nil
}

// Start issues a pairing code for a workflow.
func (s *PairingService) Start(ctx context.Context, tenantID, workflowID string) (Pairing, error) {
	if tenantID == "" || workflowID == "" {
		return Pairing{}, fmt.Errorf("sourceingest: a pairing needs a tenant and a workflow")
	}
	// 🔴 The generated code is NORMALIZED before it is stored, and refused if normalisation empties
	// it. `Claim` normalizes what the person typed, so a stored code in any other form is a code that
	// can never be claimed — the flow would issue it, the console would display it, the person would
	// type it correctly, and the platform would answer "no such code" forever. Found by a test whose
	// injected generator produced a code outside the alphabet, which is exactly the shape a future
	// deployment supplying its own generator would take.
	code := NormalizePairingCode(s.code())
	if code == "" {
		return Pairing{}, fmt.Errorf("sourceingest: the pairing-code generator produced a code that " +
			"normalises to nothing — it must use the alphabet in NewPairingCode, or every code it issues " +
			"is unclaimable")
	}
	now := s.nowMS()
	p := Pairing{
		PairingID:   s.newID("pair"),
		TenantID:    tenantID,
		WorkflowID:  workflowID,
		State:       PairingPending,
		UserCode:    code,
		CreatedAtMS: now,
		ExpiresAtMS: now + PairingTTL.Milliseconds(),
	}
	if err := p.Validate(); err != nil {
		return Pairing{}, err
	}
	if err := s.store.CreatePairing(ctx, p); err != nil {
		return Pairing{}, err
	}
	return p, nil
}

// Claim is what the local agent calls with the code the person typed.
//
// 🔴 It takes NO tenant. The agent is authenticated by its own credential and the code identifies the
// pairing; requiring the agent to also name a tenant would be a field a caller could get wrong, and
// the store's claim is scoped by the code alone. The tenant on the resulting pairing is the one the
// CONSOLE created it under, which is the only tenant that can be correct.
func (s *PairingService) Claim(ctx context.Context, userCode, machineName, revision string) (Pairing, error) {
	code := NormalizePairingCode(userCode)
	if code == "" {
		return Pairing{}, ErrNoPairing
	}
	if strings.TrimSpace(machineName) == "" {
		// Refused rather than defaulted to "unknown". The whole value of the pairing to the console is
		// being able to say WHICH machine reads this workflow, and "unknown" answers nothing while
		// looking like an answer.
		return Pairing{}, fmt.Errorf("sourceingest: a pairing must name the machine claiming it")
	}
	return s.store.ClaimPairing(ctx, code, machineName, revision, s.nowMS())
}

// Get returns one pairing for the console's poll.
func (s *PairingService) Get(ctx context.Context, tenantID, pairingID string) (Pairing, error) {
	return s.store.PairingByID(ctx, tenantID, pairingID, s.nowMS())
}

// List returns a tenant's pairings.
func (s *PairingService) List(ctx context.Context, tenantID string) ([]Pairing, error) {
	return s.store.PairingsForTenant(ctx, tenantID, s.nowMS())
}

// Availability reports which deployments the local mode works against.
//
// `platformBaseURL` is `runlink.PlatformBaseURL`, passed in rather than imported so this package does
// not depend on the CLI's transport for a string. `thisDeployment` is the URL this console is served
// from; an empty value means the deployment does not know its own address, which is reported as
// unavailable-with-a-reason rather than assumed to be a match.
func Availability(platformBaseURL, thisDeployment string) LocalModeAvailability {
	a := LocalModeAvailability{Deployments: []string{platformBaseURL}}
	switch {
	case thisDeployment == "":
		a.Why = "This deployment has not been told its own public address, so it cannot tell whether the " +
			"local bridge can reach it. Push a source bundle instead, or connect the repository."
	case strings.EqualFold(strings.TrimSuffix(thisDeployment, "/"), strings.TrimSuffix(platformBaseURL, "/")):
		a.Available = true
	default:
		a.Why = "Reading a repository in place works against " + platformBaseURL + " only. The command that " +
			"does it transmits to that address and nothing overrides it — not a flag, not an environment " +
			"variable, not a config key. On this deployment, push a source bundle or connect the repository."
	}
	return a
}
