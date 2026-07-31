import "server-only";
import { readFile } from "node:fs/promises";
import { join, basename } from "node:path";

/**
 * secrets.ts is the console's `Secrets` seam for IDENTITY credentials (P22 Decision 6, task 5.3).
 *
 * # Why the console needs its own implementation of a seam the platform already has
 *
 * `providergateway.Secrets` is Go, and it is reached at the moment of use — inside the process that
 * holds the credential. The console is a separate process in a separate language, so it cannot import
 * that seam; what it can do, and what `secrets-baseline.md` §1.1 actually asks for, is present the
 * SAME contract with the SAME externally-checkable answer: `HEROS_SECRETS_SOURCE` selects a source,
 * `Describe()` names it, and `/api/health` reports the name so an operator can tell — from the running
 * system, not from a manifest — whether a manager is involved at all.
 *
 * The failure §1.1 was written against is exactly the one to avoid here: a deployment whose LLM and
 * billing credentials come from a manager while the OIDC client secret quietly comes from an
 * environment variable, with the health surface confidently reporting the manager.
 *
 * # The two sources, and why there is no AWS client in this process
 *
 * | kind   | where the value is read from                  | bootstrap secret |
 * |--------|-----------------------------------------------|------------------|
 * | `env`  | a secrets-manager-INJECTED environment variable | none — the injector authenticated |
 * | `file` | a file under `HEROS_SECRETS_DIR`, read at the moment of use | none — the mount was granted by workload identity |
 *
 * `file` is not a lesser option; it is how a managed deployment actually reaches AWS Secrets Manager
 * from a process that should not carry an AWS client: the Secrets Store CSI driver (or a Vault agent
 * file sink, or an air-gapped operator's own mount) projects the manager's value onto a path, the pod
 * is authorised for it AMBIENTLY by its service account, and nothing anywhere holds a long-lived key
 * to reach the manager. That is the property `secrets-baseline.md` §1.1 calls the L1 tiebreak — a
 * manager reached with a long-lived key in an env var has moved the secret, not removed it — and it is
 * preserved here without adding a dependency to a browser-facing process.
 *
 * Reading at the moment of use (rather than at boot) is what makes rotation work: a rotated file is
 * picked up by the next sign-in, with no restart and no cached copy of the old value in memory.
 *
 * # What is deliberately absent
 *
 * Nothing returns a secret for logging, formatting, or inclusion in an event. `describeSecrets()`
 * names the SOURCE — never a value, never a path that contains one, never a secret's id.
 */

/** Reserved logical names. Constants, because a name spelled two ways is a credential nobody provisioned. */
export const SECRET_OIDC_CLIENT_SECRET = "console_idp_client_secret";
export const SECRET_SAML_SP_PRIVATE_KEY = "console_saml_sp_private_key";

/** SECRET_NAMES is every logical name this console reads, so a checklist iterates the real set. */
export const SECRET_NAMES = [SECRET_OIDC_CLIENT_SECRET, SECRET_SAML_SP_PRIVATE_KEY] as const;

/**
 * ErrSecretUnavailable fails CLOSED. No client secret means no code exchange and therefore no session
 * — never a fallback to an unauthenticated token request, and never a weaker mechanism.
 */
export class SecretUnavailableError extends Error {
  constructor(name: string, detail: string) {
    // Names WHICH credential was missing, never its value.
    super(`identity credential unavailable from the secrets source: ${name}: ${detail}`);
    this.name = "SecretUnavailableError";
  }
}

const SOURCE = (process.env.HEROS_SECRETS_SOURCE ?? "env").trim().toLowerCase();
const SECRETS_DIR = process.env.HEROS_SECRETS_DIR ?? "/var/run/secrets/heros";

/**
 * SECRET_PATHS maps a logical name to where this deployment keeps it — CONFIGURATION, not code
 * (task 7.2).
 *
 * The default below is a NAMING CONVENTION, not a mapping table: logical name → same-named env var /
 * same-named file. A deployment whose manager uses different paths overrides the whole map with
 * `CONSOLE_IDP_SECRET_MAP`, so adding a provider is a config injection rather than a new `case` in a
 * switch nobody can change without a release.
 */
function secretLocation(name: string): string {
  const overrides = process.env.CONSOLE_IDP_SECRET_MAP;
  if (overrides) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(overrides);
    } catch {
      throw new Error("CONSOLE_IDP_SECRET_MAP is set but is not valid JSON");
    }
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("CONSOLE_IDP_SECRET_MAP is set but is not a JSON object of logical name → location");
    }
    const at = (parsed as Record<string, unknown>)[name];
    if (at !== undefined) return String(at);
  }
  return name.toUpperCase();
}

/** identitySecret resolves one identity credential at the moment of use, or fails closed. */
export async function identitySecret(name: string): Promise<string> {
  const location = secretLocation(name);
  if (SOURCE === "file") {
    // `basename` on purpose: a location is a NAME under the secrets directory, never a path that can
    // climb out of it. A mapping document is configuration, and configuration should not be able to
    // read `/etc/shadow` because somebody wrote `../../` in it.
    const path = join(SECRETS_DIR, basename(location));
    try {
      const value = (await readFile(path, "utf8")).trim();
      if (!value) throw new Error("resolved empty");
      return value;
    } catch (err) {
      // The error names the logical name and the source kind, never the path's contents.
      throw new SecretUnavailableError(name, err instanceof Error ? err.message : "unreadable");
    }
  }
  if (SOURCE !== "env") {
    // An unknown source is a refusal, not a silent fall back to `env`. 🔴 no-lazy-defaults: falling
    // back here would make `HEROS_SECRETS_SOURCE=aws-secretz-manager` a typo that silently downgrades
    // a deployment's whole secret posture while /api/health still reports something plausible.
    throw new SecretUnavailableError(name, `unknown HEROS_SECRETS_SOURCE "${SOURCE}"`);
  }
  const value = (process.env[location] ?? "").trim();
  if (!value) throw new SecretUnavailableError(name, "not present in the injected environment");
  return value;
}

/** SecretsSourceInfo mirrors the platform's `/readyz → secrets_source` shape. Never a value. */
export type SecretsSourceInfo = { kind: string; detail: string };

/** describeSecrets names the live source for the health surface. */
export function describeSecrets(): SecretsSourceInfo {
  if (SOURCE === "file") return { kind: "file", detail: SECRETS_DIR };
  return { kind: "env", detail: "secrets-manager-injected environment" };
}
