package adminops

import (
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/adminaudit"
)

// displacement.go measures the phase by IMPERSONATION DISPLACED, not by pages shipped (P26 D6, task 8.3).
//
// # Why this metric and not "the surfaces exist"
//
// A refresh phase's natural metric is that the pages are there, which four pages nobody opens satisfies.
// `impersonate.read` is reason-required, time-bounded, read-scoped and fully audited — the marks of a
// control designed to be RARE — and it had become the only tool that could show a tenant's delivery
// state, axis coverage or refusal causes. Every such session is a data-protection cost paid for a
// missing aggregate.
//
// So the headline number is the share of impersonation sessions whose RECORDED REASON is a routine
// lookup an aggregate now answers. It is computed from the audit records that already exist, before and
// after, and it can say the phase failed — which is the point of choosing it.
//
// # 🔴 Why the classifier is deliberately crude, and why that is honest
//
// It matches an operator's own words against the subjects the new surfaces cover. It will miss a session
// whose reason was written in unusual terms, and it cannot read intent. Both directions of error are
// possible and neither is hidden: `Unclassified` is reported alongside, so a reader can see how much of
// the corpus the number rests on rather than being handed a ratio with no denominator.
//
// A cleverer classifier would be a worse one here. The number's job is to be checkable by the person it
// is shown to, and a rule you can read in ten seconds is checkable in a way a model is not.

// DisplaceableSubject is one routine lookup a P26 surface now answers without impersonation.
type DisplaceableSubject struct {
	// ID is the stable identifier; Surface is where the question is answered now.
	ID      string
	Surface string
	// Terms are the words an operator writes when the reason is this subject. Lower-case, matched as
	// substrings against the recorded reason.
	Terms []string
}

// DisplaceableSubjects is what this phase claims to have displaced. It is deliberately SHORT: a long
// list would inflate the number by claiming credit for lookups these surfaces do not answer.
func DisplaceableSubjects() []DisplaceableSubject {
	return []DisplaceableSubject{
		{ID: "delivery-state", Surface: "/delivery", Terms: []string{
			"delivery", "pull request", "did the pr", "was it merged", "merge state", "rollout"}},
		{ID: "axis-coverage", Surface: "/axes", Terms: []string{
			"coverage", "why was this refused", "refusal", "materializer", "which axis"}},
		{ID: "release-trust", Surface: "/releases", Terms: []string{
			"which version", "signing key", "signed with", "smoke", "install channel"}},
		{ID: "consent-state", Surface: "/oversight", Terms: []string{
			"accepted the terms", "consent", "legal version", "re-acceptance"}},
		{ID: "link-coverage", Surface: "/billing", Terms: []string{
			"link coverage", "unlinked runs", "how much did we observe"}},
	}
}

// DisplacementReading is one measurement of the headline metric.
type DisplacementReading struct {
	// Label names the measurement — "before" or "after", or a period.
	Label string `json:"label"`
	// Sessions is every impersonation session start in the window.
	Sessions int `json:"sessions"`
	// Displaceable is how many of them recorded a reason a P26 surface now answers.
	Displaceable int `json:"displaceable"`
	// BySubject breaks that down, so a reader can see WHICH surface is doing the work — or that one
	// subject is carrying the whole number.
	BySubject map[string]int `json:"by_subject"`
	// Unclassified is how many reasons matched no subject. 🔴 Reported, always: a ratio without its
	// unclassified remainder invites the reader to assume the remainder is zero.
	Unclassified int `json:"unclassified"`
	// Ratio is Displaceable / Sessions, or 0 when there were no sessions. A ratio over an empty corpus
	// is not a measurement, and `Sessions` beside it is what says so.
	Ratio float64 `json:"ratio"`
}

// MeasureDisplacement computes one reading from the impersonation audit records.
//
// It reads the SAME audit chain the console renders — no second collection path, and no new table (D7).
func MeasureDisplacement(label string, store adminaudit.Store) DisplacementReading {
	reading := DisplacementReading{Label: label, BySubject: map[string]int{}}
	subjects := DisplaceableSubjects()

	// 🔴 Deduplicated by impersonation id, because the command path writes its audit entry WRITE-AHEAD
	// and completes it after the effect — so one session produces more than one `impersonation.start`
	// row. Counting rows rather than sessions would have doubled the headline number in the direction
	// that flatters the phase, which is exactly the direction a metric must not drift in.
	seen := map[string]bool{}
	for _, e := range adminaudit.Select(store, adminaudit.Filter{Action: adminaudit.ActionImpersonationStart}) {
		id := e.Evidence["impersonation_id"]
		if id == "" {
			id = e.ImpersonationID
		}
		if id != "" {
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		reading.Sessions++
		reason := strings.ToLower(e.Reason)
		matched := ""
		for _, s := range subjects {
			for _, term := range s.Terms {
				if strings.Contains(reason, term) {
					matched = s.ID
					break
				}
			}
			if matched != "" {
				break
			}
		}
		if matched == "" {
			reading.Unclassified++
			continue
		}
		reading.Displaceable++
		reading.BySubject[matched]++
	}
	if reading.Sessions > 0 {
		reading.Ratio = float64(reading.Displaceable) / float64(reading.Sessions)
	}
	return reading
}

// DisplacementReport is the before/after comparison — the phase's headline.
type DisplacementReport struct {
	Before DisplacementReading `json:"before"`
	After  DisplacementReading `json:"after"`
	// Delta is After.Ratio - Before.Ratio. NEGATIVE is the outcome this phase wanted: fewer sessions
	// opened for a question a page now answers.
	Delta float64 `json:"delta"`
	// Verdict states what the numbers say, including when they say the phase did not work. It is
	// written here rather than by a reader, so "the number did not move" cannot be quietly reported as
	// "the number is stable".
	Verdict string `json:"verdict"`
	// Surfaces names the destinations credited, so the claim is checkable against the ledger.
	Surfaces []string `json:"surfaces"`
}

// CompareDisplacement builds the report and states the verdict, including a failing one.
func CompareDisplacement(before, after DisplacementReading) DisplacementReport {
	r := DisplacementReport{Before: before, After: after, Delta: after.Ratio - before.Ratio}
	seen := map[string]bool{}
	for _, s := range DisplaceableSubjects() {
		if !seen[s.Surface] {
			r.Surfaces = append(r.Surfaces, s.Surface)
			seen[s.Surface] = true
		}
	}
	sort.Strings(r.Surfaces)

	switch {
	case before.Sessions == 0:
		r.Verdict = "NO BASELINE: no impersonation sessions were recorded before this phase, so there is " +
			"nothing to measure a fall against. The metric is not satisfied by an absent denominator."
	case r.Delta < -0.001:
		r.Verdict = "DISPLACED: a smaller share of impersonation sessions is opened for a question these " +
			"surfaces now answer."
	case r.Delta > 0.001:
		r.Verdict = "WORSE: a LARGER share of impersonation sessions is opened for a question these " +
			"surfaces answer. Either the surfaces are not being used or they answer the wrong question."
	default:
		r.Verdict = "UNMOVED: the share is unchanged. The surfaces did not displace the impersonation " +
			"they were built to displace, and that is a result rather than a rounding error."
	}
	return r
}
