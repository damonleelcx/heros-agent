package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

// p7.go mounts the P7 billing/usage surface, following the p6.go pattern: one Mount method, a nil-able
// source interface, 503 when unmounted, the page embedded.
//
// The surface's load-bearing rules are structural at the HTTP boundary:
//   - NO AMOUNT IS COMPUTED HERE. Every figure the client renders comes from the server, and the
//     server gets it from the meter, the config store, or the provider. The client has no arithmetic
//     and no currency literal — a grep for one is a defect (task 7.4).
//   - EVERY DENIAL CARRIES ITS REASON AND UPGRADE PATH. The gate's Decision is passed through
//     verbatim; the client renders what it is handed rather than composing its own explanation, so the
//     paywall copy cannot drift from what the server actually enforced (task 7.1 / FR7).
//   - GAINSHARE LINES CARRY THEIR EVIDENCE. A gainshare invoice line without its verified-delta refs
//     and merge commits is not rendered as a normal line — the view model has nowhere to put one.
//   - DUNNING STATE IS THE PROVIDER'S. The view mirrors it; it never computes a payment status.

//go:embed static/p7.html
var p7HTML []byte

// P7Source is what the API asks of the billing capability: the whole billing/usage read model for one
// customer-period, plus the two actions the surface exposes (gainshare consent and its revocation).
//
// One read method rather than five: the page answers four questions that must agree with each other,
// and four independently-fetched documents is how a UI ends up showing a SUM that disagrees with the
// invoice it is next to.
type P7Source interface {
	// Billing returns the whole surface for a customer-period. An empty period means "the current one".
	Billing(customerID, period string) (BillingView, bool)
	// SetGainshareConsent records or revokes consent and returns the refreshed view.
	SetGainshareConsent(customerID string, consented bool) (BillingView, error)
	// Periods lists the periods this customer has any record for, newest first, for the period picker.
	Periods(customerID string) []string
}

// BillingView is the whole P7 read model for one customer-period.
type BillingView struct {
	CustomerID string `json:"customer_id"`
	Period     string `json:"period"`
	// Periods the customer has records for, newest first.
	AvailablePeriods []string `json:"available_periods,omitempty"`

	// ── Question 1: what am I spending? ───────────────────────────────────────
	// SUM is the period's spend under management, with its unit. The UNIT comes from the server: the
	// client must not assume a currency.
	SUM     float64 `json:"sum"`
	SUMUnit string  `json:"sum_unit"`
	// SUMTrend is the per-period history the chart renders. Empty is a real state (a new customer).
	SUMTrend []PeriodPoint `json:"sum_trend,omitempty"`
	// Meters is every metered quantity for the period, with the plan's allowance where one is set.
	Meters []MeterView `json:"meters"`

	// ── Question 2: what am I on, and what does it entitle? ───────────────────
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// PlanConfigVersion is the published configuration this plan resolved under — what makes a closed
	// period explainable after the next config publish.
	PlanConfigVersion string           `json:"plan_config_version"`
	Entitlements      []EntitlementRow `json:"entitlements"`

	// ── Question 3: what am I being charged, and why? ─────────────────────────
	Invoice InvoiceView `json:"invoice"`
	// State is the PROVIDER's billing state, mirrored. Never computed here.
	State BillingStateView `json:"state"`

	// ── Question 4: what did you actually save me? ────────────────────────────
	Savings SavingsView `json:"savings"`

	// ── First-class states (task 7.4) ─────────────────────────────────────────
	// Denial, when set, is the entitlement gate's verbatim decision for the surface the customer just
	// tried to reach. Passed through rather than re-composed, so the paywall copy cannot drift from
	// what the server enforced.
	Denial *DenialView `json:"denial,omitempty"`
	// Drift is surfaced reconciliation disagreement. The customer finds out from us, not from their
	// statement.
	Drift []DriftView `json:"drift,omitempty"`
	// Empty distinguishes "no usage recorded yet" from a measured zero. They look identical in a number
	// and mean opposite things.
	Empty bool `json:"empty"`
}

// PeriodPoint is one period's figure for the trend charts.
type PeriodPoint struct {
	Period string `json:"period"`
	// SUM is the period's spend under management. Baseline/Optimized are the verified-savings
	// comparison; both are zero when nothing was verified that period.
	SUM       float64 `json:"sum"`
	Baseline  float64 `json:"baseline_sum"`
	Optimized float64 `json:"optimized_sum"`
}

// MeterView is one metered quantity with the plan's allowance.
type MeterView struct {
	Metric string  `json:"metric"`
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	// Allowed is the plan's allowance; Unlimited says there is none. An UNSET allowance must never be
	// rendered as zero — "unlimited" and "none" are opposite states with the same numeric shape.
	Allowed   float64 `json:"allowed"`
	Unlimited bool    `json:"unlimited"`
	Over      bool    `json:"over"`
	// ReportedToProvider / ProviderUsageRef show the hand-off, so a customer can see what was sent.
	ReportedToProvider bool   `json:"reported_to_provider"`
	ProviderUsageRef   string `json:"provider_usage_ref,omitempty"`
}

// EntitlementRow is one surface and whether the active plan includes it.
type EntitlementRow struct {
	Feature  string `json:"feature"`
	Label    string `json:"label"`
	Included bool   `json:"included"`
	// Reason / UpgradePlanName explain a "not included" row — the paywall, inline.
	Reason          string `json:"reason,omitempty"`
	UpgradePlan     string `json:"upgrade_plan,omitempty"`
	UpgradePlanName string `json:"upgrade_plan_name,omitempty"`
}

// InvoiceView is the period's invoice, separated by kind.
type InvoiceView struct {
	InvoiceRef string `json:"invoice_ref,omitempty"`
	// Status is the provider's invoice status, verbatim.
	Status string      `json:"status,omitempty"`
	Lines  []LineView  `json:"lines"`
	Totals []TotalView `json:"totals,omitempty"`
}

// LineView is one invoice line. Every line names its BASIS — the platform record that justified it —
// so "traceable to the usage that justified it" is a property of the payload, not of the renderer.
type LineView struct {
	Kind  string `json:"kind"` // subscription | metered | gainshare | credit | refund
	Label string `json:"label"`
	Basis string `json:"basis"`
	// AmountRef is the provider's OPAQUE amount handle. There is deliberately no amount: the platform
	// does not hold one, and a client that invented one would be showing a number nobody can justify.
	AmountRef string  `json:"amount_ref,omitempty"`
	Quantity  float64 `json:"quantity"`
	Unit      string  `json:"unit,omitempty"`
	ChargeRef string  `json:"charge_ref,omitempty"`
	// Evidence is present on GAINSHARE lines only: the verified deltas and merge commits behind the
	// charge. A gainshare line with none is a defect, not a rendering choice.
	Evidence []EvidenceView `json:"evidence,omitempty"`
	// Corrects names the line this one corrects, for credits and refunds.
	Corrects string `json:"corrects,omitempty"`
}

// TotalView is one per-kind subtotal, expressed as a QUANTITY (never an amount).
type TotalView struct {
	Kind     string  `json:"kind"`
	Label    string  `json:"label"`
	Lines    int     `json:"lines"`
	Quantity float64 `json:"quantity"`
}

// EvidenceView is one piece of a gainshare line's proof: a verified delta or the merge that shipped it.
type EvidenceView struct {
	Kind string `json:"kind"` // verified_delta | merge
	Ref  string `json:"ref"`
	// Label is what the customer reads; Link is where it goes (the verified delta or the merged PR).
	Label string `json:"label"`
	Link  string `json:"link,omitempty"`
	// Method describes the FIXED baseline + holdout methodology behind a verified delta, so the
	// consent screen's promise ("we show you the method") is satisfiable from the invoice.
	Method *MethodView `json:"method,omitempty"`
}

// MethodView is the reconstructable baseline + holdout methodology.
type MethodView struct {
	ID               string `json:"id"`
	EvalSetHash      string `json:"eval_set_hash"`
	HoldoutCases     int    `json:"holdout_cases"`
	GeneratingCases  int    `json:"generating_cases"`
	Seeds            int    `json:"seeds"`
	BaselineConfig   string `json:"baseline_config_hash"`
	CandidateConfig  string `json:"candidate_config_hash"`
	SignificanceNote string `json:"significance_note,omitempty"`
}

// SavingsView is the verified-savings story, and — just as importantly — the estimated savings that
// were NOT billed. Showing only the billed ones would leave the trust claim unevidenced.
type SavingsView struct {
	// Consented / ConsentedAt are the gainshare contract state.
	Consented   bool   `json:"gainshare_consent"`
	ConsentedAt string `json:"consented_at,omitempty"`
	// ConsentAvailable is false when the plan has no gainshare price reference — the control is then
	// absent rather than present-and-broken.
	ConsentAvailable bool `json:"consent_available"`

	BaselineSUM  float64 `json:"baseline_sum"`
	OptimizedSUM float64 `json:"optimized_sum"`
	Verified     float64 `json:"verified_savings"`
	Unit         string  `json:"unit"`
	// Billed rows are verified AND merged. Excluded rows were considered and contributed zero, each
	// with the reason.
	Billed   []SavingRow   `json:"billed,omitempty"`
	Excluded []ExcludedRow `json:"excluded,omitempty"`
	// NoneVerified distinguishes "nothing verified and merged this period" from a measured zero.
	NoneVerified bool `json:"none_verified"`
}

// SavingRow is one verified, merged saving with its evidence.
type SavingRow struct {
	Ref          string      `json:"ref"`
	BaselineSUM  float64     `json:"baseline_sum"`
	OptimizedSUM float64     `json:"optimized_sum"`
	Savings      float64     `json:"savings"`
	MergeCommit  string      `json:"merge_commit"`
	Method       *MethodView `json:"method,omitempty"`
}

// ExcludedRow is one saving that was considered and NOT billed, with the reason.
type ExcludedRow struct {
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
	// WouldHaveBeen is what it would have contributed if billable. Shown so the customer can see the
	// size of what the platform declined to bill — the trust claim, quantified.
	WouldHaveBeen float64 `json:"would_have_been"`
}

// BillingStateView mirrors the provider's dunning state.
type BillingStateView struct {
	InvoiceStatus      string `json:"invoice_status,omitempty"`
	SubscriptionStatus string `json:"subscription_status,omitempty"`
	PaymentFailed      bool   `json:"payment_failed"`
	PastDue            bool   `json:"past_due"`
	// Guidance is what the customer should do next. A dunning banner with no next step is an alarm.
	Guidance string `json:"guidance,omitempty"`
}

// DenialView is the entitlement gate's decision, passed through verbatim.
type DenialView struct {
	Feature         string `json:"feature"`
	FeatureLabel    string `json:"feature_label"`
	Reason          string `json:"reason"`
	ReasonCode      string `json:"reason_code"`
	UpgradePlan     string `json:"upgrade_plan,omitempty"`
	UpgradePlanName string `json:"upgrade_plan_name,omitempty"`
	// Limit is set on an over-limit denial: the meter, its allowance, and the observed value.
	Limit *DenialLimitView `json:"limit,omitempty"`
}

// DenialLimitView is the meter behind an over-limit denial.
type DenialLimitView struct {
	Limit    string  `json:"limit"`
	Label    string  `json:"label"`
	Allowed  float64 `json:"allowed"`
	Observed float64 `json:"observed"`
	Period   string  `json:"period"`
}

// DriftView is one surfaced reconciliation disagreement.
type DriftView struct {
	Kind             string  `json:"kind"`
	Metric           string  `json:"metric"`
	Detail           string  `json:"detail"`
	PlatformQuantity float64 `json:"platform_quantity"`
	ProviderQuantity float64 `json:"provider_quantity"`
}

// MountP7 registers the billing/usage UI, its read endpoint, and the consent actions.
func (s *Server) MountP7(src P7Source) {
	s.p7 = src
	s.Mux.HandleFunc("GET /p7/billing", s.handleP7UI)
	s.Mux.HandleFunc("GET /api/p7/customers/{customer_id}/billing", s.handleP7Billing)
	s.Mux.HandleFunc("POST /api/p7/customers/{customer_id}/gainshare-consent", s.handleP7Consent)
}

func (s *Server) handleP7UI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(p7HTML)
}

func (s *Server) handleP7Billing(w http.ResponseWriter, r *http.Request) {
	if s.p7 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p7 billing surface is not mounted on this server"})
		return
	}
	v, ok := s.p7.Billing(r.PathValue("customer_id"), r.URL.Query().Get("period"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no billing account for " + r.PathValue("customer_id")})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleP7Consent(w http.ResponseWriter, r *http.Request) {
	if s.p7 == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the p7 billing surface is not mounted on this server"})
		return
	}
	var body struct {
		Consented bool `json:"consented"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid consent payload"})
		return
	}
	v, err := s.p7.SetGainshareConsent(r.PathValue("customer_id"), body.Consented)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}
