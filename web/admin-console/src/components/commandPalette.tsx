"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import type { Surface } from "@/lib/surfaces";

/**
 * commandPalette.tsx is the console's velocity surface (FR32).
 *
 * # What it is for
 *
 * An operator one page into an incident should not have to remember which page holds which control,
 * nor type an opaque tenant id from memory to look at it. One keystroke from any view, the palette
 * addresses every capability the role grants and every subject the operator can reach, by NAME.
 *
 * # The two rules that make it safe
 *
 * 1. **It offers only what the gate would allow.** Its entries come from `surfaces.ts` filtered by the
 *    same permission map the backend enforces, so a denied capability is ABSENT rather than
 *    offered-and-refused (FR22).
 *
 * 2. **It navigates. It never performs.** Every entry is a destination. Selecting a destructive one
 *    opens that action's confirmation with its reason field and typed-target field EMPTY and its
 *    friction intact — the palette cannot arm a kill switch, cancel a job or erase a subject (FR37).
 *    That is the whole read/write split in one component: finding is fast, doing is deliberate.
 *
 * # Finding a subject is not the same act as confirming a destruction
 *
 * Type-ahead exists so an operator never has to recall an identifier in order to LOOK at something.
 * The typed-target confirmation on an irreversible action is not a lookup cost to be optimised away —
 * it is the control itself, and nothing here touches it.
 */

export type PaletteSubject = {
  id: string;
  label: string;
  hint: string;
  href: string;
  kind: string;
};

const RECENT_KEY = "heros_admin_recent";
const RECENT_MAX = 6;

/** recordRecent remembers a visited subject so the palette can offer it first next time. */
export function recordRecent(subject: PaletteSubject) {
  if (typeof window === "undefined") return;
  try {
    const raw = window.localStorage.getItem(RECENT_KEY);
    const list: PaletteSubject[] = raw ? JSON.parse(raw) : [];
    const next = [subject, ...list.filter((s) => s.href !== subject.href)].slice(0, RECENT_MAX);
    window.localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    // A palette that cannot remember is a palette that is merely less convenient. It is never a
    // reason to fail the page the operator actually asked for.
  }
}

function readRecent(): PaletteSubject[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.localStorage.getItem(RECENT_KEY);
    return raw ? (JSON.parse(raw) as PaletteSubject[]) : [];
  } catch {
    return [];
  }
}

type Entry = {
  key: string;
  label: string;
  hint: string;
  href: string;
  group: string;
  danger?: boolean;
  subject?: PaletteSubject;
};

export function CommandPalette({
  commands,
  subjects,
}: {
  commands: Surface[];
  subjects: PaletteSubject[];
}) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState(0);
  const [recent, setRecent] = useState<PaletteSubject[]>([]);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  // The palette opens on ⌘K / Ctrl-K from ANY view, and closes on Escape. The listener is on the
  // document because "from any view" means exactly that — not "from any view where the operator has
  // already clicked something".
  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setOpen((v) => !v);
        return;
      }
      if (event.key === "Escape") setOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setCursor(0);
    setRecent(readRecent());
    // Focus lands in the input, so the next keystroke is already the search.
    inputRef.current?.focus();
  }, [open]);

  const entries = useMemo<Entry[]>(() => {
    const q = query.trim().toLowerCase();
    const match = (text: string) => text.toLowerCase().includes(q);

    const recentEntries: Entry[] = (q ? [] : recent).map((s) => ({
      key: `recent:${s.href}`,
      label: s.label,
      hint: s.hint,
      href: s.href,
      group: "Recent",
      subject: s,
    }));

    const subjectEntries: Entry[] = subjects
      .filter((s) => !q || match(s.label) || match(s.hint) || match(s.id))
      .slice(0, 12)
      .map((s) => ({
        key: `subject:${s.href}`,
        label: s.label,
        hint: s.hint,
        href: s.href,
        group: "Subjects",
        subject: s,
      }));

    const commandEntries: Entry[] = commands
      .filter((c) => !q || match(c.label) || match(c.hint))
      .map((c) => ({
        key: `cmd:${c.href}:${c.label}`,
        label: c.label,
        hint: c.hint,
        href: c.href,
        group: c.danger ? "Dangerous actions" : "Go to",
        danger: c.danger,
      }));

    return [...recentEntries, ...subjectEntries, ...commandEntries];
  }, [query, commands, subjects, recent]);

  const choose = useCallback(
    (entry: Entry) => {
      if (entry.subject) recordRecent(entry.subject);
      setOpen(false);
      // A navigation, always. There is no branch here that performs an action, and adding one would
      // be the defect FR37 names.
      router.push(entry.href);
    },
    [router],
  );

  const onInputKey = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setCursor((c) => Math.min(c + 1, Math.max(entries.length - 1, 0)));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setCursor((c) => Math.max(c - 1, 0));
    } else if (event.key === "Enter") {
      event.preventDefault();
      const entry = entries[cursor];
      if (entry) choose(entry);
    }
  };

  useEffect(() => {
    if (!open) return;
    const selected = listRef.current?.querySelector('[aria-selected="true"]');
    selected?.scrollIntoView({ block: "nearest" });
  }, [cursor, open]);

  if (!open) {
    return (
      <button
        type="button"
        className="palette-trigger"
        onClick={() => setOpen(true)}
        aria-keyshortcuts="Meta+K Control+K"
      >
        Search <kbd>⌘K</kbd>
      </button>
    );
  }

  let lastGroup = "";
  return (
    <>
      <button type="button" className="palette-trigger" onClick={() => setOpen(false)}>
        Search <kbd>⌘K</kbd>
      </button>
      <div
        className="palette__scrim"
        role="presentation"
        onClick={(event) => {
          if (event.target === event.currentTarget) setOpen(false);
        }}
      >
        <div className="palette" role="dialog" aria-modal="true" aria-label="Command palette">
          <input
            ref={inputRef}
            className="palette__input"
            type="text"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setCursor(0);
            }}
            onKeyDown={onInputKey}
            placeholder="Go to a surface, or find a tenant by name…"
            aria-label="Search surfaces and subjects"
            aria-controls="palette-list"
            autoComplete="off"
          />
          <ul className="palette__list" id="palette-list" role="listbox" ref={listRef}>
            {entries.length === 0 ? (
              <li className="palette__empty">
                Nothing matches “{query}”. The palette only offers what your role grants.
              </li>
            ) : (
              entries.map((entry, index) => {
                const header = entry.group !== lastGroup ? entry.group : null;
                lastGroup = entry.group;
                return (
                  <li key={entry.key}>
                    {header ? <p className="palette__group">{header}</p> : null}
                    <a
                      className="palette__item"
                      href={entry.href}
                      role="option"
                      aria-selected={index === cursor}
                      onMouseEnter={() => setCursor(index)}
                      onClick={(event) => {
                        event.preventDefault();
                        choose(entry);
                      }}
                    >
                      <span className="palette__item-main">
                        {entry.label}
                        <span className="palette__item-sub">{entry.hint}</span>
                      </span>
                      {entry.danger ? <span className="palette__danger">Opens confirmation</span> : null}
                    </a>
                  </li>
                );
              })
            )}
          </ul>
          <p className="palette__footer">
            <span>↑↓ to move · ↵ to open · Esc to close</span>
            <span>The palette opens a control. It never performs one.</span>
          </p>
        </div>
      </div>
    </>
  );
}
