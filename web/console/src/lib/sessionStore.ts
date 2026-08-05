import "server-only";
import { createHash } from "node:crypto";
import type { Session } from "./session";
import { platformFetch } from "./platformApi";

/**
 * sessionStore.ts is where a console session LIVES. It is deliberately the only thing P27 moved.
 *
 * # The defect this closes
 *
 * Sessions lived in a process-local `Map`. `session.ts` said so and named both consequences: a console
 * restart ends every session, and a horizontally-scaled console needs a shared store. P19's Kubernetes
 * overlay declares `replicas: 2` — under which a user signs in against one pod and is signed out by the
 * next request that lands on the other. Intermittently. Which is the worst failure mode to diagnose,
 * because the reproduction depends on which pod a load balancer happened to pick.
 *
 * # Two implementations, one seam, and the memory one is not a fallback
 *
 * `memory` is what a deployment with no platform session backing runs on, and it is honest there: a
 * single container that loses its sessions on restart is a stated property of that shape, not a
 * degradation of it. `platform` is a row in `console_session`.
 *
 * # 🔴 The platform never sees a token
 *
 * This module mints the token, hashes it, and sends only the HASH. There is no field on any of the
 * three requests a plaintext could arrive in. That is stricter than strictly necessary — the console
 * already holds the token — and it is free, and a token in a request body is a token in an access log.
 *
 * # 🔴 A console session is not an API credential
 *
 * The platform stores these rows with `purpose = 'console'`, and its `auth` layer refuses that purpose.
 * So a stolen cookie reaches the console, which exposes a closed set of routes behind its own
 * credential, and stops there. Without the distinction the same cookie would authenticate against the
 * whole platform API — which is why the two live in one table with two purposes rather than one
 * meaning.
 *
 * # What is NOT cached
 *
 * Nothing. Every resolve is a read, which is what keeps revocation effective at the next request with
 * no grace period — the property the in-memory store had and that a durable one must not lose. The
 * platform's own credential path made the same call for the same reason: a cached "yes" is a cached
 * "not yet revoked", and the length of that cache is exactly how long a revoked session keeps working.
 */

/** SessionStore is what `session.ts` writes through. Async, because a durable store is a network hop. */
export type SessionStore = {
  kind: "memory" | "platform";
  create(token: string, session: Session): Promise<void>;
  resolve(token: string): Promise<Session | null>;
  revoke(token: string): Promise<void>;
  /** clear is for tests. The platform store cannot clear anything and says so by refusing. */
  clear(): void;
};

/**
 * hashToken is the ONE way a token becomes a stored value, and it matches the platform's
 * `tenancy.HashSecret` byte for byte — SHA-256, hex, of the trimmed value.
 *
 * The two must agree or every session resolves to nothing. They are asserted equal by
 * `tests/console-session.test.mjs`, not by comment: two hash functions written from one sentence is
 * exactly the shape that drifts.
 */
export function hashToken(token: string): string {
  return createHash("sha256").update(token.trim()).digest("hex");
}

// ── memory ──────────────────────────────────────────────────────────────────────────────────────────

/**
 * The in-process store, anchored to `globalThis`.
 *
 * A plain module-level `Map` passes every test under `next start` and then, under `next dev`, signs you
 * in and immediately tells you your session ended: Next compiles route handlers and pages into SEPARATE
 * module graphs, so the module is instantiated more than once and the sign-in handler writes into a
 * different Map from the one the page reads. That defect is why this symbol exists, and it is worth
 * keeping the note beside it.
 */
const SESSION_STORE = Symbol.for("heros.console.sessions");
type SessionGlobal = typeof globalThis & { [SESSION_STORE]?: Map<string, Session> };

function memoryMap(): Map<string, Session> {
  const scope = globalThis as SessionGlobal;
  if (!scope[SESSION_STORE]) scope[SESSION_STORE] = new Map<string, Session>();
  return scope[SESSION_STORE];
}

const memoryStore: SessionStore = {
  kind: "memory",
  async create(token, session) {
    memoryMap().set(token, session);
  },
  async resolve(token) {
    return memoryMap().get(token) ?? null;
  },
  async revoke(token) {
    const session = memoryMap().get(token);
    if (session) session.revokedAt = Date.now();
  },
  clear() {
    memoryMap().clear();
  },
};

// ── platform ────────────────────────────────────────────────────────────────────────────────────────

type ConsoleSessionView = {
  session_id: string;
  tenant_id: string;
  user_id?: string;
  issued_at: number;
  expires_at: number;
  revoked_at?: number;
};

function toSession(view: ConsoleSessionView): Session {
  const session: Session = {
    id: view.session_id,
    tenantId: view.tenant_id,
    issuedAt: view.issued_at,
    expiresAt: view.expires_at,
  };
  // 🔴 Set only when present. Assigning `view.user_id` unconditionally would put `undefined` on the
  // field, which is the same value — and would make a future `"user_id": ""` from the platform become
  // an empty-string user rather than an absent one.
  if (view.user_id) session.userId = view.user_id;
  if (view.revoked_at) session.revokedAt = view.revoked_at;
  return session;
}

const platformStore: SessionStore = {
  kind: "platform",
  async create(token, session) {
    const outcome = await platformFetch<ConsoleSessionView>("/api/v1/console-sessions", {
      tenantId: session.tenantId,
      method: "POST",
      body: {
        token_hash: hashToken(token),
        session_id: session.id,
        tenant_id: session.tenantId,
        user_id: session.userId ?? "",
        issued_at: session.issuedAt,
        expires_at: session.expiresAt,
      },
    });
    if (!outcome.ok) {
      // 🔴 THROW. A sign-in whose session was not recorded must not appear to succeed: the browser
      // would receive a cookie that resolves to nothing on its very next request, which reads as "the
      // product signed me in and then signed me out" rather than as an outage.
      throw new Error(`the session could not be recorded: ${outcome.error}`);
    }
  },
  async resolve(token) {
    const outcome = await platformFetch<ConsoleSessionView>("/api/v1/console-sessions/resolve", {
      // The scope is the session's own, and it is not yet known — this call is what discovers it. The
      // platform reads the organization from the stored row, never from here.
      tenantId: "",
      method: "POST",
      body: { token_hash: hashToken(token) },
    });
    // Every failure is "no session", including a transport failure. That is the fail-closed reading:
    // a console that cannot reach the platform does not know whether this session is live, and "I do
    // not know" must not resolve to "yes".
    if (!outcome.ok) return null;
    return toSession(outcome.data);
  },
  async revoke(token) {
    await platformFetch<unknown>("/api/v1/console-sessions/revoke", {
      tenantId: "",
      method: "POST",
      body: { token_hash: hashToken(token) },
    });
    // A failed revoke is not swallowed silently by accident — it is swallowed DELIBERATELY, because the
    // caller is a sign-out handler and there is nothing useful it can do. The session still expires at
    // its TTL, and the alternative (a 500 on sign-out) tells a user their sign-out failed when the
    // thing that actually failed was our record of it.
  },
  clear() {
    throw new Error("the platform session store cannot be cleared; it is not a test double");
  },
};

// ── selection ───────────────────────────────────────────────────────────────────────────────────────

/**
 * CONSOLE_SESSION_STORE selects the backing. `platform` is DECLARED, never inferred.
 *
 * Inferring it from whether a platform credential is configured would be wrong in both directions: a
 * deployment can hold that credential and still want in-process sessions, and one that wants durable
 * sessions and has a misconfigured credential would silently get the memory store and discover it as a
 * mass logout on the next deploy.
 */
const DECLARED = (process.env.CONSOLE_SESSION_STORE ?? "memory").trim().toLowerCase();

if (DECLARED !== "memory" && DECLARED !== "platform") {
  // Refuse to boot rather than fall back. A session store nobody chose is the kind of default that is
  // discovered during an incident.
  throw new Error(
    `CONSOLE_SESSION_STORE=${DECLARED} is not a session store; it must be "memory" or "platform"`,
  );
}

export const sessionStore: SessionStore = DECLARED === "platform" ? platformStore : memoryStore;

/** describeSessionStore names the live backing, for the console's own health surface. */
export function describeSessionStore(): { kind: string; durable: boolean } {
  return { kind: sessionStore.kind, durable: sessionStore.kind === "platform" };
}
