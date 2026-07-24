"use server";

import { revalidatePath } from "next/cache";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { ADMIN_SESSION_COOKIE, AdminApiError, adminFetch, exchangeAssertion, revokeSession } from "./adminApi";
import { readSessionToken, SESSION_COOKIE_OPTIONS } from "./session";
import { DENSITY_COOKIE, DENSITY_COOKIE_OPTIONS, THEME_COOKIE, THEME_COOKIE_OPTIONS, isTheme } from "./prefs";
import type { Receipt } from "./types";

/**
 * actions.ts holds every mutating server action.
 *
 * They all run on the SERVER (the "use server" directive), so a form submission carries no platform
 * credential to the browser and the credential never leaves the BFF. Each action re-reads the session
 * from the HttpOnly cookie rather than trusting a value the form supplied — a form field is caller
 * input, and the acting principal is not something the caller gets to assert.
 *
 * # The result shape
 *
 * Every action returns an ActionResult rather than throwing, because the value drives the four render
 * states on the page (FR26): `ok` shows a receipt, `denied` names the escalation path, `friction`
 * asks for the missing reason or typed target, `degraded` says the platform is unreachable. A thrown
 * error would collapse all four into one 500.
 */

/**
 * Command describes WHAT was invoked, as the server invoked it.
 *
 * It is built here rather than read from a hidden form field on purpose: a receipt that echoed
 * caller-supplied labels would be a receipt the caller could make say anything. The reason is the one
 * value that does come from the operator — it is what they typed and what the platform recorded — and
 * the receipt links to the audit entry so the RECORDED version is always one click away (FR36).
 */
export type Command = {
  /** The capability name, matching the backend's permission map. */
  action: string;
  /** The subject the command names. */
  target: string;
  /** The reason the operator supplied, which the platform recorded. */
  reason: string;
  /** undo names the reversing action, or is absent when none exists — which the receipt then says. */
  undo?: string;
};

export type ActionResult =
  | { ok: true; receipt?: Receipt; command?: Command; message?: string }
  | {
      ok: false;
      /**
       * denied/friction/request/not_found/gated are FAILURES in which NOTHING HAPPENED and the
       * operator can act on the message. `not_mounted` means the command does not exist in this
       * deployment. `degraded` is a call that could not complete. `unusable` means the platform
       * answered unreadably — retrying will not help. `unknown` is the third outcome only a
       * fail-closed platform produces: the command may or may not have taken effect, and the answer
       * lives in the audit log, not in a retry (FR36).
       *
       * They stay distinct all the way to the confirmation sheet because the operator's NEXT MOVE
       * differs for each, and a command surface that guesses wrong invites either a double-suspend or
       * an abandoned incident.
       */
      kind:
        | "denied"
        | "friction"
        | "request"
        | "not_found"
        | "not_mounted"
        | "gated"
        | "degraded"
        | "unusable"
        | "unknown";
      message: string;
      heldBy?: string[];
      command?: Command;
    };

async function withSession<T>(fn: (token: string) => Promise<T>): Promise<T> {
  const token = await readSessionToken();
  if (!token) redirect("/signin?reason=no_session");
  return fn(token);
}

/**
 * COMMAND_KINDS are the API classifications a command outcome reports as itself.
 *
 * `auth` is deliberately absent: an expired session is not an outcome to render, it is a redirect,
 * and it is handled before a command is ever issued.
 */
const COMMAND_KINDS = [
  "denied",
  "friction",
  "request",
  "not_found",
  "not_mounted",
  "gated",
  "degraded",
  "unusable",
  "unknown",
] as const;

function toResult(error: unknown, command?: Command): ActionResult {
  if (error instanceof AdminApiError) {
    // `degraded` is the fallback rather than the default: it is the answer that sends an operator to
    // look for an outage, and it should only be given when there might be one.
    const kind = (COMMAND_KINDS as readonly string[]).includes(error.kind)
      ? (error.kind as ActionResult extends { ok: false; kind: infer K } ? K : never)
      : "degraded";
    return { ok: false, kind, message: error.message, heldBy: error.heldBy, command };
  }
  return { ok: false, kind: "degraded", message: String(error), command };
}

/** signIn exchanges an SSO+MFA assertion and sets the HttpOnly session cookie. */
export async function signIn(assertion: unknown): Promise<ActionResult> {
  try {
    const { session_token } = await exchangeAssertion(assertion);
    const jar = await cookies();
    jar.set(ADMIN_SESSION_COOKIE, session_token, SESSION_COOKIE_OPTIONS);
  } catch (error) {
    return toResult(error);
  }
  redirect("/tenants");
}

/**
 * setDensity records the operator's display density (FR29).
 *
 * It is a preference, not a command: no reason, no confirmation, no audit entry. The distinction
 * matters — friction belongs on the write path to the PLATFORM, and spending it on "make my rows
 * tighter" would teach operators to click through confirmations, which is exactly how the
 * confirmations that matter stop working (design.md Decision 14).
 *
 * `revalidatePath("/", "layout")` re-renders the shell so the new density lands on the server-rendered
 * root element rather than being applied after hydration.
 */
export async function setDensity(density: "comfortable" | "compact"): Promise<void> {
  const jar = await cookies();
  jar.set(DENSITY_COOKIE, density === "compact" ? "compact" : "comfortable", DENSITY_COOKIE_OPTIONS);
  revalidatePath("/", "layout");
}

/**
 * setTheme records the operator's colour theme (R17 / FR39).
 *
 * Deliberately the same shape as `setDensity`: both are display preferences read on the server so the
 * first paint is correct, and a second mechanism for the second preference would be a second place to
 * get the same thing wrong. `revalidatePath("/", "layout")` is what makes the root element's attribute
 * re-render rather than only the page under it.
 */
export async function setTheme(theme: "system" | "dark" | "light"): Promise<void> {
  const jar = await cookies();
  jar.set(THEME_COOKIE, isTheme(theme) ? theme : "system", THEME_COOKIE_OPTIONS);
  revalidatePath("/", "layout");
}

/** signOut revokes the session server-side and clears the cookie. */
export async function signOut(): Promise<void> {
  const token = await readSessionToken();
  const jar = await cookies();
  jar.delete(ADMIN_SESSION_COOKIE);
  if (token) {
    try {
      await revokeSession(token);
    } catch {
      // The cookie is already gone, so the browser cannot present the session again; a failed
      // server-side revoke is logged by the platform and does not block the sign-out the operator asked
      // for.
    }
  }
  redirect("/signin?reason=signed_out");
}

type CommandFields = {
  reason?: string;
  confirmed?: boolean;
  typed_target?: string;
  [key: string]: unknown;
};

/**
 * post issues one privileged command and returns its outcome — never an optimistic success (FR36).
 *
 * The result is produced only after the platform answers. There is no branch here that reports a
 * state change before the backend (and its write-ahead audit) confirms one, because on this platform
 * the audit entry is committed BEFORE the effect and a command can fail closed after the operator has
 * pressed the button. A surface that rendered success on intent would routinely assert suspensions,
 * refunds and halts that never happened.
 */
async function post(
  path: string,
  body: CommandFields,
  revalidate: string | undefined,
  command: Omit<Command, "reason">,
): Promise<ActionResult> {
  const full: Command = { ...command, reason: String(body.reason ?? "") };
  return withSession(async (token) => {
    const jar = await cookies();
    const impersonationId = jar.get("heros_admin_impersonation")?.value;
    try {
      const receipt = await adminFetch<Receipt>(path, {
        method: "POST",
        body,
        sessionToken: token,
        impersonationId,
      });
      if (revalidate) revalidatePath(revalidate);
      return { ok: true, receipt, command: full };
    } catch (error) {
      return toResult(error, full);
    }
  });
}

function form(fd: FormData): CommandFields {
  return {
    reason: String(fd.get("reason") ?? ""),
    confirmed: fd.get("confirmed") === "on" || fd.get("confirmed") === "true",
    typed_target: fd.get("typed_target") ? String(fd.get("typed_target")) : undefined,
  };
}

// Every mutating action below takes the `(id, prevState, formData)` shape. That is exactly what
// `action.bind(null, id)` produces for React's `useActionState`, so a page binds the target id on the
// server and hands the client component a real server-action reference — never an inline closure,
// which Next refuses to pass across the server/client boundary. Actions with no id take
// `(prevState, formData)`.

// ── Tenant lifecycle ────────────────────────────────────────────────────────

export async function suspendTenant(tenantId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  return post(`/admin/api/tenants/${encodeURIComponent(tenantId)}/suspend`, form(fd), `/tenants/${tenantId}`, {
    action: "tenant.suspend",
    target: tenantId,
    undo: "Reactivate the tenant on its detail page",
  });
}
export async function reactivateTenant(tenantId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  return post(`/admin/api/tenants/${encodeURIComponent(tenantId)}/reactivate`, form(fd), `/tenants/${tenantId}`, {
    action: "tenant.reactivate",
    target: tenantId,
    undo: "Suspend the tenant again",
  });
}
export async function setQuota(tenantId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.limit = String(fd.get("limit") ?? "");
  const raw = String(fd.get("value") ?? "");
  if (raw.trim() === "") body.clear_value = true;
  else body.value = Number(raw);
  return post(`/admin/api/tenants/${encodeURIComponent(tenantId)}/quota`, body, `/tenants/${tenantId}`, {
    action: "tenant.quota",
    target: `${tenantId} · ${String(body.limit ?? "")}`,
    undo: "Set the limit again, or clear it to fall back to the plan's allowance",
  });
}
export async function overrideEntitlement(tenantId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.plan_ref = String(fd.get("plan_ref") ?? "");
  return post(`/admin/api/tenants/${encodeURIComponent(tenantId)}/entitlement`, body, `/tenants/${tenantId}`, {
    action: "entitlement.override",
    target: `${tenantId} → ${String(body.plan_ref ?? "")}`,
    undo: "Override again to the previous plan",
  });
}

// ── Billing ─────────────────────────────────────────────────────────────────

export async function issueCredit(tenantId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.against_event_id = String(fd.get("against_event_id") ?? "");
  return post(`/admin/api/billing/${encodeURIComponent(tenantId)}/credit`, body, `/billing?tenant=${tenantId}`, {
    action: "billing.credit",
    target: `${tenantId} · against ${String(body.against_event_id ?? "")}`,
    // A credit is ADDITIVE (P7): it is corrected by a further correction, never by editing what is
    // already on the ledger. Saying so is more useful than offering an undo that would be a lie.
  });
}
export async function issueRefund(tenantId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.against_event_id = String(fd.get("against_event_id") ?? "");
  return post(`/admin/api/billing/${encodeURIComponent(tenantId)}/refund`, body, `/billing?tenant=${tenantId}`, {
    action: "billing.refund",
    target: `${tenantId} · against ${String(body.against_event_id ?? "")}`,
  });
}

// ── Registry ────────────────────────────────────────────────────────────────

export async function repointPriceRef(modelId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.price_ref = String(fd.get("price_ref") ?? "");
  return post(`/admin/api/registry/models/${encodeURIComponent(modelId)}/price-ref`, body, "/registry", {
    action: "registry.repoint_price_ref",
    target: `${modelId} → ${String(body.price_ref ?? "")}`,
    undo: "Repoint the model back to its previous reference (closed periods are unaffected either way)",
  });
}
export async function deprecateModel(modelId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  return post(`/admin/api/registry/models/${encodeURIComponent(modelId)}/deprecate`, form(fd), "/registry", {
    action: "registry.deprecate_model",
    target: modelId,
  });
}
export async function addModel(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.model_id = String(fd.get("model_id") ?? "");
  body.provider = String(fd.get("provider") ?? "");
  body.price_ref = String(fd.get("price_ref") ?? "");
  return post("/admin/api/registry/models", body, "/registry", {
    action: "registry.add_model",
    target: String(body.model_id ?? ""),
    undo: "Deprecate the model",
  });
}

// ── Jobs ────────────────────────────────────────────────────────────────────

export async function retryJob(runId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  return post(`/admin/api/jobs/${encodeURIComponent(runId)}/retry`, form(fd), "/fleet", {
    action: "job.retry",
    target: runId,
  });
}
export async function cancelJob(runId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  return post(`/admin/api/jobs/${encodeURIComponent(runId)}/cancel`, form(fd), "/fleet", {
    action: "job.cancel",
    target: runId,
    undo: "Retry the run from the queue",
  });
}

// ── Kill switch ─────────────────────────────────────────────────────────────
//
// The scope is derived on the SERVER from the form, never built in a client closure: the global form
// carries scope="global" in a hidden field, and the per-tenant form carries a tenant id that this
// action prefixes. That keeps "halt one tenant" and "halt the fleet" two different server-side code
// paths, not one closure with a variable in it.

export async function armKillSwitch(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.scope = scopeFrom(fd);
  return post("/admin/api/killswitch/arm", body, "/killswitch", {
    action: "killswitch.arm",
    target: String(body.scope ?? ""),
    undo: "Disarm the same scope — the safer direction is the one that needs the second approver",
  });
}
export async function disarmKillSwitch(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.scope = scopeFrom(fd);
  const approver = String(fd.get("second_approver") ?? "");
  if (approver) body.second_approver = approver;
  return post("/admin/api/killswitch/disarm", body, "/killswitch", {
    action: "killswitch.disarm",
    target: String(body.scope ?? ""),
    undo: "Arm the same scope again",
  });
}

// scopeFrom resolves the kill-switch scope: an explicit "global" hidden field, or "tenant:<id>" built
// from a per-tenant form's tenant id.
function scopeFrom(fd: FormData): string {
  const explicit = String(fd.get("scope") ?? "").trim();
  if (explicit) return explicit;
  const tenant = String(fd.get("tenant_id") ?? "").trim();
  return tenant ? `tenant:${tenant}` : "";
}

// ── Impersonation ───────────────────────────────────────────────────────────

export async function startImpersonation(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.tenant_id = String(fd.get("tenant_id") ?? "");
  const ttl = Number(fd.get("ttl_seconds") ?? 0);
  if (ttl > 0) body.ttl_seconds = ttl;
  return withSession(async (token) => {
    try {
      const res = await adminFetch<{ session: { id: string } }>("/admin/api/impersonation", {
        method: "POST",
        body,
        sessionToken: token,
      });
      const jar = await cookies();
      jar.set("heros_admin_impersonation", res.session.id, SESSION_COOKIE_OPTIONS);
      revalidatePath("/tenants");
      return {
        ok: true,
        message: "Impersonation started — the banner above names the tenant, the scope and the expiry.",
        command: {
          action: "impersonate.start",
          target: String(body.tenant_id ?? ""),
          reason: String(body.reason ?? ""),
          undo: "End the session from the banner — it also expires on its own bound",
        },
      };
    } catch (error) {
      return toResult(error, {
        action: "impersonate.start",
        target: String(body.tenant_id ?? ""),
        reason: String(body.reason ?? ""),
      });
    }
  });
}

export async function elevateImpersonation(impId: string, _p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  return post(`/admin/api/impersonation/${encodeURIComponent(impId)}/elevate`, form(fd), "/tenants", {
    action: "impersonate.elevate",
    target: impId,
    undo: "End the impersonation session from the banner",
  });
}

export async function endImpersonation(impId: string): Promise<void> {
  await withSession(async (token) => {
    try {
      await adminFetch(`/admin/api/impersonation/${encodeURIComponent(impId)}/end`, {
        method: "POST",
        body: { reason: "operator ended the session" },
        sessionToken: token,
      });
    } catch {
      // Ending is best-effort from the button; the session also expires on its own bound.
    }
  });
  const jar = await cookies();
  jar.delete("heros_admin_impersonation");
  revalidatePath("/tenants");
}

// ── Compliance ──────────────────────────────────────────────────────────────

export async function executeGDPR(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const body = form(fd);
  body.subject_ref = String(fd.get("subject_ref") ?? "");
  // The typed second confirmation is the FULL target identifier the backend checks.
  const subject = body.subject_ref as string;
  if (subject) body.typed_target = `subject:${subject}`;
  return withSession(async (token) => {
    try {
      const res = await adminFetch<{ receipt: Receipt }>("/admin/api/gdpr/execute", {
        method: "POST",
        body,
        sessionToken: token,
      });
      revalidatePath("/compliance");
      return {
        ok: true,
        receipt: res.receipt,
        command: {
          action: "gdpr.execute",
          target: String(body.subject_ref ?? ""),
          reason: String(body.reason ?? ""),
          // Deliberately no `undo`. Erasure is a one-way door, and the receipt says so in those words
          // rather than leaving the absence of a control to imply it (FR36).
        },
      };
    } catch (error) {
      return toResult(error, {
        action: "gdpr.execute",
        target: String(body.subject_ref ?? ""),
        reason: String(body.reason ?? ""),
      });
    }
  });
}
