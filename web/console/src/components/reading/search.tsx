"use client";

import Link from "next/link";
import { useMemo, useState } from "react";
import index from "@/generated/search-index.json";

/**
 * DocSearch is the second — and last — client island on the reading surface (Decision 9).
 *
 * # 🔴 The disclosed limit, stated where a reader meets it and not only in a design document
 *
 * The index ranks over **titles, headings and lead paragraphs**. It does NOT search full body text. A
 * reader who assumes otherwise concludes the product has no documentation about a thing it documents
 * thoroughly — so the limit is printed under the box, next to the results, in the same type as the
 * results. `scripts/gen-search-index.mjs` states the same limit in its own header.
 *
 * # Why there is no hosted search service
 *
 * The same two reasons as ADR-011: a third party on a trust surface (level 1, security), and an
 * air-gapped P19 deployment with no egress at all (level 2, stability), where a hosted search box is an
 * input that spins forever.
 *
 * # Degradation, and the one thing it is not allowed to be
 *
 * With JavaScript disabled this input does nothing — and `/docs` is a browsable section index either
 * way, which is the fallback. It is never a blank panel and never a spinner: a control that looks like
 * it is working and is not is worse than a control that is plainly absent.
 *
 * # Zero results say WHAT WAS SEARCHED (task 5.7)
 *
 * "No results" is an answer a reader cannot act on. "No page title, heading or summary contains …" tells
 * them the query was fine and the corpus is the limit — which is a different next step.
 */

type Entry = {
  route: string;
  title: string;
  section: string;
  summary: string;
  headings: string[];
};

const ENTRIES = index as { generated_from: string; ranks_over: string; entries: Entry[] };

/** score is deliberately simple and deliberately explainable: title beats heading beats summary. */
function score(entry: Entry, query: string): number {
  const q = query.toLowerCase();
  let total = 0;
  if (entry.title.toLowerCase().includes(q)) total += 10;
  if (entry.section.toLowerCase().includes(q)) total += 2;
  for (const heading of entry.headings) {
    if (heading.toLowerCase().includes(q)) total += 4;
  }
  if (entry.summary.toLowerCase().includes(q)) total += 3;
  return total;
}

export function DocSearch() {
  const [query, setQuery] = useState("");

  const results = useMemo(() => {
    const trimmed = query.trim();
    if (trimmed.length < 2) return null;
    return ENTRIES.entries
      .map((entry) => ({ entry, value: score(entry, trimmed) }))
      .filter((hit) => hit.value > 0)
      .sort((a, b) => b.value - a.value || a.entry.title.localeCompare(b.entry.title))
      .slice(0, 8);
  }, [query]);

  return (
    <div className="docsearch">
      <label className="stat__label" htmlFor="docsearch-input">
        Search the documentation
      </label>
      <input
        className="docsearch__input"
        id="docsearch-input"
        type="search"
        autoComplete="off"
        placeholder="eval, exit code, discovery…"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />

      {results === null ? null : results.length === 0 ? (
        <p className="docsearch__result-context">
          No page title, heading or summary contains &ldquo;{query.trim()}&rdquo;. This index ranks over{" "}
          {ENTRIES.ranks_over} across {ENTRIES.entries.length} page
          {ENTRIES.entries.length === 1 ? "" : "s"} — it does not search full body text, so a term that
          appears only inside a paragraph will not be found here.
        </p>
      ) : (
        <ul className="docsearch__results">
          {results.map((hit) => (
            <li key={hit.entry.route} className="docsearch__result">
              <Link className="docsearch__result-title prose-link" href={hit.entry.route}>
                {hit.entry.title}
              </Link>
              <p className="docsearch__result-context">{hit.entry.summary}</p>
            </li>
          ))}
        </ul>
      )}

      <p className="docsearch__result-context">
        Ranks over {ENTRIES.ranks_over}. Built into the page — nothing is sent anywhere when you type.
      </p>
    </div>
  );
}
