package metering

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/verification"
)

// savings.go computes VERIFIED BILLABLE SAVINGS — the only thing gainshare may be billed on (task 6.1 /
// design Decision 8 / FR3).
//
// ## This is the phase's load-bearing invariant
//
// The platform's founding law is *analysis without verification is confident guessing*, and its most
// expensive failure mode is BILLING a customer for a guessed saving. So this computation reads exactly
// one input — the P5.5 verified-delta ledger — and accepts an entry only when it is:
//
//	VERIFIED  the P5.5 gate passed: held-out, statistically significant, regression-clean
//	MERGED    a git-history fact per ADR-001 — the saving is not real until the change ships
//
// An estimated, unverified, or verified-but-un-merged saving contributes ZERO. Not "is discounted",
// not "is flagged": zero. `Billable()` is the single predicate, and every reason an entry can fail it
// is named on the entry itself, so a customer asking "why was this not billed" gets an answer.
//
// ## Why the refs are stored, not just the number
//
// A billed saving must TRACE. Every `BillableSavings` row carries the verifying ledger entries and the
// merge commits that produced it, and the baseline + holdout methodology is reconstructable from those
// refs. That is what makes the figure auditable rather than asserted — the difference between "we saved
// you this" and "here is the held-out experiment, the seeds, the excluded generating cases, and the
// commit that shipped it".

// BaselineMethod is the FIXED baseline + holdout methodology behind one verified delta. It is stored on
// the ledger entry (not recomputed per invoice) precisely so a gainshare figure cannot drift with
// whoever runs the query: the methodology is a property of the experiment, not of the billing run.
type BaselineMethod struct {
	// ID names the methodology version ("holdout-v1"). A change to how baselines are formed is a NEW id,
	// so two periods computed under different methods can never be silently compared.
	ID string `json:"id"`
	// EvalSetHash pins the eval set the delta was measured over.
	EvalSetHash string `json:"eval_set_hash"`
	// HoldoutCaseIDs / GeneratingCaseIDs are the split. The generating cases produced the diagnosis and
	// are EXCLUDED from the held-out measurement — that exclusion is what makes the delta generalize
	// rather than describe the cases it was fitted on.
	HoldoutCaseIDs    []string `json:"holdout_case_ids"`
	GeneratingCaseIDs []string `json:"generating_case_ids"`
	// Seeds are the seeds the multi-seed verification ran under.
	Seeds []int64 `json:"seeds"`
	// BaselineConfigHash / CandidateConfigHash bound the comparison.
	BaselineConfigHash  string `json:"baseline_config_hash"`
	CandidateConfigHash string `json:"candidate_config_hash"`
}

// Complete reports whether the methodology is fully reconstructable. An incomplete method makes the
// figure un-auditable, which disqualifies the saving from being billed at all.
func (m BaselineMethod) Complete() bool {
	return m.ID != "" && m.EvalSetHash != "" && len(m.HoldoutCaseIDs) > 0 && len(m.Seeds) > 0 &&
		m.BaselineConfigHash != "" && m.CandidateConfigHash != ""
}

// VerifiedDelta is one entry of the P5.5 verified-delta ledger, as gainshare reads it.
//
// It carries the REAL `verification.Verdict` rather than a copy of a few of its fields: the gate result,
// held-out flag and significance are the P5.5 gate's own output, and re-encoding them here would create
// a second opinion about whether something was verified.
type VerifiedDelta struct {
	// Ref is the ledger entry's stable id — what a billed saving traces BACK to.
	Ref        string `json:"ref"`
	ProposalID string `json:"proposal_id"`
	CustomerID string `json:"customer_id"`
	Period     string `json:"period"`
	// Verdict is the P5.5 gate's verdict, verbatim.
	Verdict verification.Verdict `json:"verdict"`
	// Merged / MergeCommit are the ADR-001 git-history fact. A verified-but-un-merged proposal is a
	// saving that has not happened.
	Merged      bool   `json:"merged"`
	MergeCommit string `json:"merge_commit,omitempty"`
	// Estimated marks a projection rather than a measurement (a P4.5/P5.5 pre-verification number). It is
	// carried explicitly so an estimate can be SHOWN in the product and still be structurally unbillable.
	Estimated bool `json:"estimated"`
	// BaselineSUM / OptimizedSUM are the period's spend under management before and after the change.
	BaselineSUM  float64        `json:"baseline_sum"`
	OptimizedSUM float64        `json:"optimized_sum"`
	Baseline     BaselineMethod `json:"baseline_method"`
}

// Savings is the delta this entry would contribute if billable.
func (d VerifiedDelta) Savings() float64 { return d.BaselineSUM - d.OptimizedSUM }

// Billable reports whether this entry may contribute to a gainshare charge, and names WHY NOT when it
// may not.
//
// Every condition is stated as its own branch with its own reason, rather than folded into one boolean,
// because "why was this saving not billed" is a question a customer will ask and a support engineer
// must be able to answer without reading this file.
func (d VerifiedDelta) Billable() (bool, string) {
	switch {
	case d.Estimated:
		return false, "estimated, not measured — gainshare bills verified savings only"
	case d.Verdict.GateResult != verification.GatePass:
		return false, "the P5.5 verification gate did not pass (" + string(d.Verdict.GateResult) + ")"
	case !d.Verdict.HeldOut:
		return false, "no held-out split was formed, so the delta generalizes unproven"
	case !d.Verdict.Significant:
		return false, "the measured delta is not statistically significant"
	case !d.Verdict.RegressionPass:
		return false, "the change regressed another cluster"
	case !d.Merged:
		return false, "verified but never merged — a saving is not real until the change ships (ADR-001)"
	case d.MergeCommit == "":
		return false, "merged with no recorded merge commit, so the saving cannot be attributed to a git fact"
	case !d.Baseline.Complete():
		return false, "the baseline + holdout methodology is not reconstructable, so the figure is not auditable"
	case d.Savings() <= 0:
		return false, "the change did not reduce spend under management"
	}
	return true, ""
}

// VerifiedDeltaLedger is the P5.5 ledger, seen from billing. READ-ONLY: gainshare must never be able to
// write the evidence it bills on.
type VerifiedDeltaLedger interface {
	// VerifiedDeltas returns every ledger entry attributed to the customer in the period — INCLUDING
	// the un-billable ones. Returning only the billable entries would hide the estimates and un-merged
	// proposals, and those are exactly what the product must be able to show as "not billed".
	VerifiedDeltas(customerID, period string) ([]VerifiedDelta, error)
	// ByRef resolves one entry, so a billed saving can be traced back to its evidence.
	ByRef(ref string) (VerifiedDelta, bool)
	Describe() string
}

// BillableSavings is the persisted result — one row per {customer, period}.
type BillableSavings struct {
	CustomerID   string  `json:"customer_id"`
	Period       string  `json:"period"`
	BaselineSUM  float64 `json:"baseline_sum"`
	OptimizedSUM float64 `json:"optimized_sum"`
	Savings      float64 `json:"savings"`
	// VerifiedDeltaRefs / MergeCommits are the evidence. A row with a non-zero saving and no refs is
	// rejected by both the store and the database.
	VerifiedDeltaRefs []string `json:"verified_delta_refs"`
	MergeCommits      []string `json:"merge_commits"`
	// Excluded names the entries that contributed zero, with their reason. Kept because "we looked at
	// this and did not bill it" is the claim the trust story rests on, and an invisible exclusion is
	// indistinguishable from an oversight.
	Excluded  []ExcludedSaving `json:"excluded,omitempty"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// ExcludedSaving is one entry that was considered and contributed zero.
type ExcludedSaving struct {
	Ref           string  `json:"ref"`
	Reason        string  `json:"reason"`
	WouldHaveBeen float64 `json:"would_have_been"`
}

// Savings errors.
var (
	// ErrNoVerifiedSavings means nothing in the period is billable. It is a normal outcome, not a
	// failure — most periods have none.
	ErrNoVerifiedSavings = errors.New("metering: no verified, merged savings in the period")
	// ErrUntraceableSavings is returned when a savings figure carries no evidence. It is a hard error:
	// a billed saving that cannot name its verification and merge is the exact thing this design forbids.
	ErrUntraceableSavings = errors.New("metering: refusing a billable-savings record with no verified-delta refs or merge commits")
)

// SavingsStore persists `billable_savings`, keyed {customer, period}.
type SavingsStore interface {
	// Upsert writes the row for one {customer, period}. Keyed like the usage records, for the same
	// reason: a second row for a period is a second gainshare charge waiting to happen.
	Upsert(bs BillableSavings) (BillableSavings, error)
	Get(customerID, period string) (BillableSavings, error)
}

// MemSavingsStore is the in-memory `billable_savings` table.
type MemSavingsStore struct {
	mu   sync.Mutex
	rows map[string]BillableSavings
}

// NewMemSavingsStore builds an empty savings store.
func NewMemSavingsStore() *MemSavingsStore {
	return &MemSavingsStore{rows: map[string]BillableSavings{}}
}

func savingsKey(customerID, period string) string { return customerID + "\x00" + period }

// Upsert writes one {customer, period} row, refusing an untraceable one.
func (s *MemSavingsStore) Upsert(bs BillableSavings) (BillableSavings, error) {
	if bs.CustomerID == "" || bs.Period == "" {
		return BillableSavings{}, errors.New("metering: billable savings needs a customer and a period")
	}
	if bs.Savings < 0 {
		return BillableSavings{}, fmt.Errorf("metering: refusing a negative billable saving (%v)", bs.Savings)
	}
	if bs.Savings > 0 && (len(bs.VerifiedDeltaRefs) == 0 || len(bs.MergeCommits) == 0) {
		return BillableSavings{}, ErrUntraceableSavings
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[savingsKey(bs.CustomerID, bs.Period)] = bs
	return bs, nil
}

// Get returns one row.
func (s *MemSavingsStore) Get(customerID, period string) (BillableSavings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs, ok := s.rows[savingsKey(customerID, period)]
	if !ok {
		return BillableSavings{}, fmt.Errorf("metering: no billable savings for %s/%s", customerID, period)
	}
	return bs, nil
}

// Rows is how many {customer, period} savings rows exist.
func (s *MemSavingsStore) Rows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

// ComputeBillableSavings reads the P5.5 verified-delta ledger and returns what may be billed for a
// customer-period, together with the evidence and the exclusions.
//
// It is a PURE function of the ledger: it writes nothing, and it invents nothing. An empty ledger, or a
// ledger of only estimates and un-merged proposals, yields zero — with every exclusion named.
func ComputeBillableSavings(ledger VerifiedDeltaLedger, customerID string, p Period) (BillableSavings, error) {
	entries, err := ledger.VerifiedDeltas(customerID, p.ID)
	if err != nil {
		return BillableSavings{}, fmt.Errorf("metering: read verified-delta ledger for %s/%s: %w", customerID, p.ID, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Ref < entries[j].Ref })

	bs := BillableSavings{CustomerID: customerID, Period: p.ID}
	for _, e := range entries {
		if ok, why := e.Billable(); !ok {
			bs.Excluded = append(bs.Excluded, ExcludedSaving{Ref: e.Ref, Reason: why, WouldHaveBeen: e.Savings()})
			continue
		}
		bs.BaselineSUM += e.BaselineSUM
		bs.OptimizedSUM += e.OptimizedSUM
		bs.VerifiedDeltaRefs = append(bs.VerifiedDeltaRefs, e.Ref)
		bs.MergeCommits = append(bs.MergeCommits, e.MergeCommit)
	}
	bs.Savings = bs.BaselineSUM - bs.OptimizedSUM
	return bs, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// MemVerifiedDeltas — the reference ledger
// ─────────────────────────────────────────────────────────────────────────────

// MemVerifiedDeltas is an in-memory VerifiedDeltaLedger for the demo and the tests. It holds entries
// exactly as P5.5 would record them — verified and unverified, merged and un-merged, measured and
// estimated — because a ledger that only contains billable entries could not prove that the
// un-billable ones are excluded.
type MemVerifiedDeltas struct {
	mu      sync.RWMutex
	entries []VerifiedDelta
	err     error
}

// NewMemVerifiedDeltas builds an empty ledger.
func NewMemVerifiedDeltas() *MemVerifiedDeltas { return &MemVerifiedDeltas{} }

// Put appends one entry.
func (m *MemVerifiedDeltas) Put(d VerifiedDelta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, d)
}

// SetErr makes the ledger unreadable — the fail-closed seam.
func (m *MemVerifiedDeltas) SetErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// VerifiedDeltas returns the customer's entries for a period.
func (m *MemVerifiedDeltas) VerifiedDeltas(customerID, period string) ([]VerifiedDelta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, m.err
	}
	var out []VerifiedDelta
	for _, e := range m.entries {
		if e.CustomerID == customerID && e.Period == period {
			out = append(out, e)
		}
	}
	return out, nil
}

// ByRef resolves one entry.
func (m *MemVerifiedDeltas) ByRef(ref string) (VerifiedDelta, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		if e.Ref == ref {
			return e, true
		}
	}
	return VerifiedDelta{}, false
}

// Describe names the ledger.
func (m *MemVerifiedDeltas) Describe() string { return "memory:p5.5-verified-delta-ledger" }

// RecordBillableSavings computes and persists a period's billable savings.
func (m *Meter) RecordBillableSavings(ledger VerifiedDeltaLedger, savings SavingsStore, customerID string, p Period) (BillableSavings, error) {
	bs, err := ComputeBillableSavings(ledger, customerID, p)
	if err != nil {
		return BillableSavings{}, err
	}
	bs.UpdatedAt = m.now().UTC()
	return savings.Upsert(bs)
}
