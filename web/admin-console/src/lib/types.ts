/**
 * types.ts mirrors the admin API's wire shapes.
 *
 * These are the SERVER's models, transcribed. The console adds no derived business fields and does no
 * arithmetic on them: every number it renders came from the platform, so the screen cannot disagree
 * with the system of record — and no plan price or numeric limit is ever computed here (FR28).
 */

export type Role = "support" | "billing_ops" | "platform_sre" | "superadmin";

export type CapabilityFriction = {
  capability: string;
  description: string;
  read_only: boolean;
  irreversible: boolean;
  typed_target: boolean;
  held_by: Role[];
};

export type ImpersonationView = {
  id: string;
  tenant_id: string;
  scope: string;
  reason: string;
  expires_at: string;
  remaining_seconds: number;
  banner: string;
};

export type AdminIdentity = {
  admin_id: string;
  roles: Role[];
  capabilities: string[];
  permission_map: Record<Role, string[]>;
  friction: CapabilityFriction[];
  session_expires_at: string;
  console: string;
  active_impersonation?: ImpersonationView;
  kill_switch_two_person_disarm: boolean;
  minimum_cohort: number;
};

export type TenantView = {
  tenant_id: string;
  plan_id: string;
  plan_name: string;
  plan_config_version: string;
  status: string;
  suspension_reason?: string;
  suspended_at?: string;
  quota_overrides?: Record<string, number>;
  autonomous_merges_halted: boolean;
  halt_reason?: string;
  gainshare_consent: boolean;
};

export type PlanOption = {
  plan_id: string;
  plan_name: string;
  rank: number;
  features: string[];
};

export type InvoiceLine = {
  event_id: string;
  period: string;
  type: string;
  kind?: string;
  status: string;
  quantity: number;
  provider_ref?: string;
  caused_by: string;
  reason?: string;
  created_at: string;
  /**
   * True when this row's quantity derives from SUM over LINKED runs.
   *
   * The mark exists so a coverage percentage shown beside the table does not appear to qualify a seat
   * count or a plan change, neither of which run linking has anything to say about.
   */
  sum_derived?: boolean;
};

export type GainshareLine = {
  event_id: string;
  period: string;
  verified_delta_refs?: string[];
  merge_commits?: string[];
  exception?: string;
};

/**
 * DerivedFigure is a SUM-derived figure and the link coverage that qualifies it.
 *
 * 🔴 `coverage: null` means UNKNOWN, and an unknown-coverage figure is NOT RENDERED — the surface says
 * coverage is unknown instead. The pairing is in the platform's type (`adminops.DerivedFigure`), so a
 * figure cannot reach this file without one. `value` is a STRING because the platform formatted it;
 * the console renders what it was sent and derives nothing.
 */
export type DerivedFigure = {
  value: string;
  coverage: number | null;
  source: string;
  basis?: string;
  runs_linked: number;
  runs_reported: number;
};

/** CoverageView is the link-coverage reading itself, stated beside the figures it qualifies. */
export type CoverageView = {
  known: boolean;
  complete: boolean;
  percent: number;
  runs_linked: number;
  runs_reported: number;
  statement: string;
};

export type BillingOversight = {
  tenant_id: string;
  period: string;
  /** How much of this tenant's activity the SUM-derived figures below reflect. Always present. */
  link_coverage: CoverageView;
  /** The period's SUM-derived total. Absent when coverage is unknown — withheld, never shown bare. */
  metered_sum?: DerivedFigure;
  /** The period's billable saving, drawn exclusively on the P5.5 verified-delta ledger. */
  gainshare_savings?: DerivedFigure;
  invoices: InvoiceLine[];
  dunning: InvoiceLine[];
  reconciliation_matched: boolean;
  drift?: Array<{
    kind: string;
    metric: string;
    period: string;
    platform_quantity: number;
    provider_quantity: number;
    detail: string;
  }>;
  reconciliation_degraded: boolean;
  reconciliation_detail?: string;
  gainshare?: GainshareLine[];
  exceptions: number;
  /**
   * Every audited plan change for the tenant, newest first, across ALL periods.
   *
   * Not period-scoped on purpose: a plan change carries no period, so the period-filtered `invoices`
   * list above structurally cannot contain one.
   */
  plan_history?: PlanChangeLine[];
};

export type PlanChangeLine = {
  event_id: string;
  status: string;
  caused_by: string;
  reason?: string;
  created_at: string;
};

export type ModelRecord = {
  model_id: string;
  provider: string;
  price_ref: string;
  deprecated: boolean;
  deprecated_at?: string;
  updated_at: string;
  revision: number;
};

export type Job = {
  run_id: string;
  config_hash: string;
  source_revision: string;
  state: string;
  attempts: number;
  leased_by?: string;
  lease_expires_at?: string;
  enqueued_at: string;
  dead_letter_reason?: string;
};

export type FleetHealth = {
  ready: number;
  leased: number;
  done: number;
  dead: number;
  workers: Record<string, number>;
  expired_leases: number;
  oldest_lease_age_seconds: number;
  source: string;
  degraded: boolean;
  detail?: string;
};

export type KillSwitchState = {
  scope: string;
  armed: boolean;
  set_by?: string;
  reason?: string;
  set_at?: string;
};

export type KillSwitchPage = {
  states: KillSwitchState[];
  global?: KillSwitchState;
  policy: { global_disarm_requires_two_person: boolean };
  degraded?: boolean;
  detail?: string;
};

export type AggregateRow = {
  label: string;
  value: number;
  unit: string;
  detail?: string;
};

export type ReadModel = {
  aggregate: string;
  display_name: string;
  period: string;
  cohort: number;
  suppressed: boolean;
  suppression_reason?: string;
  rows?: AggregateRow[];
  source: string;
  degraded: boolean;
  detail?: string;
  per_tenant?: string;
  /** What the model excludes, stated where the figures appear rather than in a document. */
  note?: string;
};

export type AuditEntry = {
  seq: number;
  prev_hash: string;
  entry_hash: string;
  actor_admin_id: string;
  target: string;
  action: string;
  reason?: string;
  params_digest?: string;
  result: string;
  impersonation_id?: string;
  evidence?: Record<string, string>;
  created_at: string;
};

/** MergePath is one way a merge reaches a customer's repository. */
export type MergePath = {
  id: string;
  name: string;
  mechanism: string;
  /** Where this path IS readable, when the chain does not record it. A gap with a destination. */
  readable_at?: string;
};

/**
 * MergePathCoverage is the honest statement of what the hash chain holds about merges.
 *
 * The chain mirrors P6 autonomous merges. It does not record P12 customer-CI-mediated deliveries,
 * which merge in the customer's own CI under a credential the platform does not hold — so the absence
 * of a delivery from this log is not evidence that it did not happen.
 */
export type MergePathCoverage = {
  covered: MergePath[];
  not_covered: MergePath[];
  statement: string;
};

export type AuditView = {
  entries: AuditEntry[];
  verification: { intact: boolean; break_at?: number; detail?: string; checked: number };
  total: number;
  merge_coverage: MergePathCoverage;
};

export type GDPRRequest = {
  request_id: string;
  subject_ref: string;
  status: string;
  actor: string;
  reason: string;
  verification_ref?: string;
  tombstone_ref?: string;
  removed_count: number;
  created_at: string;
  completed_at?: string;
};

export type VerificationReport = {
  request_id: string;
  completed: boolean;
  content_remaining: number;
  verification_ref_matches: boolean;
  tombstone_in_chain: boolean;
  chain_intact: boolean;
  detail?: string;
};

export type Receipt = {
  write_ahead_seq: number;
  outcome_seq: number;
  entry_hash: string;
  result: string;
  evidence?: Record<string, string>;
  actor_admin_id: string;
  at: string;
};

/* ══════════════════════════════════════════════════════════════════════════
   P26 — Delivery oversight (read-only)
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * MergeState is what the platform KNOWS about a delivery's merge.
 *
 * 🔴 Three values, and the third is the point. A pull request that closed may have been merged,
 * squashed, rebased, or abandoned, and only one of those is a delivery — so a merge is observed, never
 * inferred from a pull request closing, and `unknown` is a real answer rather than a gap papered over
 * with the most likely outcome.
 */
export type MergeState = "merged" | "closed_unmerged" | "unknown";

export type DeliveryRow = {
  delivery_id: string;
  tenant_id: string;
  config_hash: string;
  source_revision: string;
  target: string;
  forge_ref?: string;
  /** The credential path: `ci` (the platform holds none) or `app` (the opt-in hosted Git App). */
  mode: string;
  /** The P12 lifecycle state, verbatim. */
  state: string;
  merge: MergeState;
  merge_commit?: string;
  reason?: string;
  /** The audit-chain target, only where the chain actually covers this delivery's merge path. */
  audit_target?: string;
};

/** DeliveryCount is one aggregate figure and the query that reaches the records behind it. */
export type DeliveryCount = { label: string; value: number; drill_down: string };

/** RolloutCauseCount is one typed undeliverable cause. `cause` is a stable identifier, never prose. */
export type RolloutCauseCount = {
  cause: string;
  /** Who can close it: "nobody", "you", "the platform". Computed by the platform, not inferred here. */
  owner: string;
  /** A boundary rather than unbuilt work. A permanent cause names no missing artifact, ever. */
  permanent: boolean;
  missing_artifact?: string;
  label: string;
  count: number;
};

export type RolloutStageRow = {
  axis: string;
  change: string;
  route: string;
  status: string;
  cause?: string;
  owner?: string;
  permanent?: boolean;
  missing_artifact?: string;
  note?: string;
};

export type DeliveryView = {
  tenant_id?: string;
  rows: DeliveryRow[];
  counts: DeliveryCount[];
  rollout_stages: RolloutStageRow[];
  /** Never a single total: the three causes are answered by three different people. */
  undeliverable: RolloutCauseCount[];
  undeliverable_total: number;
  merge_coverage: MergePathCoverage;
  degraded: boolean;
  detail?: string;
  /** Stated on the wire: this surface shows a problem it cannot act on, deliberately. */
  read_only: boolean;
  source: string;
};

/* ══════════════════════════════════════════════════════════════════════════
   P26 — Release & trust oversight (read-only)
   ══════════════════════════════════════════════════════════════════════════ */

/** VerifyState: *not yet checked* is neither a pass nor a failure. */
export type VerifyState = "verified" | "failed" | "not_yet_verified";

/**
 * SmokeState is the post-publish smoke result per platform image.
 *
 * 🔴 `queued_until_timeout` is NOT a failure. A retired runner label queues until the workflow times
 * out — measured in P20 with `macos-13` — and rendering that as *failed* sends an engineer to debug a
 * build that never ran. The empty string is the ABSENCE of a run, not a fourth outcome; the sequence's
 * stopping point says so instead.
 */
export type SmokeState = "passed" | "failed" | "queued_until_timeout" | "";

export type PublishStep = "publish" | "verify" | "smoke" | "complete";

export type ArtefactRow = {
  version: string;
  channel: string;
  platform: string;
  name: string;
  /** False renders as *absent*; the version is not presented as complete. */
  published: boolean;
  /** 🔴 Identifier and fingerprint. There is no field here that could carry key material. */
  signing_key_id: string;
  key_fingerprint: string;
  signed_with_retired_key: boolean;
  key_retired_at?: string;
  key_retired_why?: string;
  verification: VerifyState;
  smoke?: SmokeState;
  smoke_detail?: string;
};

export type SequenceRow = {
  version: string;
  channel: string;
  /** Where publish → verify → smoke stopped, or "complete". */
  stopped_at: PublishStep;
  reason: string;
  completed?: PublishStep[];
};

export type SigningKeyRow = {
  id: string;
  fingerprint: string;
  role: string;
  note?: string;
  retired_at?: string;
  signed_releases?: string[];
};

export type ChannelRow = {
  id: string;
  label: string;
  /** Whether a user can install from this channel TODAY. A generated manifest is not a channel. */
  delivered: boolean;
  blocker?: string;
  verification: string;
  versions?: string[];
};

export type ReleaseView = {
  channels: ChannelRow[];
  artefacts: ArtefactRow[];
  sequences: SequenceRow[];
  keys: SigningKeyRow[];
  read_only: boolean;
  degraded: boolean;
  detail?: string;
  source: string;
};

/* ══════════════════════════════════════════════════════════════════════════
   P26 — Axis oversight (read-only)
   ══════════════════════════════════════════════════════════════════════════ */

/**
 * CellState is how one coverage cell renders. Three, and there is no "not applicable".
 *
 * 🔴 *Not applicable* is deliberately absent from this union. It says "your call site cannot carry
 * this", while the truth may be "we have not built the materializer" — a substitution that converts
 * our backlog into the customer's problem, invisibly. `unknown` names the missing input instead.
 */
export type CellState = "applies" | "refused" | "unknown";

export type CoverageCellRow = {
  axis: string;
  language: string;
  form: string;
  state: CellState;
  /** The engine's STABLE cause identifier, verbatim — never prose, never re-vocabularised. */
  cause?: string;
  missing_input?: string;
  note?: string;
};

export type AxisRow = {
  axis: string;
  /** The axis's OWN declared status. The console does not compute, adjust or reinterpret it. */
  status: string;
  /** null means UNKNOWN — no adoption source is wired. Not zero, and never rendered as zero. */
  tenants: number | null;
  nodes: number | null;
  refusals: Record<string, number>;
  refusals_by_language: Record<string, Record<string, number>>;
  drill_down: string;
};

export type ArtefactRank = {
  artefact: string;
  /** A COUNT of refused cells, not a score. */
  closes: number;
  axes: string[];
  languages: string[];
  drill_down: string;
};

export type CauseLegend = {
  cause: string;
  owner: string;
  permanent: boolean;
  meaning: string;
};

export type RefusedNode = {
  tenant_id: string;
  node_id: string;
  language: string;
  axis: string;
  cause: string;
};

export type AxisView = {
  axes: AxisRow[];
  matrix: CoverageCellRow[];
  ranking: ArtefactRank[];
  legend: CauseLegend[];
  /** FALSE. Only the eval harness ranks; these are counts and must not wear a result's grammar. */
  is_ranking: boolean;
  /** TRUE. A coverage gap is identical on every plan; no tier unlocks a cell the engine refuses. */
  plan_independent: boolean;
  adoption_known: boolean;
  coverage_source: string;
  coverage_version: string;
  read_only: boolean;
};

/* ══════════════════════════════════════════════════════════════════════════
   P26 — Identity, consent and reporting health (read-only)
   ══════════════════════════════════════════════════════════════════════════ */

/** Three values: *not configured* is a DECISION and *degraded* is a FAULT. Never a boolean. */
export type IntegrationState = "absent" | "configured" | "degraded";

export type SessionRow = {
  session_id: string;
  admin_id: string;
  /** The factor NAME the platform VERIFIED — never the IdP's claim, never the factor's value. */
  factor: string;
  verified_at: string;
  expires_at: string;
  live: boolean;
  multi_factor: boolean;
};

export type IdentityProviderView = {
  issuer: string;
  kind: string;
  /** True when the IdP is the test-mode fixture. The surface says so; it claims no real IdP. */
  test_mode: boolean;
  note: string;
};

export type LegalRow = {
  tenant_id: string;
  kind?: string;
  accepted_version?: string;
  accepted_hash?: string;
  /** Links the ARCHIVED text at the accepted content hash — not the current text. */
  archive_href?: string;
  accepted_at?: string;
  /** Set only after a MATERIAL publication. A non-material version creates no obligation. */
  owed_version?: string;
  owed_since?: string;
  owed_href?: string;
};

export type IntegrationRow = {
  name: string;
  state: IntegrationState;
  /** Required when degraded: "unreachable" and "rejecting our schema" are two different places to go. */
  failure_class?: string;
  /** The PLATFORM's readiness surface — never a third party's dashboard. */
  source: string;
};

export type DeploymentRow = {
  tenant_id: string;
  shape?: string;
  version?: string;
  /** True when no signal carries the version. Nothing here is inferred from a proxy signal. */
  unknown: boolean;
  missing_collection?: string;
};

export type NotYetReadable = { subject: string; requires: string; statement: string };

export type OversightView = {
  sessions: SessionRow[];
  identity_provider: IdentityProviderView;
  legal: LegalRow[];
  legal_known: boolean;
  integrations: IntegrationRow[];
  integrations_known: boolean;
  deployments: DeploymentRow[];
  not_yet_readable: NotYetReadable[];
  read_only: boolean;
  source: string;
};

// ── P30 · the platform's own analysis agent ──────────────────────────────────

/** How a tenant's placement came to be what it is. A DEFAULT and a DECISION are different facts. */
export type PlacementSource = "defaulted" | "explicit";

/** The three-valued axis status. `not_in_effect` always carries a reason. */
export type AgentAxisStatus = "set" | "defaulted" | "not_in_effect";

export type AgentAxisRow = {
  /**
   * The node this axis belongs to, or "" for the definition-level `graph` axis.
   *
   * 🔴 Present on EVERY row, including a single-node definition's. A field that appeared only once a
   * definition had two nodes would make the row key change shape underneath the table, and "which
   * node" is the one question this surface exists to answer after P36.
   */
  node_id: string;
  axis: string;
  status: AgentAxisStatus;
  value: string;
  /** Present when status is `not_in_effect`. An inert axis with no stated reason cannot be acted on. */
  reason?: string;
  /** False for the `graph` axis on a single-node definition, which is rendered read-only with its reason
   * rather than hidden — a hidden axis is indistinguishable from one that does not exist. */
  editable: boolean;
};

/**
 * One node of the serving definition, with what it has actually done.
 *
 * 🔴 Per node and never only as an aggregate: an aggregate over a graph says the agent is slow, not
 * WHICH NODE is slow, and that is the only form of the answer anybody can act on.
 */
export type AgentNodeRow = {
  node_id: string;
  prompt_ref: string;
  model_ref: string;
  loop_ref?: string;
  harness_ref: string;
  inferences: number;
  provider_calls: number;
  tokens_in: number;
  tokens_out: number;
  failures: number;
  skips: number;
  /** Mean over completed calls. 🔴 Zero with `inferences: 0` is NOT a fast node — the console renders
   * "not yet run" for that reason. */
  latency_ms: number;
};

/** One strategy's availability, with the host service it would need. */
export type Availability = {
  name: string;
  available: boolean;
  needs?: string;
  reason?: string;
  /** True when making this available costs a SECOND metered model call. */
  second_spend_line?: boolean;
};

export type AgentVersionRow = {
  config_hash: string;
  display: string;
  /** The DISTINCT set this version binds, comma-joined. Byte-identical to the single value for a
   * single-node row, and the honest answer for a graph. */
  model_ref: string;
  credential_ref: string;
  rehearsal_state: string;
  /** True for at most one row, and never derived from recency. */
  active: boolean;
  created_at_ms: number;
  /** How many nodes this version declares. Two versions differing only in TOPOLOGY are otherwise
   * indistinguishable in this list — same model, same credential, different agent. */
  nodes: number;
  /** True when this version passed and is not serving, so it could be rolled back TO.
   * 🔴 Decided by the PLATFORM, so the control is offered exactly where the backend would accept it. */
  rollback_target: boolean;
};

export type AgentOverview = {
  serving_config_hash: string;
  serving_since_ms: number;
  state: "serving" | "none_published" | "pending_rehearsal" | "rehearsal_failed";
  sentence: string;
  axes: AgentAxisRow[] | null;
  rehearsal_state: string;
  rehearsal_report?: string;
  stored_inferences: number;
  /** False when no inference store is wired — the count then renders as unknown, never as zero. */
  inferences_known: boolean;
  harness_availability: Availability[] | null;
  memory_availability: Availability[] | null;
  versions: AgentVersionRow[] | null;
  /** The serving definition's nodes with their live numbers. */
  nodes: AgentNodeRow[] | null;
  /** False when no per-node source is wired — the numbers then render as unknown, never as zero. */
  nodes_known: boolean;
  kill_switch_armed: boolean;
  kill_switch_note?: string;
  can_admin: boolean;
};

export type AgentSpendRow = {
  tenant_id: string;
  inferences: number;
  tokens_in: number;
  tokens_out: number;
  estimated_cost: number;
  /** 🔴 False means UNPRICED. The console renders the word, never a zero. */
  priced: boolean;
  placement: string;
  placement_source: PlacementSource;
  cap_tokens: number;
};

export type AgentSpendView = {
  /** Always true and stated on the wire: these are estimates, not an invoice. */
  estimated: boolean;
  rows: AgentSpendRow[] | null;
  fleet_cap_tokens: number;
  unpriced_tenants: number;
  can_admin: boolean;
  /**
   * The closed set of placements, from the package that owns the vocabulary.
   *
   * 🔴 Never typed into this console. A `<select>` listing the three values in its own markup would be
   * a copy of a closed set, and it fails quietly: the platform gains a placement, the editor keeps
   * offering the old ones, and the new value is unreachable from the surface that exists to set it.
   */
  placements: string[] | null;
};

export type AxisChange = { axis: string; from: string; to: string };

export type PublishPreview = {
  config_hash: string;
  display: string;
  changes: AxisChange[] | null;
  /** True when the edit resolves to a definition that already exists. It creates no version. */
  no_change: boolean;
  already_published: boolean;
  deprecated_model?: string;
  refusals: string[] | null;
};
