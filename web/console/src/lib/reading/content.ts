import "server-only";

/**
 * content.ts is the SERVER-SIDE door to the corpus.
 *
 * It exists only to attach `server-only` to `corpus.ts`, which cannot carry it: the build scripts import
 * that file directly under Node's type stripping, and `server-only` throws in any context that is not a
 * React Server Component. See `corpus.ts`'s own header for why one parser serves both callers.
 *
 * Pages and route handlers import from HERE. A client component that reaches for the corpus gets a build
 * error naming the rule, which is the whole reason the guard is worth the extra file: the corpus reads
 * the filesystem, and a filesystem read that compiles into a browser bundle fails at runtime with a
 * message about `node:fs` rather than about the mistake.
 */
export * from "./corpus.ts";
