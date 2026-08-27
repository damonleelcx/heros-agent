"use server";

import { revalidatePath } from "next/cache";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { ADMIN_SESSION_COOKIE, AdminApiError, adminFetch, exchangeAssertion, revokeSession } from "./adminApi";
import { readSessionToken, SESSION_COOKIE_OPTIONS } from "./session";
import { DENSITY_COOKIE, DENSITY_COOKIE_OPTIONS, THEME_COOKIE, THEME_COOKIE_OPTIONS, isTheme } from "./prefs";
import { count } from "./format";
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
/**
 * PER_NODE_AXES are the eight axes an operator sets ON A NODE.
 *
 * 🔴 `graph` is NOT here. Topology is a property BETWEEN nodes, so it is declared once for the
 * definition — and the platform REFUSES it inside a node's axis map by name rather than hoisting it,
 * because silently moving it would let an operator believe one node owns the graph.
 *
 * 🔴 The list is asserted against the platform's own `AuthorableAxes()` by `agent-publish.test.mjs`.
 * If the form sends an axis the platform does not author, every publish fails; if it sends a
 * valid-but-wrong one, the operator publishes something they did not compose. Neither is visible from
 * either side alone.
 */
const PER_NODE_AXES = ["prompt", "model", "context", "memory", "harness", "loop"] as const;

/** listOf splits a comma-separated field, dropping empties. */
function listOf(fd: FormData, name: string): string[] {
  return String(fd.get(name) ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

/**
 * nodeEditsFrom reads the repeated per-node fields the publish form submits.
 *
 * The form names them `node.<i>.<field>`, so a definition of any size arrives as one flat FormData —
 * which is what lets the control be a progressively-added fieldset rather than a JSON textarea.
 *
 * 🔴 A node whose every field is blank is DROPPED rather than submitted. An operator who pressed
 * "add a node" and changed their mind would otherwise publish a node with no bindings, which the
 * platform refuses with a message about the prompt axis rather than about the empty node.
 */
function nodeEditsFrom(fd: FormData): Array<Record<string, unknown>> {
  const out: Array<Record<string, unknown>> = [];
  for (let i = 0; ; i++) {
    const prefix = `node.${i}.`;
    if (!Array.from(fd.keys()).some((k) => k.startsWith(prefix))) break;
    const axes: Record<string, string> = {};
    for (const axis of PER_NODE_AXES) {
      const v = String(fd.get(prefix + axis) ?? "").trim();
      if (v !== "") axes[axis] = v;
    }
    const skills = listOf(fd, prefix + "skill_refs");
    const tools = listOf(fd, prefix + "tool_names");
    const credential = String(fd.get(prefix + "credential_ref") ?? "").trim();
    const nodeId = String(fd.get(prefix + "node_id") ?? "").trim();
    if (Object.keys(axes).length === 0 && skills.length === 0 && tools.length === 0 && credential === "") {
      continue;
    }
    out.push({
      node_id: nodeId,
      axes,
      skill_refs: skills,
      tool_names: tools,
      credential_ref: credential,
    });
  }
  return out;
}

/** parseTopology reads the definition-level `graph` axis. Empty fields mean "no topology declared". */
function parseTopology(fd: FormData): Record<string, unknown> | undefined {
  const order = listOf(fd, "topology.order");
  const edgesRaw = String(fd.get("topology.edges") ?? "").trim();
  const groupsRaw = String(fd.get("topology.graph_groups") ?? "").trim();
  if (order.length === 0 && edgesRaw === "" && groupsRaw === "") return undefined;
  // 🔴 Parsed here so a malformed declaration is refused with the operator still looking at the form.
  // Sending it through would produce a 400 whose message is about JSON rather than about the graph.
  const parse = (raw: string, field: string): unknown[] => {
    if (raw === "") return [];
    const v = JSON.parse(raw);
    if (!Array.isArray(v)) throw new SyntaxError(`${field} must be a JSON array`);
    return v;
  };
  return {
    order,
    edges: parse(edgesRaw, "edges"),
    graph_groups: parse(groupsRaw, "graph_groups"),
  };
}

export async function publishAgentDefinition(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const nodes = nodeEditsFrom(fd);
  let topology: Record<string, unknown> | undefined;
  try {
    topology = parseTopology(fd);
  } catch (error) {
    return {
      ok: false,
      kind: "request",
      message:
        `The topology could not be read: ${error instanceof Error ? error.message : String(error)}. ` +
        `Edges and graph groups are JSON arrays — nothing was published.`,
    };
  }

  const reason = String(fd.get("reason") ?? "");
  const firstModel = nodes.length > 0 ? String((nodes[0].axes as Record<string, string>).model ?? "") : "";
  const command: Command = {
    action: "agent.publish",
    target:
      nodes.length > 1
        ? `${nodes.length}-node definition`
        : firstModel !== ""
          ? `model ${firstModel}`
          : "definition",
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
          // 🔴 `nodes` alone. The platform REFUSES a request carrying both the single-node axis fields
          // and a node list rather than merging them, because whether the top-level axes describe the
          // first node or override every node is a question with two answers.
          nodes,
          topology,
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

/**
 * activateAgentDefinition makes a rehearsed definition the one serving inference.
 *
 * # 🔴 This is the control that spends
 *
 * Pressing it runs the pinned calibration set against a live model on this deployment's own provider
 * credential — one provider call per fixture, at the moment of the press. Deliberately not behind a
 * schedule: the operator who decides to activate is the one who should see the bill for measuring it.
 *
 * The gate then decides. `Publisher.Activate` refuses any version whose rehearsal state is not `passed`,
 * and the floor is read as the MINIMUM across fixtures rather than the mean — a mean is exactly the
 * aggregate that hides a per-repository catastrophe.
 *
 * # Why the outcome is not a receipt
 *
 * The route answers 204 on success and carries the gate's refusal in its error, which is the useful half:
 * a refusal names the fixture and the score that failed. So a failure here is rendered with that text
 * intact rather than flattened into "activation failed", which would send an operator to look for an
 * outage instead of at a measurement.
 *
 * 🚫 No typed-target friction, deliberately. The friction tier on this console is driven by the SERVER's
 * classification, and `handleAgentActivate` takes a config_hash and a reason — it does not request a
 * confirmation. Inventing a red tier here would be the console deciding blast radius on its own, which
 * is the one thing the friction rules forbid.
 */
export async function activateAgentDefinition(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const configHash = String(fd.get("config_hash") ?? "").trim();
  const reason = String(fd.get("reason") ?? "");
  const command: Command = {
    action: "agent.activate",
    target: configHash || "definition",
    reason,
    undo: "Activate a different published definition — a version is never un-activated, it is replaced",
  };
  return withSession(async (token) => {
    const jar = await cookies();
    const impersonationId = jar.get("heros_admin_impersonation")?.value;
    try {
      await adminFetch<unknown>("/admin/api/agent/activate", {
        method: "POST",
        body: { config_hash: configHash, reason },
        sessionToken: token,
        impersonationId,
      });
      revalidatePath("/agent");
      return {
        ok: true,
        command,
        message:
          `Activated. ${configHash} met the floor on every calibration fixture and is now serving ` +
          `inference. Its per-fixture report is on the Rehearsal tab.`,
      };
    } catch (error) {
      return toResult(error, command);
    }
  });
}

// ── The analysis agent's placement and caps ─────────────────────────────────
//
// Neither of the two below uses `post`, and the reason is the same for both: `post` returns the
// platform's `Receipt`, and these two routes answer with the value they set. Handing that to the
// receipt renderer would not merely lose the audit link — it reads `entry_hash` off the object, so a
// SUCCESSFUL command would throw while rendering. The outcome is assembled here instead, which is what
// `activateAgentDefinition` does and for the same reason.
//
// ⚠️ So the receipt's audit-sequence link is absent on these two, and the outcome says what changed
// instead. Both acts ARE audited — `SetPlacement` and `SetCap` each append before returning — but the
// sequence number is not on the wire to link to. Recorded here rather than papered over with a link
// that would have to guess.

/**
 * setAgentPlacement decides where one tenant's analysis runs, and whether it runs at all.
 *
 * # What `platform` means, and why this is not a toggle
 *
 * It makes THIS platform read that tenant's source under a platform-held credential. `customer` means
 * the tenant runs it on their own machine under their own key and submits the result. `disabled` means
 * nothing runs anywhere. Three states, one of which is the default — which is why the control names all
 * three and offers no "enable" shortcut: "on" would have to pick one of two very different answers.
 *
 * # 🔴 The placement is NOT validated here
 *
 * The value goes to the server as typed and `herosagent.ParsePlacement` refuses anything outside the
 * set. Re-checking it here would be a copy of a closed set in a second language — the exact duplication
 * the option list is fetched from the platform to avoid.
 */
export async function setAgentPlacement(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const tenantId = String(fd.get("tenant_id") ?? "").trim();
  const placement = String(fd.get("placement") ?? "").trim();
  const reason = String(fd.get("reason") ?? "");
  const command: Command = {
    action: "agent.placement",
    target: `${tenantId || "(no tenant)"} → ${placement || "(none)"}`,
    reason,
    undo:
      "Set the placement again — it is a current value rather than an event, and moving a tenant back " +
      "off `disabled` also clears the stale mark that disabling put on its stored inferences",
  };
  if (!tenantId || !placement) {
    return {
      ok: false,
      kind: "request",
      message:
        "A placement needs both a tenant and a value, and nothing was sent. A blank tenant id would " +
        "not name the fleet here — it would write a placement against no tenant at all.",
      command,
    };
  }
  return withSession(async (token) => {
    const jar = await cookies();
    const impersonationId = jar.get("heros_admin_impersonation")?.value;
    try {
      await adminFetch<unknown>("/admin/api/agent/placement", {
        method: "POST",
        body: { tenant_id: tenantId, placement, reason },
        sessionToken: token,
        impersonationId,
      });
      revalidatePath("/agent/spend");
      return { ok: true, command, message: placementOutcome(tenantId, placement) };
    } catch (error) {
      return toResult(error, command);
    }
  });
}

/**
 * placementOutcome says what the new placement MEANS, not that it was saved.
 *
 * 🔴 The three sentences are different on purpose, the same way `Host.MayRun`'s refusals are. "Placement
 * set to platform" tells an operator nothing they did not type; "this platform now reads that
 * tenant's source under a platform-held credential" is the fact they are accountable for. The default
 * branch exists because the option list comes from the PLATFORM: a placement added there arrives on
 * this console before this function knows a sentence for it, and the honest answer then is the value
 * itself rather than a sentence borrowed from a neighbouring state.
 */
function placementOutcome(tenantId: string, placement: string): string {
  switch (placement) {
    case "platform":
      return (
        `${tenantId} is now placed \`platform\`: this platform reads that tenant's source and analyses ` +
        `it under a platform-held credential. The spend lands on this deployment's own bill.`
      );
    case "customer":
      return (
        `${tenantId} is now placed \`customer\`: it analyses on its own machine under its own ` +
        `credential, and the result arrives through the structure ingest. This platform runs nothing ` +
        `for it and spends nothing on it.`
      );
    case "disabled":
      return (
        `${tenantId} is now placed \`disabled\`: no inference runs for it on either host. Its stored ` +
        `inferences are marked stale rather than deleted, so nothing keeps rendering agent-authored ` +
        `facts as current.`
      );
    default:
      return `${tenantId} is now placed \`${placement}\`.`;
  }
}

/**
 * setAgentCap edits a token ceiling. It is checked BEFORE the provider call, so it bounds a cost
 * rather than reporting one.
 *
 * # Two forms, one action, and the scope is decided on the SERVER
 *
 * Exactly the kill switch's shape: the fleet form carries `scope="fleet"` in a hidden field, the
 * per-tenant form carries a tenant id, and this reads which one it got.
 *
 * 🔴 That indirection is the point. The API spells "the fleet" as an EMPTY tenant id, so the obvious
 * implementation — send whatever the field holds — turns a per-tenant form submitted with a blank
 * tenant into a fleet-wide ceiling change, reported as success. The blank is refused here instead.
 *
 * # 🔴 Zero REMOVES the ceiling, and an empty field is not zero
 *
 * The store deletes the cap row at zero, so `0` means unbounded. `Number("")` is also `0` — which
 * would make an empty field, the commonest slip on any form, silently remove the bound on fleet-wide
 * analysis spend and then say "done". The field is parsed strictly and anything that is not a whole
 * number is refused with nothing sent.
 */
export async function setAgentCap(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const fleet = String(fd.get("scope") ?? "").trim() === "fleet";
  const tenantId = String(fd.get("tenant_id") ?? "").trim();
  const raw = String(fd.get("tokens") ?? "").trim();
  const reason = String(fd.get("reason") ?? "");
  const command: Command = {
    action: "agent.cap",
    target: `${fleet ? "fleet" : tenantId || "(no tenant)"} · ${raw || "(no value)"} tokens`,
    reason,
    // No trailing full stop: the receipt renders `To reverse: {undo}.` and adds its own, which showed
    // up on the rendered page as "Zero removes it entirely..".
    undo: "Set the cap again — it is a current value rather than an event, and zero removes it entirely",
  };
  if (!fleet && !tenantId) {
    return {
      ok: false,
      kind: "request",
      message:
        "No tenant was named, and nothing was sent. An empty tenant id is how the platform spells " +
        "`the fleet`, so submitting this would have changed the FLEET-WIDE ceiling from the per-tenant " +
        "control. Name a tenant, or use the fleet control.",
      command,
    };
  }
  if (!/^\d+$/.test(raw) || !Number.isSafeInteger(Number(raw))) {
    return {
      ok: false,
      kind: "request",
      message:
        "A cap is a whole number of tokens, and nothing was sent. `0` is a valid entry and REMOVES the " +
        "ceiling — an empty field is not the same thing, which is why it is refused here rather than " +
        "read as a zero.",
      command,
    };
  }
  const tokens = Number(raw);
  return withSession(async (token) => {
    const jar = await cookies();
    const impersonationId = jar.get("heros_admin_impersonation")?.value;
    try {
      await adminFetch<unknown>("/admin/api/agent/cap", {
        method: "POST",
        // 🔴 The empty string is written HERE, from the scope this action resolved — never forwarded
        // from a field an operator could leave blank.
        body: { tenant_id: fleet ? "" : tenantId, tokens, reason },
        sessionToken: token,
        impersonationId,
      });
      revalidatePath("/agent/spend");
      const what = fleet ? "Fleet-wide analysis" : `Analysis for ${tenantId}`;
      const message =
        tokens === 0
          ? `${what} now has NO ceiling. A cap is what stops analysis before the provider call, so ` +
            `nothing bounds this spend.` +
            (fleet ? "" : " The fleet cap, if one is set, still applies.")
          : `${what} stops before the provider call once ${count(tokens)} tokens are spent, and emits ` +
            `an event.`;
      return { ok: true, command, message };
    } catch (error) {
      return toResult(error, command);
    }
  });
}

/**
 * rollbackAgentDefinition returns a PREVIOUSLY SERVING definition to service.
 *
 * # 🔴 Why this control exists at all
 *
 * Rollback is one act: activating a version that already exists. Without a control it is a capability
 * with no way to press it — and this console has shipped three of those (`agent/prompt`,
 * `agent/publish`, `agent/activate`), each found by a person going to do the thing and discovering
 * there was no thing to press. During an incident is the worst possible time to discover the fourth.
 *
 * # 🔴 Why it is not "activate, again"
 *
 * The same database write and two different events. An operator reconstructing an incident reads the
 * audit log, and "rolled back to X" and "activated X" answer different questions — the first says
 * something went wrong, the second says somebody shipped. A shared action would erase that distinction
 * at exactly the moment it matters most.
 *
 * 🚫 It sends NO definition. Re-authoring the older shape means retyping a configuration under
 * pressure; any transcription error produces a different `config_hash`, which is a THIRD configuration
 * nobody has measured, activated in place of the one known to work. A shape that can no longer be
 * authored cannot be retyped at all — and that is precisely the version somebody most wants back.
 *
 * 🚫 It does not re-run the rehearsal. The version already passed; that verdict is on its immutable
 * row. Re-measuring during an incident spends provider tokens to reproduce a recorded number and makes
 * rollback slow at the moment speed is the point.
 */
export async function rollbackAgentDefinition(_p: ActionResult | null, fd: FormData): Promise<ActionResult> {
  const configHash = String(fd.get("config_hash") ?? "").trim();
  const reason = String(fd.get("reason") ?? "");
  const command: Command = {
    action: "agent.rollback",
    target: configHash === "" ? "definition" : configHash.slice(0, 12),
    reason,
    undo: "Roll back again to whichever definition was serving before this one — every published " +
      "version stays addressable by its hash, so there is always something to return to",
  };
  if (configHash === "") {
    return {
      ok: false,
      kind: "request",
      message:
        "A rollback names the version to return to, and nothing was sent. It is a config_hash from " +
        "the Versions tab — a rollback never re-authors a definition, so there is no other way to " +
        "say which one.",
      command,
    };
  }
  return withSession(async (token) => {
    const jar = await cookies();
    const impersonationId = jar.get("heros_admin_impersonation")?.value;
    try {
      await adminFetch<unknown>("/admin/api/agent/rollback", {
        method: "POST",
        body: { config_hash: configHash, reason },
        sessionToken: token,
        impersonationId,
      });
      revalidatePath("/agent");
      return {
        ok: true,
        command,
        message:
          `${configHash.slice(0, 12)} is serving inference again. Nothing was re-authored and no ` +
          `version was created — this is the definition that was already published, returned to ` +
          `service. Pinned inferences are untouched: a configuration change is a pinning event, not a ` +
          `re-inference.`,
      };
    } catch (error) {
      return toResult(error, command);
    }
  });
}
