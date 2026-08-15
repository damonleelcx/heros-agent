"use server";

import { revalidatePath } from "next/cache";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { ADMIN_SESSION_COOKIE, AdminApiError, adminFetch, exchangeAssertion, revokeSession } from "./adminApi";
import { readSessionToken, SESSION_COOKIE_OPTIONS } from "./session";
import { DENSITY_COOKIE, DENSITY_COOKIE_OPTIONS, THEME_COOKIE, THEME_COOKIE_OPTIONS, isTheme } from "./prefs";
import type { PublishPreview, Receipt } from "./types";

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

// ── The platform agent's own instruction ────────────────────────────────────

/**
 * publishPlatformPrompt authors the PLATFORM's agent instruction and returns the ref a definition
 * binds.
 *
 * # Why this control exists on the operator console at all
 *
 * `Publisher.Publish` refuses a definition whose `prompt_ref` does not resolve, and the prompt
 * registry's only other write route is TENANT-scoped. The platform is not a tenant, so without this
 * an operator could compose a definition here and never publish it — the ref had no operator-side
 * origin.
 *
 * # It does NOT use `post`
 *
 * `post` returns the platform's `Receipt`, and this route answers with the published version id
 * instead. That id is the entire point of the control — it is what the operator pastes into a
 * definition's prompt axis — so it is surfaced in the outcome `message` rather than discarded to fit
 * a shared helper.
 *
 * # The reason field
 *
 * The API does not REQUIRE a reason: publishing a prompt version changes nothing on its own, and
 * demanding a justification for an inert act is how the reason on the act that does change something
 * becomes noise. The console asks for one anyway, because every mutating control here asks for one and
 * a single exception would read as an oversight. The split is deliberate: uniform discipline in the
 * UI, no bootstrap-blocking requirement in the API.
 */
export async function publishPlatformPrompt(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const name = String(fd.get("name") ?? "").trim();
  const body = String(fd.get("body") ?? "");
  const command: Command = {
    action: "agent.publish_prompt",
    target: name,
    reason: String(fd.get("reason") ?? ""),
    undo: "Publish the previous text again — versions are immutable and content-addressed, so the " +
      "earlier one stays resolvable and republishing it returns its original id",
  };
  return withSession(async (token) => {
    const jar = await cookies();
    const impersonationId = jar.get("heros_admin_impersonation")?.value;
    try {
      const res = await adminFetch<{ version_id: string; name: string; created: boolean }>(
        "/admin/api/agent/prompt",
        { method: "POST", body: { name, body }, sessionToken: token, impersonationId },
      );
      revalidatePath("/agent");
      // The two outcomes are reported apart. Content-addressing makes a re-publish of identical text a
      // no-op, and calling that "published" would tell an operator they had edited the platform's
      // instruction when they had not — after which they would go looking for a version that does not
      // exist.
      const message = res.created
        ? `Published. Bind this as the definition's prompt_ref: ${res.version_id}`
        : `No change — this text was already published. Its existing ref is ${res.version_id}`;
      return { ok: true, command, message };
    } catch (error) {
      return toResult(error, command);
    }
  });
}

/**
 * publishAgentDefinition composes a definition from its axes and publishes it as a PENDING version.
 *
 * # What pressing this does and does not do
 *
 * It creates a version. It does NOT change what any customer is analysed by: a published definition is
 * inert until it passes the activation gate and is activated, and those are separate acts on purpose.
 * The copy on the control says so, because "Publish" on an operator console otherwise reads like it
 * takes effect.
 *
 * # Why the outcome is assembled here rather than shown as a receipt
 *
 * This route answers with a `PublishPreview`, not a `Receipt` — it carries the config_hash, the
 * axis-by-axis diff against what is active, and any refusals. Those are the things the operator needs
 * next (the hash is what `activate` takes), so they are surfaced in the outcome message rather than
 * discarded to fit the shared `post` helper.
 *
 * 🔴 `no_change` is reported as its own outcome. A definition is identified by its CONTENT, so an edit
 * that resolves to something already published creates nothing. Calling that "published" would leave an
 * operator waiting for a version that was never made — the same failure the instruction control avoids.
 */
export async function publishAgentDefinition(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const axes: Record<string, string> = {};
  // Only NON-EMPTY axes are sent. An empty string is a real edit meaning "unset this axis", and
  // submitting one for every field an operator left blank would silently clear axes they never touched.
  for (const axis of ["prompt", "model", "context", "memory", "harness"]) {
    const v = String(fd.get(axis) ?? "").trim();
    if (v !== "") axes[axis] = v;
  }
  const list = (name: string) =>
    String(fd.get(name) ?? "")
      .split(",")
      .map((s) => s.trim())
      .filter((s) => s !== "");

  const credentialRef = String(fd.get("credential_ref") ?? "").trim();
  const reason = String(fd.get("reason") ?? "");
  const command: Command = {
    action: "agent.publish",
    target: axes.model ? `model ${axes.model}` : "definition",
    reason,
    undo: "Publish the previous definition again — versions are immutable and content-addressed, so " +
      "the earlier one is still there and republishing it creates nothing",
  };

  return withSession(async (token) => {
    const jar = await cookies();
    const impersonationId = jar.get("heros_admin_impersonation")?.value;
    try {
      const view = await adminFetch<PublishPreview>("/admin/api/agent/publish", {
        method: "POST",
        body: {
          axes,
          skill_refs: list("skill_refs"),
          tool_names: list("tool_names"),
          credential_ref: credentialRef,
          reason,
        },
        sessionToken: token,
        impersonationId,
      });
      revalidatePath("/agent");

      // Refusals come back on a 200 — the definition was rejected on its content, not by transport.
      // Reported as a FAILURE so the operator is not told something was published when nothing was.
      if (view.refusals && view.refusals.length > 0) {
        return {
          ok: false,
          kind: "request",
          message: `Not published. ${view.refusals.join(" ")}`,
        };
      }
      const message = view.no_change
        ? `No change — this definition is already published as ${view.display}. Nothing was created.`
        : `Published as ${view.config_hash}. It is PENDING and serving nothing: activate it to run the ` +
          `calibration set against it, and only a definition that passes is served.`;
      return { ok: true, command, message };
    } catch (error) {
      return toResult(error, command);
    }
  });
}
