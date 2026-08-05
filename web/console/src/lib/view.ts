import "server-only";
import { requireSession, type Session } from "./session";
import { scoped, type Scoped } from "./scope";
import { platformFetch, type PlatformOutcome } from "./platformApi";

/**
 * view.ts is the one way a page reads a platform read model.
 *
 * # Why every page goes through here
 *
 * Three things must happen on every tenant view, in this order, and any page that does them by hand
 * is a page that can get the order wrong:
 *
 *   1. resolve the session (fail closed — redirect, never render a shell that then 401s);
 *   2. derive the upstream path from the SESSION's tenant, never from client input;
 *   3. return the outcome as a UNION, so the caller cannot ignore the failure class.
 *
 * The third is the one that decays. A helper that threw on failure would let a page write
 * `const board = await loadBoard(id)` and never think about not-mounted, not-found and transport
 * again — and the three would collapse into one `error.tsx`, which is exactly the flattening R5
 * forbids.
 */

export type Loaded<T> = { session: Session; paths: Scoped; outcome: PlatformOutcome<T> };

/**
 * load resolves the session, builds the scoped path, and fetches — returning the outcome unmodified.
 *
 * `requires` names the top-level fields the caller cannot render without. A 200 whose body lacks one
 * becomes an **upstream** failure rather than data.
 *
 * # 🔴 Why this exists, found by the §9.8 acceptance test rather than by a build
 *
 * The console trusted any 200 to match its read model. Given a body that did not — a subsystem
 * answering for a different view, a rolled-back platform, a proxy substituting a page — a view
 * dereferenced a nested field, threw during server render, and Next replaced the WHOLE PAGE with its
 * error output. No frame, no heading, no subject: the reader could not even confirm which variant they
 * had opened, which is the exact failure FR27 exists to prevent.
 *
 * Guarding each dereference at each call site was the first attempt, and it is the wrong shape: there
 * are dozens of them, a new field arrives with every read-model change, and the one that gets missed
 * fails the same way. The boundary is the place where "this is not something I can render" is
 * expressible **once**.
 *
 * # Why `upstream` and not one of the other three classes
 *
 * The platform answered, so it is not a transport failure. It answered 200, so it is neither
 * not-mounted nor not-found. A fourth answer already exists for *the platform responded and the
 * response is not usable*, and this is that. Collapsing it into any of the other three would send the
 * reader after the wrong remedy — and rendering it as empty would be the worst of all, because an
 * empty board and a board the console could not read are different facts about the world.
 *
 * # Why a field list and not a schema
 *
 * The generated types already describe the full shape, and re-stating them here as runtime validators
 * would be a second source of truth that drifts from the first. What a view actually needs is a much
 * shorter list — the fields it dereferences without a `??` — and naming those is honest about the
 * dependency rather than pretending to validate.
 */
export async function load<T>(
  path: (paths: Scoped) => string,
  requires: readonly string[] = [],
): Promise<Loaded<T>> {
  const session = await requireSession();
  const paths = scoped(session);
  const outcome = await platformFetch<T>(path(paths), { tenantId: session.tenantId, userId: session.userId });

  if (outcome.ok && requires.length > 0) {
    const body = outcome.data as Record<string, unknown> | null;
    const missing = requires.filter((field) => body == null || body[field] === undefined || body[field] === null);
    if (missing.length > 0) {
      return {
        session,
        paths,
        outcome: {
          ok: false,
          kind: "upstream",
          // The status the platform actually returned is kept, not replaced. It answered 200 and that
          // is a true and useful fact: it distinguishes "the platform is fine and we disagree about
          // the shape" from "the platform refused", which have different owners.
          status: outcome.status,
          // The missing field names are the whole diagnostic. Without them the reader — and whoever
          // they escalate to — is left comparing two JSON documents by eye.
          error: `The platform answered, but the response is missing ${missing.join(", ")}, so this view cannot be rendered from it.`,
        },
      };
    }
  }

  return { session, paths, outcome };
}

/** session resolves the session alone, for a view that reads nothing upstream. */
export async function sessionOnly(): Promise<{ session: Session; paths: Scoped }> {
  const session = await requireSession();
  return { session, paths: scoped(session) };
}
