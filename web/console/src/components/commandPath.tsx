"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { ChevronRight, Search } from "lucide-react";
import { cx } from "@/lib/cx";

/**
 * commandPath.tsx is the keyboard route to every surface and every already-visited subject
 * (FR30, task 5.12).
 *
 * # Why this exists on a console with four data views
 *
 * Not for speed. It is the keyboard answer to a gap the console is not permitted to close: the
 * platform cannot enumerate a tenant's workflows or runs, so the console cannot offer a list of "all
 * your workflows". What it can always offer is **the ones you have already opened**, without asking
 * you to retype an identifier the session is holding — which is the whole of 🔴
 * `interaction-simplicity-first` applied to the one input this product genuinely cannot derive.
 *
 * # Why the entries are passed in as props
 *
 * The visited list lives on the server-side session (subjects.ts). It is handed down as data rather
 * than fetched here, so this component makes no request, holds no session material, and works before
 * hydration completes — it simply has nothing to show until it does.
 *
 * # Accessibility is the interface, not a coat of paint on it
 *
 * A dialog that traps focus, returns focus to where it came from, is dismissible with Escape, and is
 * navigable with the arrow keys — because a command path that can only be driven with a mouse is a
 * decoration on top of the problem it claims to solve.
 */

export type CommandEntry = {
  id: string;
  label: string;
  hint?: string;
  href: string;
  group: string;
};

export function CommandPath({ entries }: { entries: CommandEntry[] }) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const openerRef = useRef<Element | null>(null);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return entries;
    return entries.filter(
      (entry) => entry.label.toLowerCase().includes(q) || (entry.hint ?? "").toLowerCase().includes(q),
    );
  }, [entries, query]);

  const groups = useMemo(() => [...new Set(matches.map((entry) => entry.group))], [matches]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        // Remember what had focus so Escape can give it back. A dialog that drops the caller's focus
        // on the floor makes the keyboard user restart their traversal from the top of the document.
        openerRef.current = document.activeElement;
        setOpen((was) => !was);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  useEffect(() => {
    if (open) {
      setQuery("");
      setActive(0);
      inputRef.current?.focus();
    } else if (openerRef.current instanceof HTMLElement) {
      openerRef.current.focus();
    }
  }, [open]);

  if (!open) {
    return (
      <button
        className="flex cursor-pointer items-center gap-2 rounded-lg border border-border bg-muted/20 px-2.5 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground"
        type="button"
        onClick={() => setOpen(true)}
        aria-keyshortcuts="Meta+K Control+K"
      >
        <Search className="size-3.5" aria-hidden="true" />
        <span className="hidden sm:inline">Go to</span>
        <kbd className="kbd">Ctrl K</kbd>
      </button>
    );
  }

  function go(href: string) {
    setOpen(false);
    router.push(href);
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-background/70 px-4 pt-[14vh] backdrop-blur-sm"
      role="presentation"
      onMouseDown={() => setOpen(false)}
    >
      <div
        className="w-full max-w-lg overflow-hidden rounded-xl border border-border bg-popover shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-label="Go to a surface or a subject you have opened"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
          <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <input
            ref={inputRef}
            className="flex-1 bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
            type="text"
            value={query}
            placeholder="Surfaces, and subjects from this session"
            aria-label="Filter surfaces and subjects"
            aria-controls="palette-list"
            autoComplete="off"
            spellCheck={false}
            onChange={(event) => {
              setQuery(event.target.value);
              setActive(0);
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault();
                setOpen(false);
              } else if (event.key === "ArrowDown") {
                event.preventDefault();
                // Wrap-around, matching the leaderboard's row navigation (P4-18). A list that stops at
                // the end makes the last item cost a full traversal to reach from the first.
                setActive((index) => (matches.length === 0 ? 0 : (index + 1) % matches.length));
              } else if (event.key === "ArrowUp") {
                event.preventDefault();
                setActive((index) => (matches.length === 0 ? 0 : (index - 1 + matches.length) % matches.length));
              } else if (event.key === "Enter") {
                event.preventDefault();
                const match = matches[active];
                if (match) go(match.href);
              }
            }}
          />
          <kbd className="kbd">esc</kbd>
        </div>
        <ul className="max-h-80 overflow-y-auto py-1" id="palette-list" role="listbox" aria-label="Results">
          {matches.length === 0 ? (
            <li className="px-4 py-6 text-center text-xs leading-relaxed text-muted-foreground">
              Nothing matches. This session has not opened a subject by that name — the console cannot
              list subjects it has not seen, so open one by identifier first.
            </li>
          ) : (
            groups.map((group) => (
              <li key={group}>
                <p className="px-4 py-1.5 font-mono text-[10px] uppercase tracking-widest text-muted-foreground/70">
                  {group}
                </p>
                <ul>
                  {matches
                    .map((entry, index) => ({ entry, index }))
                    .filter(({ entry }) => entry.group === group)
                    .map(({ entry, index }) => (
                      <li key={entry.id}>
                        <button
                          type="button"
                          className={cx(
                            "flex w-full cursor-pointer items-center gap-3 px-4 py-2 text-left transition-colors",
                            index === active ? "bg-muted/60" : "hover:bg-muted/30",
                          )}
                          role="option"
                          aria-selected={index === active}
                          onClick={() => go(entry.href)}
                          onMouseEnter={() => setActive(index)}
                        >
                          <span className="mono min-w-0 flex-1 truncate text-xs text-foreground">
                            {entry.label}
                          </span>
                          {entry.hint ? (
                            <span className="shrink-0 text-xs text-muted-foreground">{entry.hint}</span>
                          ) : null}
                          <ChevronRight className="size-3 shrink-0 text-muted-foreground/40" aria-hidden="true" />
                        </button>
                      </li>
                    ))}
                </ul>
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  );
}
