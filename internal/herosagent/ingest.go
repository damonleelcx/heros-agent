package herosagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ingest.go accepts a CUSTOMER-PLACED result (tasks 7.3, 7.4, 7.5).
//
// # Why the platform re-validates something the customer's runner already validated
//
// The customer-side runner is this package's Runner, so it applied the floor, checked the node
// vocabulary and honoured D3's fence before it submitted anything. Re-doing that here looks redundant
// and is not, for one reason: THE PLATFORM DOES NOT KNOW THAT IT DID. What arrives is an authenticated
// HTTP request, not a proof of provenance. It may come from a CLI three versions old, from a CLI
// somebody patched, or from `curl`. A floor enforced only where the number is produced is not a floor;
// it is a convention, and the one participant it fails to bind is the one that benefits from ignoring
// it.
//
// 🚫 So this is NOT a second arbitration path (task 4.6's rule). It is the SAME rules — the same floor,
// the same closed vocabularies, the same D3 fence — evaluated on the side that has something to lose.
// Every refusal becomes a stored abstention with the same closed-enum cause the runner would have used,
// so a submission's rejections aggregate beside a platform-side run's.

// SubmittedEdge is one edge as a customer submitted it.
//
// A type of this package's own rather than `runlink.WireIREdge`, because `internal/runlink` must not
// import this package — the egress package does not import the thing whose output it constrains — and
// a dependency in the other direction would put the wire's shape in the middle of the ingest's rules.
// `internal/api` owns the mapping, which is the layer that already sees both.
type SubmittedEdge struct {
	From string
	To   string
	Kind string
	// Author is `frontend` or `heros` on this boundary. Empty reads as `legacy` — see WireIREdge.Author
	// for why the absence is not filled in with `frontend`.
	Author string
	// Confidence is present only on a `heros`-authored edge. A ZERO here is "no confidence was stated",
	// which is a failure to meet the contract rather than a confident zero — the runner draws the same
	// distinction with a pointer, and the wire's `omitempty` collapses them, so an ingested zero is
	// treated as the weaker reading.
	Confidence float64
}

// SubmittedAbstention is one decline the customer's runner recorded.
type SubmittedAbstention struct {
	Subject    string
	Cause      string
	Confidence *float64
}

// Submission is a customer-side result, already scoped to the AUTHENTICATED tenant by the handler.
type Submission struct {
	TenantID        string
	WorkflowID      string
	SourceRevision  string
	AgentConfigHash string
	// Placement is the tenant's setting as the platform holds it. 🔴 Never anything the submitter said:
	// a payload that could name its own placement could enable itself.
	Placement Placement
	// NodeIDs is the vocabulary the submitted edges are checked against — the node ids in the same
	// payload. An edge naming something outside it is recorded and not written, exactly as the runner
	// does with a model that names a node the IR does not carry.
	NodeIDs []string
	Edges   []SubmittedEdge
	// Abstentions are what the customer's runner already declined. Carried through so a customer-placed
	// tenant's stored inference has the same shape as a platform-placed one.
	Abstentions []SubmittedAbstention
}

// HasAgentFacts reports whether this submission claims anything the agent authored.
//
// 🔴 The test is over the AUTHOR field and the hash and the abstentions together, not over the hash
// alone. A submission carrying `heros`-authored edges and no hash is the case that must be refused
// rather than silently treated as a plain structure upload, and a test on the hash would classify it as
// "nothing to see" — which is the failure reading like the safe one.
func (s Submission) HasAgentFacts() bool {
	if strings.TrimSpace(s.AgentConfigHash) != "" || len(s.Abstentions) > 0 {
		return true
	}
	for _, e := range s.Edges {
		if e.Author == string(authorHEROS) {
			return true
		}
	}
	return false
}

// authorFrontend and authorHEROS are the two authors this boundary accepts. Typed off Placement's
// neighbour vocabulary in `internal/discovery`; spelled here as constants so no call site types them.
type submittedAuthor string

const (
	authorFrontend submittedAuthor = "frontend"
	authorHEROS    submittedAuthor = "heros"
)

// VersionLookup is the half of VersionStore an ingest needs: does this hash name a published version.
type VersionLookup interface {
	Get(ctx context.Context, configHash string) (Version, bool, error)
}

// Ingester accepts customer-placed results.
type Ingester struct {
	versions   VersionLookup
	inferences InferenceStore
	floor      float64
	nowMS      func() int64
}

// NewIngester wires the ingest. Every dependency is required, and the refusals say why.
func NewIngester(versions VersionLookup, inferences InferenceStore, floor float64, nowMS func() int64) (*Ingester, error) {
	switch {
	case versions == nil:
		// 🔴 FAIL CLOSED, and this is the one worth reading twice. Without a version store the platform
		// cannot tell a published `config_hash` from a string somebody typed, so it would be storing facts
		// it can never re-derive, attributed to a definition that may not exist. An ingest that accepted
		// them "because verification is unavailable" is an ingest whose fence is off exactly when the
		// deployment is misconfigured.
		return nil, fmt.Errorf("%w: an agent version store is required — without one an ingest cannot "+
			"tell a published config_hash from a string, and would store facts nothing can re-derive",
			ErrInvalidDefinition)
	case inferences == nil:
		return nil, fmt.Errorf("%w: an inference store is required", ErrInvalidDefinition)
	case floor <= 0 || floor > 1:
		return nil, fmt.Errorf("%w: the confidence floor must be in (0,1]; got %v. A floor nobody set is "+
			"a floor that is zero, and a zero floor accepts every submitted number", ErrInvalidDefinition, floor)
	case nowMS == nil:
		return nil, fmt.Errorf("%w: a clock is required", ErrInvalidDefinition)
	}
	return &Ingester{versions: versions, inferences: inferences, floor: floor, nowMS: nowMS}, nil
}

// IngestResult is what an accepted submission produced.
type IngestResult struct {
	InferenceID string
	// Written is the edges that were STORED — the submitted ones that survived every check.
	Written []ProvenancedEdge
	// Abstentions is what was declined: the ones the customer's runner reported, plus the ones this
	// ingest determined itself. Both, and not distinguished — an abstention is an abstention regardless
	// of which side of the boundary noticed, and marking them differently would invite a reader to
	// discount one kind.
	Abstentions []Abstention
	// Code is the outcome a surface renders.
	Code Code
}

// Accept validates and stores a customer-placed submission.
//
// 🔴 Nothing is written on any refusal. Every early return below leaves the platform in the state it was
// in — a partially-ingested inference would be a graph nobody can reproduce from its key, which is the
// same reason the runner writes no partial IR when a budget is exceeded.
func (i *Ingester) Accept(ctx context.Context, sub Submission) (IngestResult, error) {
	// Task 7.5 — `disabled` refuses ingest, and so does `platform`. The customer runner's own gate is
	// the same call; this is the platform's, and they are separate because the CLI's answer is a claim
	// and this one is a decision.
	if err := HostCustomer.MayRun(sub.Placement); err != nil {
		code := CodeDisabled
		if sub.Placement != PlacementDisabled {
			code = CodeWrongPlacement
		}
		return IngestResult{Code: code}, err
	}

	hash := strings.TrimSpace(sub.AgentConfigHash)
	if hash == "" {
		// Reached only with agent facts present — the handler checks HasAgentFacts before calling.
		return IngestResult{Code: CodeOutputRejected}, fmt.Errorf(
			"%w: this submission carries facts authored by the agent and names no `agent_config_hash`. "+
				"A fact whose definition cannot be named is one nothing can re-derive, re-run or compare "+
				"against a later version", ErrUnattributedInference)
	}

	version, ok, err := i.versions.Get(ctx, hash)
	if err != nil {
		return IngestResult{Code: CodeProviderFailed}, err
	}
	if !ok {
		// 🔴 NAMED, per task 7.4 — and the sentence points at the likeliest cause. A CLI submitting a
		// hash this deployment has never published is almost always a CLI that fetched its definition
		// from somewhere else, or one running against a deployment that has since republished.
		return IngestResult{Code: CodeOutputRejected}, fmt.Errorf(
			"%w: %s. Nothing was written. The definition a customer-side analysis runs is fetched from "+
				"this platform, so a hash it does not carry means the two are looking at different "+
				"platforms — or that the analysis predates a republish",
			ErrUnknownAgentVersion, confighashDisplay(hash))
	}
	if !version.Active() {
		// A published-but-not-active version is a real row, so the check above passes. Accepting its
		// output would let a customer keep submitting under a definition the operator stood down —
		// including one that FAILED its rehearsal, which is the whole point of the gate.
		return IngestResult{Code: CodeRehearsalPending}, fmt.Errorf(
			"%w: %s is published and is not the active definition, so its output is not accepted. "+
				"Re-run the analysis against the definition this platform is serving",
			ErrRehearsalNotPassed, confighashDisplay(hash))
	}

	known := map[string]bool{}
	for _, id := range sub.NodeIDs {
		known[id] = true
	}
	// Pairs a FRONTEND established in this same submission. D3 fence 1, evaluated here because the
	// submitter is the only one who saw the source and the platform is the only one that has to live
	// with the graph.
	established := map[[2]string]bool{}
	for _, e := range sub.Edges {
		if e.Author != string(authorHEROS) {
			established[[2]string{e.From, e.To}] = true
			established[[2]string{e.To, e.From}] = true
		}
	}

	res := IngestResult{Code: CodeOK, Written: []ProvenancedEdge{}, Abstentions: []Abstention{}}
	for _, a := range sub.Abstentions {
		cause, err := ParseAbstentionReason(a.Cause)
		if err != nil {
			return IngestResult{Code: CodeOutputRejected}, err
		}
		res.Abstentions = append(res.Abstentions, Abstention{
			Subject: a.Subject, Reason: cause, Confidence: a.Confidence,
		})
	}

	decline := func(subject string, reason AbstentionReason, conf *float64) {
		res.Abstentions = append(res.Abstentions, Abstention{Subject: subject, Reason: reason, Confidence: conf})
	}
	for _, e := range sub.Edges {
		if e.Author != string(authorHEROS) {
			// A frontend-authored edge is the P29 structure. It is not this ingest's to keep or decline.
			continue
		}
		subject := e.From + "→" + e.To
		c := e.Confidence
		switch {
		case !known[e.From] || !known[e.To]:
			decline(subject, AbstainUnknownNode, &c)
		case e.Kind != "data" && e.Kind != "control":
			// 🚫 Rejected, never repaired — a `kind` of "dataflow" is not coerced to "data" here either.
			decline(subject, AbstainOutOfVocabulary, &c)
		case established[[2]string{e.From, e.To}]:
			decline(subject, AbstainFrontendOwns, &c)
		case c <= 0:
			// No confidence stated. Distinct from below-floor: one is silence, the other is uncertainty.
			decline(subject, AbstainNoCandidate, nil)
		case c < i.floor:
			// 🔴 TASK 7.4 — THE FLOOR, APPLIED TO A NUMBER SOMEBODY ELSE PRODUCED. Not written, recorded.
			decline(subject, AbstainBelowFloor, &c)
		default:
			res.Written = append(res.Written, ProvenancedEdge{
				From: e.From, To: e.To, Kind: e.Kind, Confidence: c,
			})
		}
	}

	sort.SliceStable(res.Abstentions, func(a, b int) bool {
		if res.Abstentions[a].Subject != res.Abstentions[b].Subject {
			return res.Abstentions[a].Subject < res.Abstentions[b].Subject
		}
		return res.Abstentions[a].Reason < res.Abstentions[b].Reason
	})

	stored := Stored{
		InferenceID:     defaultInferenceID(sub.WorkflowID, sub.SourceRevision, hash),
		TenantID:        sub.TenantID,
		WorkflowID:      sub.WorkflowID,
		SourceRevision:  sub.SourceRevision,
		AgentConfigHash: hash,
		// The submission ran on the customer's machine, and that is what the graph attributes (task 8.6).
		Placement:   PlacementCustomer,
		Edges:       res.Written,
		Abstentions: res.Abstentions,
		CreatedAtMS: i.nowMS(),
		// 🚫 No token counts. The customer spent their OWN credential, so the platform has no meter
		// reading to record and must not invent one — a zero here would render as "this analysis was
		// free", which is a claim about somebody else's bill.
	}
	if err := i.inferences.Put(ctx, stored); err != nil {
		return IngestResult{Code: CodeProviderFailed}, err
	}
	res.InferenceID = stored.InferenceID
	return res, nil
}

// ParseAbstentionReason reads a submitted cause, refusing anything outside the closed enum.
func ParseAbstentionReason(s string) (AbstentionReason, error) {
	for _, r := range AbstentionReasons() {
		if string(r) == s {
			return r, nil
		}
	}
	return "", fmt.Errorf("%w: %q is not an abstention cause. It is one of %s",
		ErrInvalidDefinition, s, strings.Join(abstentionReasonNames(), ", "))
}

// AbstentionReasons returns the closed set, so a consumer's switch can be proved exhaustive and the
// drift test can compare it against the wire's copy.
func AbstentionReasons() []AbstentionReason {
	return []AbstentionReason{
		AbstainBelowFloor, AbstainNoCandidate, AbstainOutOfVocabulary,
		AbstainUnknownNode, AbstainFrontendOwns,
	}
}

func abstentionReasonNames() []string {
	out := make([]string, 0, len(AbstentionReasons()))
	for _, r := range AbstentionReasons() {
		out = append(out, string(r))
	}
	return out
}
