// prose.mjs extracts the STATIC PROSE a working route ships — the words a reader meets that were
// written by us rather than produced from their data (P37 FR9, FR10).
//
// # Why the extraction lives in a library rather than inside the scan
//
// Two callers need the same ruler and must not have two of them: `scan-prose.mjs` fails the build on a
// route over budget, and `tests/p37-prose.test.mjs` proves that fence goes red. A test with its own copy
// of the counter proves that copy works, which is not the claim anybody wanted.
//
// # 🔴 What this counts, and the blind spot stated in the same breath (design D3)
//
// It counts JSX text nodes and display string literals, with comments, imports, class names, routes and
// identifiers removed. It is a measure of VOLUME and nothing else.
//
// A word count is gameable. The same content survives as three shorter blocks, as a tooltip, as an
// accordion or as a modal, and this scan cannot see any of that — which is why FR11 forbids those
// destinations outright and why `scan-prose` refuses a route that grows a disclosure widget. A fence
// whose weakness is undocumented is a fence that will be cited as proof of something it never checked.

/** stripComments removes block and line comments so a doc comment never counts as prose. */
export function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^[ \t]*\/\/.*$/gm, "");
}

/**
 * CLASS_TOKEN matches the shape of a Tailwind utility. A `className` string is markup instruction, not
 * prose, and counting it would make a layout change look like an authoring change.
 */
const CLASS_TOKEN =
  /^(?:[a-z0-9:[\]()\-./#%_]+\s+)*[a-z0-9:[\]()\-./#%_]+$/;

/**
 * CODE_SIGNALS are punctuation sequences that essentially never appear in UI copy and always appear in
 * source.
 *
 * 🔴 Without this the ruler was measuring the wrong thing, and it was found by measuring rather than by
 * review. `>([^<>]+)<` matches the run between the `>` of a TypeScript generic and the next `<`, so
 * `Promise<T> { const res = await fetch(` counted as eleven words of prose. On an interactive surface
 * that is most of the "prose" — `/app/studio` measured 1,268 words in one file that renders perhaps
 * forty.
 *
 * A budget computed from a ruler that counts `=>` chains is a budget nobody can meet by writing less,
 * which is the failure mode that gets a fence deleted rather than obeyed. Fixed BEFORE the fence is
 * enforced (task 1.5 says the numbers are tuned against the six real surfaces first), and the ratchet
 * values in `prose-budgets.mjs` are re-measured with the corrected ruler.
 *
 * Parentheses are deliberately NOT here: prose uses them constantly.
 */
const CODE_SIGNALS = /[;={}`]|=>|\?\?|\|\||&&|::|\/\//;

/** looksLikeCode rejects a literal that is an identifier, a route, a class list or a wire value. */
function looksLikeCode(text) {
  const trimmed = text.trim();
  if (trimmed.length === 0) return true;
  if (CODE_SIGNALS.test(trimmed)) return true;
  if (/^[/#]/.test(trimmed)) return true; // a route or an anchor
  if (/^https?:/.test(trimmed)) return true;
  if (/^[A-Za-z0-9_.-]+$/.test(trimmed)) return true; // one identifier-ish token
  // A class list: every token is lower case with no sentence punctuation anywhere.
  if (!/[.!?,;:—]/.test(trimmed) && CLASS_TOKEN.test(trimmed) && /-/.test(trimmed)) return true;
  return false;
}

/**
 * LABEL_WORDS separates a LABEL from PROSE, and the line has to be drawn somewhere.
 *
 * 🔴 A button reading `Save`, a column headed `Apply mode` and a chip reading `unverified` are CONTROLS.
 * Counting them makes the ruler measure UI density instead of documentation, and it makes an
 * interactive surface — which is what this whole phase is trying to produce — score worse than the
 * document it replaced. `/app/studio` renders roughly forty words of explanation across five files and
 * several hundred one- and two-word control labels.
 *
 * Five is where a run stops being a label and starts being a sentence. "Select a cell to edit its
 * prompt" (7) counts; "Apply mode" (2) does not.
 *
 * 🔴 This WIDENS design D3's stated blind spot — content can now also hide as a run of four-word
 * fragments — and D3 already names the mitigation: the fence is paired with FR11's prohibition on
 * tooltips, accordions and modals, and with review. It is stated here rather than left for somebody to
 * discover, because a fence whose limits are unstated is a fence that will be trusted past them.
 */
const LABEL_WORDS = 5;

/** words counts whitespace-separated words that contain a letter. */
export function words(text) {
  return text
    .split(/\s+/)
    .filter((token) => /[A-Za-z]/.test(token)).length;
}

/**
 * proseBlocks returns every static prose block in a source file, in source order.
 *
 * A BLOCK is one JSX text run or one display string literal. Blocks are returned rather than a total so
 * the lede rule (FR10 — one lede, capped) can be checked on the first block of a page without a second
 * pass with a second set of rules.
 */
export function proseBlocks(source) {
  let src = stripComments(source);
  // Imports carry module paths, not prose.
  src = src.replace(/^\s*import[\s\S]*?from\s*["'][^"']+["'];?$/gm, "");
  // `className` values are markup instruction. Removed before anything else looks at a literal.
  src = src.replace(/className=(?:"[^"]*"|'[^']*'|\{[^}]*\})/g, "");
  src = src.replace(/\bcx\([^)]*\)/g, "");

  const blocks = [];

  // 1. JSX text runs. `>` … `<` with the embedded expressions removed, which is what a reader sees
  //    minus the values that come from their own data.
  for (const match of src.matchAll(/>([^<>]+)</g)) {
    const text = match[1].replace(/\{[^{}]*\}/g, " ").replace(/&[a-z]+;/g, "'");
    if (looksLikeCode(text)) continue;
    const count = words(text);
    if (count < LABEL_WORDS) continue;
    blocks.push({ kind: "jsx-text", text: text.trim(), words: count, index: match.index ?? 0 });
  }

  // 2. Display string literals — a sentence stored in a constant and rendered later. The threshold is
  //    four words: below that a literal is a label, and a label is not prose.
  for (const match of src.matchAll(/(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1/g)) {
    const text = match[2];
    if (looksLikeCode(text)) continue;
    // A literal carrying an interpolation, a path or a tag is a wire value, not a sentence a reader is
    // meant to read as English.
    if (/\$\{|\/|</.test(text)) continue;
    const count = words(text);
    if (count < LABEL_WORDS) continue;
    blocks.push({ kind: "literal", text: text.trim(), words: count, index: match.index ?? 0 });
  }

  // 3. Template literals with no interpolation — the same thing written with backticks.
  for (const match of src.matchAll(/`([^`$\\]*)`/g)) {
    const text = match[1];
    if (looksLikeCode(text)) continue;
    const count = words(text);
    if (count < LABEL_WORDS) continue;
    blocks.push({ kind: "template", text: text.trim(), words: count, index: match.index ?? 0 });
  }

  return blocks.sort((a, b) => a.index - b.index);
}

/** totalWords sums a file's prose blocks. */
export function totalWords(source) {
  return proseBlocks(source).reduce((sum, block) => sum + block.words, 0);
}
