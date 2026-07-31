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
};

export type GainshareLine = {
  event_id: string;
  period: string;
  verified_delta_refs?: string[];
  merge_commits?: string[];
  exception?: string;
};

export type BillingOversight = {
  tenant_id: string;
  period: string;
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

export type AuditView = {
  entries: AuditEntry[];
  verification: { intact: boolean; break_at?: number; detail?: string; checked: number };
  total: number;
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
