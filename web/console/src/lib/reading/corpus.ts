import { createHash } from "node:crypto";
import { readdir, readFile } from "node:fs/promises";
import { join, relative, sep } from "node:path";
import { parseMarkdown, type Heading, type ParsedMarkdown } from "./markdown.ts";

/**
 * corpus.ts loads the reading surface's corpus and computes each document's IDENTITY (tasks 3.2, 8.3).
 *
 * # 🔴 Why this file has NO `server-only` import and NO `@/` alias
 *
 * It is imported two ways, and that is the point of task 4.1: the generators must emit their manifests
 * from **the same render pass that produces the pages**, so a manifest cannot drift from the page it
 * describes.
 *
 *   the pages   `src/lib/reading/content.ts` re-exports this behind `server-only`
 *   the build   `scripts/*.mjs` import this file DIRECTLY, under Node's type stripping
 *
 * A second parser written in JavaScript for the scripts would be a second answer to "what is this
 * document's hash" — and the first time the two disagreed, the manifest would say a customer accepted a
 * hash the page never showed. So there is one parser, it has no framework imports, and both callers get
 * it. Anything added here must stay importable by plain Node: no `server-only`, no path aliases, no JSX.
 *
 * # 🔴 The one contract in this file that outlives every other artifact in the phase
 *
 * A legal document's identity is `(kind, version, content_hash)` and consent points at THAT TRIPLE, never
 * at a URL (ADR-011, decisions/p23-one-way-doors.md §1.1). "The customer agreed to the Terms" is
 * meaningless if "the Terms" is a URL whose text has since changed.
 *
 * So `contentHash` below is a load-bearing function, not a convenience. Its normalization is fixed in the
 * decision record and repeated here because the two must not drift:
 *
 *   front matter EXCLUDED       a version bump or a metadata fix is not a change to the agreement
 *   line endings normalized     a CRLF checkout must not produce a different hash from an LF one
 *   trailing whitespace stripped, per line
 *   trailing blank lines collapsed
 *
 * A reformat that changes no words changes no hash. A word change always changes it. Both halves matter:
 * the first stops a whitespace commit from invalidating every consent record on file; the second is the
 * entire evidentiary value of the record.
 *
 * # Why the content lives outside `src/`
 *
 * `web/console/content/**` rather than `src/content/**`, because `src/` is walked by `scan-tokens`,
 * `scan-strings` and `scan-markup`, whose rules are about SOURCE. Content gets its own fences
 * (`scan-content`, `scan-links`, `scan-secrets`, `scan-docs-claims`, …) with different rules, and running
 * the source fences over prose would produce findings nobody can act on.
 */

/**
 * CONTENT_ROOT is the corpus. Locale-segmented from day one — see ADR-011.
 *
 * `HEROS_CONTENT_ROOT` overrides it. That exists for ONE caller: the fence fixtures (task 4.14), which
 * must prove each scan goes red by pointing it at a deliberately broken corpus. A fixture that had to
 * write into the real content tree to prove a fence works is a fixture that can leave the tree broken.
 */
/**
 * 🔴 An EMPTY override is treated as ABSENT, and this was a real production defect rather than
 * defensiveness.
 *
 * `??` falls back only on null/undefined, so `HEROS_CONTENT_ROOT=""` — which is exactly what the
 * Kubernetes base and the Compose file declared, meaning "unset, use the in-image default" — won the
 * coalesce and made CONTENT_ROOT the empty string. Every corpus read then resolved against nothing.
 *
 * The failure is silent and, worse, it is CONFIDENT: /documentation rendered its four sections with
 * "No pages in this section yet", and /legal rendered "Not published yet. Nothing is asserted here
 * until the document exists" — while 14 documentation pages and 4 legal documents, including a
 * published Terms of Service, sat in the image the whole time. A deployment that misreports its own
 * legal documents as unpublished is not a cosmetic bug; /legal/terms answered 404 on a document that
 * exists, and P23 consent reads this same corpus.
 *
 * `.trim()` because a whitespace-only value is the same mistake with a space in it.
 */
const contentRootOverride = process.env.HEROS_CONTENT_ROOT?.trim();
export const CONTENT_ROOT = contentRootOverride
  ? contentRootOverride
  : join(process.cwd(), "content");
export const LOCALE = "en";

/**
 * LegalKind is the closed set of legal document kinds. A new kind is a decision, not a new file.
 *
 * `sub-processors` is P24 task 7.1. It is a separate KIND rather than a section of the privacy notice,
 * and the reason is versioning: the set of processors changes on a different clock from the privacy
 * notice's prose, and a material change to WHO RECEIVES DATA has to be able to invalidate a consent
 * grant without dragging every unrelated paragraph through a re-acceptance. One document, one subject,
 * one version line.
 */
export type LegalKind = "terms" | "privacy" | "sub-processors";
export const LEGAL_KINDS: LegalKind[] = ["terms", "privacy", "sub-processors"];

/**
 * LegalFrontMatter is the machine-readable header every legal document must carry.
 *
 * Every field is REQUIRED and the build fails when one is missing (task 8.3). `material` in particular:
 * no machine can judge materiality, so the fence does not decide it — it forces the decision to exist and
 * to be attributable, set in a reviewed pull request and visible in the manifest (Decision 3).
 */
export type LegalFrontMatter = {
  kind: LegalKind;
  version: string;
  effective_date: string;
  authoritative_language: string;
  /** The version this one supersedes, or "none" for the first. Never omitted — an omission reads as "none". */
  supersedes: string;
  material: boolean;
  title: string;
};

/** DocsFrontMatter is the header every documentation page must carry. */
export type DocsFrontMatter = {
  title: string;
  /** The tier this page belongs to: quickstart | guide | reference | glossary. */
  tier: string;
  /** One sentence, used by the section index and by the search index's lead. */
  summary: string;
  /** The platform version this page documents (FR / task 5.5). Checked against the generated facts. */
  platform_version: string;
  /** What the capability deliberately does NOT do (task 5.5), reusing the manifest's own field name. */
  boundary: string;
  /** Sort order inside a section. Absent sorts last, then alphabetically — never randomly. */
  order?: number;
};

export type LegalDocument = {
  frontMatter: LegalFrontMatter;
  contentHash: string;
  route: string;
  versionRoute: string;
  sourcePath: string;
  parsed: ParsedMarkdown;
};

export type DocsPage = {
  frontMatter: DocsFrontMatter;
  contentHash: string;
  /** section/page, e.g. "guides/run-an-eval". The index page's slug is "index". */
  slug: string;
  section: string;
  route: string;
  sourcePath: string;
  parsed: ParsedMarkdown;
  headings: Heading[];
};

/**
 * contentHash is the document identity's third component. See the file header for the normalization and
 * why each step is there.
 */
export function contentHash(body: string): string {
  const normalized = body
    .replace(/\r\n?/g, "\n")
    .split("\n")
    .map((line) => line.replace(/[ \t]+$/, ""))
    .join("\n")
    .replace(/\n+$/, "\n");
  return createHash("sha256").update(normalized, "utf8").digest("hex");
}

/**
 * splitFrontMatter separates the YAML header from the body and reports the body's first line number.
 *
 * The parser accepts SCALARS ONLY — `key: value`, one per line. That is not a shortcut, it is the shape
 * the fence can check: a nested structure in a legal document's header is a structure somebody has to
 * decide the meaning of, and the six fields Decision 3 requires are all scalars.
 */
export function splitFrontMatter(
  source: string,
  file: string,
): { data: Record<string, string>; body: string; bodyStartLine: number } {
  const text = source.replace(/\r\n?/g, "\n");
  if (!text.startsWith("---\n")) {
    throw new Error(`${file}: no front matter — every document on the reading surface declares its own header`);
  }
  const end = text.indexOf("\n---\n", 3);
  if (end < 0) throw new Error(`${file}: unterminated front matter`);
  const header = text.slice(4, end + 1);
  const body = text.slice(end + 5);
  const data: Record<string, string> = {};
  header.split("\n").forEach((line, index) => {
    if (line.trim() === "" || line.trimStart().startsWith("#")) return;
    const match = /^([a-z_]+):\s*(.*)$/.exec(line);
    if (!match) {
      throw new Error(
        `${file}:${index + 2}: front matter takes \`key: value\` scalars only — got ${JSON.stringify(line)}`,
      );
    }
    data[match[1]] = match[2].trim().replace(/^"(.*)"$/, "$1");
  });
  return { data, body, bodyStartLine: header.split("\n").length + 2 };
}

function require_(data: Record<string, string>, key: string, file: string): string {
  const value = data[key];
  if (value === undefined || value === "") {
    throw new Error(
      `${file}: front matter is missing \`${key}\` — a document that does not declare it cannot be published ` +
        `(decisions/p23-one-way-doors.md §1.3)`,
    );
  }
  return value;
}

/** parseLegalFrontMatter validates the six required fields plus the title, and refuses on anything else. */
export function parseLegalFrontMatter(data: Record<string, string>, file: string): LegalFrontMatter {
  const kind = require_(data, "kind", file);
  if (!LEGAL_KINDS.includes(kind as LegalKind)) {
    throw new Error(`${file}: unknown legal kind "${kind}" — the published kinds are ${LEGAL_KINDS.join(", ")}`);
  }
  const version = require_(data, "version", file);
  if (!/^\d+\.\d+\.\d+$/.test(version)) {
    throw new Error(`${file}: version "${version}" is not MAJOR.MINOR.PATCH — the version is a permanent route segment`);
  }
  const effective = require_(data, "effective_date", file);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(effective)) {
    throw new Error(`${file}: effective_date "${effective}" is not YYYY-MM-DD`);
  }
  const material = require_(data, "material", file);
  if (material !== "true" && material !== "false") {
    throw new Error(
      `${file}: material must be exactly true or false. No machine can judge materiality, so this field ` +
        `records a person's decision — and an ambiguous value is not one`,
    );
  }
  return {
    kind: kind as LegalKind,
    version,
    effective_date: effective,
    authoritative_language: require_(data, "authoritative_language", file),
    supersedes: require_(data, "supersedes", file),
    material: material === "true",
    title: require_(data, "title", file),
  };
}

/** parseDocsFrontMatter validates a documentation page's header. */
export function parseDocsFrontMatter(data: Record<string, string>, file: string): DocsFrontMatter {
  const order = data.order === undefined || data.order === "" ? undefined : Number(data.order);
  if (order !== undefined && Number.isNaN(order)) throw new Error(`${file}: order must be a number`);
  return {
    title: require_(data, "title", file),
    tier: require_(data, "tier", file),
    summary: require_(data, "summary", file),
    platform_version: require_(data, "platform_version", file),
    boundary: require_(data, "boundary", file),
    order,
  };
}

async function* walk(dir: string): AsyncGenerator<string> {
  let entries;
  try {
    entries = await readdir(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(full);
    else if (entry.name.endsWith(".md")) yield full;
  }
}

/**
 * loadLegal reads every legal document, of every version.
 *
 * EVERY version is loaded, including superseded ones, because every version stays served forever — a
 * deleted archive orphans every consent record that references it, and that is a one-way door with no
 * recovery (task 8.6 fences the manifest side of the same rule).
 */
export async function loadLegal(): Promise<LegalDocument[]> {
  const root = join(CONTENT_ROOT, "legal", LOCALE);
  const out: LegalDocument[] = [];
  for await (const file of walk(root)) {
    const rel = relative(CONTENT_ROOT, file).split(sep).join("/");
    const source = await readFile(file, "utf8");
    const { data, body, bodyStartLine } = splitFrontMatter(source, rel);
    const frontMatter = parseLegalFrontMatter(data, rel);
    out.push({
      frontMatter,
      contentHash: contentHash(body),
      route: `/legal/${frontMatter.kind}`,
      versionRoute: `/legal/${frontMatter.kind}/v/${frontMatter.version}`,
      sourcePath: rel,
      parsed: parseMarkdown(body, rel, bodyStartLine),
    });
  }
  return out;
}

/** compareVersions orders MAJOR.MINOR.PATCH numerically. String order puts 1.10.0 before 1.9.0. */
export function compareVersions(a: string, b: string): number {
  const pa = a.split(".").map(Number);
  const pb = b.split(".").map(Number);
  for (let i = 0; i < 3; i += 1) {
    if (pa[i] !== pb[i]) return pa[i] - pb[i];
  }
  return 0;
}

/**
 * currentLegal returns the version in force for a kind: the highest version whose effective date has
 * arrived, or — when none has — the lowest version, so a document published ahead of its effective date
 * is still readable rather than 404ing.
 */
export function currentLegal(documents: LegalDocument[], kind: LegalKind, today: string): LegalDocument | null {
  const ofKind = documents
    .filter((d) => d.frontMatter.kind === kind)
    .sort((a, b) => compareVersions(a.frontMatter.version, b.frontMatter.version));
  if (ofKind.length === 0) return null;
  const effective = ofKind.filter((d) => d.frontMatter.effective_date <= today);
  return effective.length > 0 ? effective[effective.length - 1] : ofKind[0];
}

/** loadDocs reads every documentation page. */
export async function loadDocs(): Promise<DocsPage[]> {
  const root = join(CONTENT_ROOT, "docs", LOCALE);
  const out: DocsPage[] = [];
  for await (const file of walk(root)) {
    const rel = relative(CONTENT_ROOT, file).split(sep).join("/");
    const slugPath = relative(root, file).split(sep).join("/").replace(/\.md$/, "");
    const source = await readFile(file, "utf8");
    const { data, body, bodyStartLine } = splitFrontMatter(source, rel);
    const frontMatter = parseDocsFrontMatter(data, rel);
    const parsed = parseMarkdown(body, rel, bodyStartLine);
    const section = slugPath.includes("/") ? slugPath.split("/")[0] : "";
    out.push({
      frontMatter,
      contentHash: contentHash(body),
      slug: slugPath,
      section,
      route: slugPath === "index" ? "/docs" : `/docs/${slugPath}`,
      sourcePath: rel,
      parsed,
      headings: parsed.headings,
    });
  }
  return out;
}

/**
 * SECTIONS names the documentation sections and their order, so the index is not an alphabetical
 * accident. A section with no entry here fails `scan-links`, which is how a page added to a directory
 * nobody navigates to gets noticed.
 */
export const SECTIONS: { id: string; label: string; blurb: string }[] = [
  { id: "start", label: "Get started", blurb: "Install the CLI and build a discovery graph on your own repository." },
  { id: "guides", label: "Guides", blurb: "One job per page: configure, evaluate, wire CI, take delivery." },
  { id: "reference", label: "Reference", blurb: "Generated from what ships: the CLI, the exit codes, the schemas." },
  { id: "concepts", label: "Concepts", blurb: "The product's own nouns, and what a refusal means." },
];

export function sectionLabel(id: string): string {
  return SECTIONS.find((s) => s.id === id)?.label ?? id;
}
