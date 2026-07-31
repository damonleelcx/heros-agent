/**
 * markdown.ts parses the CONSTRAINED Markdown subset the reading surface publishes (task 3.2).
 *
 * # Why this is hand-written rather than `remark` or `marked`
 *
 * ADR-011 refuses a third party with write access to the page where a customer reads what they are
 * agreeing to. A build-time dependency is a weaker version of the same exposure, and it buys support for
 * constructs this surface does not publish — raw HTML, autolinks, footnotes, embedded scripts — every one
 * of which `scan-content.mjs` is written to REFUSE anyway. So the parser covers exactly the subset the
 * fence permits, and the console keeps its five runtime dependencies.
 *
 * It also has to: `scan-markup.mjs` bans `dangerouslySetInnerHTML` across `src/`, so nothing here may
 * produce an HTML string. It produces a typed tree, and `components/reading/prose.tsx` renders it as
 * React elements — which is the whole class of injection defect removed structurally rather than escaped
 * carefully.
 *
 * # 🔴 The rule that makes a small parser safe: an unsupported construct FAILS, it does not degrade
 *
 * A lenient parser renders `<script>alert(1)</script>` as literal text and a `<b>bold</b>` as literal
 * text, and an author concludes the syntax "did not work" rather than that it is forbidden. Worse, the
 * silent-passthrough version of that mistake is how raw markup reaches a page.
 *
 * Every refusal below throws with the file and the line, at build time, so the author meets the boundary
 * while they are writing rather than a reader meeting a mis-rendered page.
 *
 * # What this deliberately does NOT support
 *
 *   raw HTML of any kind            refused — `scan-content` refuses it independently
 *   reference links `[a][b]`        refused; inline links only, so a link's target is where it is read
 *   setext headings (`===` under)   refused; ATX only, so heading level is unambiguous in the source
 *   images                          refused — no `img` on this surface; a figure is a code block or a table
 *   footnotes, definition lists     not published on this surface
 *   inline HTML entities            passed through as text; React escapes them, which is correct
 *   nested lists deeper than one    refused rather than silently flattened
 *
 * The list is here rather than in a commit message because the next author's first question is "can I
 * write X", and the honest answer must be readable next to the code that decides it.
 */

/** Inline is the span-level vocabulary. Deliberately five cases; there is no sixth escape hatch. */
export type Inline =
  | { kind: "text"; value: string }
  | { kind: "strong"; children: Inline[] }
  | { kind: "em"; children: Inline[] }
  | { kind: "code"; value: string }
  | { kind: "link"; href: string; children: Inline[] };

/** A code sample inside a `:::tabs` container — one language, one label. */
export type CodeSample = { label: string; lang: string; value: string };

/** Block is the block-level vocabulary. */
export type Block =
  | { kind: "heading"; level: 2 | 3 | 4 | 5 | 6; slug: string; text: string; children: Inline[] }
  | { kind: "paragraph"; children: Inline[] }
  | { kind: "code"; lang: string; value: string }
  | { kind: "tabs"; samples: CodeSample[] }
  | { kind: "list"; ordered: boolean; items: Inline[][] }
  | { kind: "quote"; children: Block[] }
  | { kind: "table"; head: Inline[][]; rows: Inline[][][] }
  | { kind: "rule" };

/** Heading is the flattened outline a table of contents and the slug manifest are both built from. */
export type Heading = { level: 2 | 3 | 4 | 5 | 6; slug: string; text: string };

export type ParsedMarkdown = { blocks: Block[]; headings: Heading[] };

/**
 * MarkdownError names the file and the line. A parse failure that says only "unexpected token" makes an
 * author bisect their own document, which is a worse experience than the syntax error itself.
 */
export class MarkdownError extends Error {
  constructor(file: string, line: number, message: string) {
    super(`${file}:${line}: ${message}`);
    this.name = "MarkdownError";
  }
}

/**
 * RAW_HTML matches an HTML tag. It is deliberately broad — `<` followed by a letter or a slash — because
 * the narrow version is the one that lets `<sCrIpT` through, and this surface publishes no markup at all,
 * so there is nothing legitimate for a broad rule to catch by accident.
 *
 * A literal `<` in prose is written `&lt;`, and in code it needs no escape because a code span and a code
 * fence are both taken verbatim before this rule runs.
 */
const RAW_HTML = /<\/?[a-zA-Z][^>]*>/;

/**
 * slugify produces the anchor a heading publishes. GitHub's rule, because that is the one authors expect
 * and the one an external reader's muscle memory already has.
 *
 * Anchors are a PUBLISHED CONTRACT (Decision 8): a CLI error message and a console empty state deep-link
 * into these, and the message ships inside a binary the customer already installed. So the rule lives in
 * one function, and `scan-links.mjs` checks every reference against the manifest this produces.
 */
export function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[`*_[\]()]/g, "")
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-");
}

/** inlineText flattens an inline tree to its readable text — for slugs, search and the TOC. */
export function inlineText(nodes: Inline[]): string {
  return nodes
    .map((node) => {
      switch (node.kind) {
        case "text":
          return node.value;
        case "code":
          return node.value;
        default:
          return inlineText(node.children);
      }
    })
    .join("");
}

/**
 * parseInline turns one line of span-level Markdown into a tree.
 *
 * Code spans are taken FIRST and verbatim, so `` `**not bold**` `` renders as it is written — the rule
 * every Markdown reader already relies on, and the one a naive regex pass gets wrong.
 */
function parseInline(source: string, file: string, line: number): Inline[] {
  if (RAW_HTML.test(source)) {
    throw new MarkdownError(
      file,
      line,
      `raw HTML is not published on this surface — found ${RAW_HTML.exec(source)?.[0]}. ` +
        `Write it as Markdown, or as \`&lt;\` if you mean the character.`,
    );
  }
  if (/\]\s*\[/.test(source)) {
    throw new MarkdownError(file, line, "reference links are not supported — write the target inline");
  }
  if (/!\[/.test(source)) {
    throw new MarkdownError(file, line, "images are not published on this surface");
  }

  const out: Inline[] = [];
  let rest = source;

  // One pass, longest-match-first. The order is load-bearing: code before emphasis, and `**` before `*`.
  const patterns: [RegExp, (m: RegExpExecArray) => Inline][] = [
    [/^`([^`]+)`/, (m) => ({ kind: "code", value: m[1] })],
    [/^\*\*([^*]+)\*\*/, (m) => ({ kind: "strong", children: parseInline(m[1], file, line) })],
    [/^\*([^*]+)\*/, (m) => ({ kind: "em", children: parseInline(m[1], file, line) })],
    [/^_([^_]+)_/, (m) => ({ kind: "em", children: parseInline(m[1], file, line) })],
    [
      /^\[([^\]]+)\]\(([^)\s]+)\)/,
      (m) => ({ kind: "link", href: m[2], children: parseInline(m[1], file, line) }),
    ],
  ];

  let buffer = "";
  while (rest.length > 0) {
    let matched = false;
    for (const [pattern, build] of patterns) {
      const m = pattern.exec(rest);
      if (!m) continue;
      if (buffer) {
        out.push({ kind: "text", value: buffer });
        buffer = "";
      }
      out.push(build(m));
      rest = rest.slice(m[0].length);
      matched = true;
      break;
    }
    if (matched) continue;
    buffer += rest[0];
    rest = rest.slice(1);
  }
  if (buffer) out.push({ kind: "text", value: buffer });
  return out;
}

/**
 * splitRow splits a GFM table row on UNESCAPED pipes and trims each cell.
 *
 * The escape handling is not a nicety. A naive `.split("|")` broke the generated CLI reference the first
 * time it ran: `--apply-mode`'s summary is literally `inline | bound`, the generator correctly escaped it
 * as `inline \| bound`, and the split then produced a six-cell row under a five-column header. The parser
 * refused the row — which is the behaviour that turned a silently-lost value into a build failure — but
 * the row was correct and the splitter was wrong.
 *
 * So `\|` is a literal pipe inside a cell, and the backslash is removed on the way out.
 */
function splitRow(source: string): string[] {
  const trimmed = source.replace(/^\s*\|/, "").replace(/\|\s*$/, "");
  const cells: string[] = [];
  let current = "";
  for (let i = 0; i < trimmed.length; i += 1) {
    if (trimmed[i] === "\\" && trimmed[i + 1] === "|") {
      current += "|";
      i += 1;
      continue;
    }
    if (trimmed[i] === "|") {
      cells.push(current.trim());
      current = "";
      continue;
    }
    current += trimmed[i];
  }
  cells.push(current.trim());
  return cells;
}

/** infoString parses a fence's info — `bash label="macOS"` — into a language and an optional label. */
function infoString(info: string): { lang: string; label: string | null } {
  const lang = /^[a-z0-9+#-]*/i.exec(info.trim())?.[0] ?? "";
  const label = /\blabel="([^"]+)"/.exec(info)?.[1] ?? null;
  return { lang, label };
}

/**
 * parseMarkdown parses a document BODY (front matter already removed) into blocks and an outline.
 *
 * `startLine` is the line the body begins on in the original file, so every error message points at a
 * line number the author can find in their editor rather than at an offset into a substring.
 */
export function parseMarkdown(body: string, file: string, startLine = 1): ParsedMarkdown {
  const lines = body.split("\n");
  const blocks: Block[] = [];
  const headings: Heading[] = [];
  const seenSlugs = new Map<string, number>();
  let i = 0;

  const at = (n: number) => startLine + n;

  /** claimSlug de-duplicates repeated headings the way an anchor must: deterministically. */
  function claimSlug(text: string, line: number): string {
    const base = slugify(text);
    if (!base) throw new MarkdownError(file, line, "a heading must contain at least one word character");
    const seen = seenSlugs.get(base) ?? 0;
    seenSlugs.set(base, seen + 1);
    return seen === 0 ? base : `${base}-${seen}`;
  }

  while (i < lines.length) {
    const line = lines[i];

    if (line.trim() === "") {
      i += 1;
      continue;
    }

    // ── `:::tabs` — a multi-language sample container (task 3.5) ───────────────
    if (line.trim().startsWith(":::")) {
      const directive = line.trim().slice(3).trim();
      if (directive !== "tabs") {
        throw new MarkdownError(
          file,
          at(i),
          `unknown container directive ":::${directive}" — the only one published is ":::tabs"`,
        );
      }
      const openedAt = at(i);
      i += 1;
      const samples: CodeSample[] = [];
      while (i < lines.length && !lines[i].trim().startsWith(":::")) {
        if (lines[i].trim() === "") {
          i += 1;
          continue;
        }
        const fence = /^```(.*)$/.exec(lines[i]);
        if (!fence) {
          throw new MarkdownError(
            file,
            at(i),
            "a :::tabs container holds code fences only — prose belongs above or below it",
          );
        }
        const { lang, label } = infoString(fence[1]);
        if (!label) {
          throw new MarkdownError(
            file,
            at(i),
            'every fence inside :::tabs needs a label, e.g. ```bash label="macOS" — an unlabelled tab ' +
              "is a tab strip a reader cannot choose from",
          );
        }
        i += 1;
        const buf: string[] = [];
        while (i < lines.length && lines[i].trim() !== "```") {
          buf.push(lines[i]);
          i += 1;
        }
        if (i >= lines.length) throw new MarkdownError(file, openedAt, "unterminated code fence inside :::tabs");
        i += 1;
        samples.push({ label, lang, value: buf.join("\n") });
      }
      if (i >= lines.length) throw new MarkdownError(file, openedAt, "unterminated :::tabs container");
      i += 1;
      if (samples.length < 2) {
        throw new MarkdownError(
          file,
          openedAt,
          "a :::tabs container with fewer than two samples is a code block wearing a tab strip — write the code block",
        );
      }
      blocks.push({ kind: "tabs", samples });
      continue;
    }

    // ── fenced code ───────────────────────────────────────────────────────────
    const fence = /^```(.*)$/.exec(line);
    if (fence) {
      const openedAt = at(i);
      const { lang } = infoString(fence[1]);
      i += 1;
      const buf: string[] = [];
      while (i < lines.length && lines[i].trim() !== "```") {
        buf.push(lines[i]);
        i += 1;
      }
      if (i >= lines.length) throw new MarkdownError(file, openedAt, "unterminated code fence");
      i += 1;
      blocks.push({ kind: "code", lang, value: buf.join("\n") });
      continue;
    }

    // ── heading ───────────────────────────────────────────────────────────────
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      const level = heading[1].length;
      if (level === 1) {
        throw new MarkdownError(
          file,
          at(i),
          "a document body may not contain an H1 — the page renders one from the front matter's `title`, " +
            "and a second one makes the document outline ambiguous to a screen reader",
        );
      }
      const children = parseInline(heading[2].trim(), file, at(i));
      const text = inlineText(children);
      const slug = claimSlug(text, at(i));
      blocks.push({ kind: "heading", level: level as 2 | 3 | 4 | 5 | 6, slug, text, children });
      headings.push({ level: level as 2 | 3 | 4 | 5 | 6, slug, text });
      i += 1;
      continue;
    }

    // ── thematic break ────────────────────────────────────────────────────────
    if (/^(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      blocks.push({ kind: "rule" });
      i += 1;
      continue;
    }

    // ── table ─────────────────────────────────────────────────────────────────
    if (line.trim().startsWith("|") && i + 1 < lines.length && /^\s*\|?[\s:|-]+\|[\s:|-]*$/.test(lines[i + 1])) {
      const head = splitRow(line).map((cell) => parseInline(cell, file, at(i)));
      const columns = head.length;
      i += 2;
      const rows: Inline[][][] = [];
      while (i < lines.length && lines[i].trim().startsWith("|")) {
        const cells = splitRow(lines[i]);
        if (cells.length !== columns) {
          throw new MarkdownError(
            file,
            at(i),
            `table row has ${cells.length} cells but the header has ${columns} — a row that does not line ` +
              `up renders as a table that silently loses a value`,
          );
        }
        rows.push(cells.map((cell) => parseInline(cell, file, at(i))));
        i += 1;
      }
      blocks.push({ kind: "table", head, rows });
      continue;
    }

    // ── list ──────────────────────────────────────────────────────────────────
    const bullet = /^(\s*)([-*]|\d+\.)\s+(.*)$/.exec(line);
    if (bullet) {
      if (bullet[1].length > 0) {
        throw new MarkdownError(
          file,
          at(i),
          "nested lists are not published on this surface — a second level of nesting usually wants to be a " +
            "table or a heading, and both survive a print stylesheet better",
        );
      }
      const ordered = /\d/.test(bullet[2]);
      const items: Inline[][] = [];
      while (i < lines.length) {
        const item = /^(\s*)([-*]|\d+\.)\s+(.*)$/.exec(lines[i]);
        if (!item || item[1].length > 0) break;
        if (/\d/.test(item[2]) !== ordered) break;
        // A continuation line is indented under its bullet; join it so a long item may be wrapped in
        // source without becoming two items.
        let text = item[3];
        i += 1;
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !/^\s*([-*]|\d+\.)\s/.test(lines[i])) {
          text += ` ${lines[i].trim()}`;
          i += 1;
        }
        items.push(parseInline(text.trim(), file, at(i - 1)));
      }
      blocks.push({ kind: "list", ordered, items });
      continue;
    }

    // ── blockquote ────────────────────────────────────────────────────────────
    if (/^>\s?/.test(line)) {
      const buf: string[] = [];
      const openedAt = i;
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^>\s?/, ""));
        i += 1;
      }
      const inner = parseMarkdown(buf.join("\n"), file, at(openedAt));
      // A heading inside a quote would inject an anchor into the outline from inside a citation, which is
      // how a table of contents ends up listing somebody else's words as one of our sections.
      if (inner.headings.length > 0) {
        throw new MarkdownError(file, at(openedAt), "a blockquote may not contain a heading");
      }
      blocks.push({ kind: "quote", children: inner.blocks });
      continue;
    }

    // ── paragraph ─────────────────────────────────────────────────────────────
    const buf: string[] = [];
    const openedAt = i;
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^(#{1,6}\s|```|>|\s*([-*]|\d+\.)\s|:::)/.test(lines[i]) &&
      !/^(-{3,}|\*{3,}|_{3,})\s*$/.test(lines[i]) &&
      !lines[i].trim().startsWith("|")
    ) {
      buf.push(lines[i].trim());
      i += 1;
    }
    if (buf.length === 0) {
      throw new MarkdownError(file, at(i), `could not parse: ${lines[i].slice(0, 60)}`);
    }
    blocks.push({ kind: "paragraph", children: parseInline(buf.join(" "), file, at(openedAt)) });
  }

  return { blocks, headings };
}
