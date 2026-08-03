package hostedproposals

import (
	"context"

	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/verification"
)

// delivery.go adapts the durable proposal store onto the two reads P12 asks of the platform: the
// authoritative gate, and the set of verified proposals eligible for delivery.
//
// They are two adapters rather than one because they answer different questions of the same table and
// are consumed by different parts of the funnel — the gate is re-read INSIDE Prepare for every single
// proposal, and the pending set is read once per fetch.

// VerdictReader is the store read behind the gate.
type VerdictReader interface {
	VerdictFor(ctx context.Context, tenantID, configHash, sourceRevision string) (verification.Verdict, bool, error)
}

// Gate implements forgedelivery.GateOracle over the durable verdicts.
//
// 🔴 It exists so Prepare re-reads the gate from the AUTHORITY rather than trusting the verdict on the
// proposal object it was handed. That is the whole design of the enforcement funnel: a proposal that
// arrived with a passing verdict attached — stale, or fabricated by anything that can construct one —
// still cannot open a pull request unless this read agrees.
type Gate struct{ store VerdictReader }

// NewGate returns the P12 gate oracle.
func NewGate(store VerdictReader) *Gate { return &Gate{store: store} }

func (g *Gate) Verdict(ctx context.Context, tenantID, configHash, sourceRevision string) (verification.Verdict, bool, error) {
	return g.store.VerdictFor(ctx, tenantID, configHash, sourceRevision)
}

// PendingReader is the store read behind the pending set.
type PendingReader interface {
	PendingVerified(ctx context.Context, tenantID string) ([]proposalstore.Scored, error)
}

// Pending implements forgedelivery.PendingProvider over the durable proposals.
type Pending struct {
	store PendingReader
	// consoleBase is the origin the PR body's evidence reference points at. Empty is legitimate — a
	// headless deployment renders relative refs — and is passed through rather than guessed.
	consoleBase string
}

// NewPending returns the P12 pending provider.
func NewPending(store PendingReader, consoleBase string) *Pending {
	return &Pending{store: store, consoleBase: consoleBase}
}

// PendingVerified returns the tenant's gate-passing proposals as delivery candidates.
//
// 🔴 DiffPatch is left EMPTY, and that is deliberate rather than unfinished. This platform records a
// candidate Variant Spec and never compiles it, so there is no diff — and Deliverer.Prepare now refuses
// a proposal with none (ErrNoDiff), which Service.Pending reports as the `no_diff` withholding. So the
// CI fetch answers "here is why nothing is deliverable" instead of serving a pull request with no
// changes in it. Synthesising a placeholder patch here to make the pipeline "work" is precisely the
// failure that refusal exists to prevent.
//
// DiffStat is empty for the same reason: a files/lines summary of a diff that does not exist would
// appear in a pull-request body as a claim about content nobody generated.
func (p *Pending) PendingVerified(ctx context.Context, tenantID string) ([]forgedelivery.Proposal, error) {
	scored, err := p.store.PendingVerified(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]forgedelivery.Proposal, 0, len(scored))
	for _, sc := range scored {
		out = append(out, forgedelivery.Proposal{
			TenantID:       tenantID,
			ProposalID:     sc.ProposalID,
			ConfigHash:     sc.CandidateConfigHash,
			SourceRevision: sc.SourceRevision,
			Title:          title(sc),
			// ADVISORY, matching the surface. Assisted is the level at which the platform opens the pull
			// request itself, and it cannot; declaring it here would also be the level at which
			// Prepare checks the auto-merge entitlement for an autonomous proposal that can never exist.
			Level:      entitlement.LevelAdvisory,
			ConsoleRef: consoleRef(p.consoleBase, sc),
		})
	}
	return out, nil
}

// title is the pull request's subject line, built from what the proposal actually says.
func title(sc proposalstore.Scored) string {
	node := sc.NodeID
	if node == "" {
		// A proposal with no node id predates migration 0030. Named as unknown rather than omitted: a
		// title reading "Optimize " is a rendering bug, and "an unrecorded node" is a fact.
		node = "an unrecorded node"
	}
	return "Optimize " + node + " (" + sc.Operator + ")"
}

// consoleRef is the canonical evidence reference the PR body cites. Relative when no console origin is
// configured — P9's rules — rather than pointing at a host that may not resolve.
func consoleRef(base string, sc proposalstore.Scored) string {
	path := "/app/proposals/" + sc.ProposalID
	if base == "" {
		return path
	}
	return base + path
}
