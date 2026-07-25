"use client";

import { useId, useRef, useState, type ReactNode } from "react";
import { cx } from "@/lib/cx";

/**
 * Tabs is the viewport-first console's answer to a page that would otherwise stack its sections into a
 * tall scroll (NFR17, DESIGN-BRIEF "the console fits one screen"). One section is on screen at a time,
 * so each fits the viewport instead of pushing the next below the fold.
 *
 * It is a real tablist, not a set of styled buttons: `role="tablist"`, roving `tabindex`, arrow/Home/End
 * keys, and `aria-selected`/`aria-controls`/`aria-labelledby` wiring — the accessibility floor P9 (NFR7)
 * requires. The active panel is the ONE scroll owner (min-h-0 flex-1 overflow-y-auto); the tab strip
 * stays fixed. No section is removed by moving it into a tab — content moves, it does not disappear
 * (ui-redesign-feature-and-visual-consistency).
 */
export type TabItem = { id: string; label: string; content: ReactNode };

export function Tabs({
  tabs,
  initial,
  right,
}: {
  tabs: TabItem[];
  initial?: string;
  /** Optional controls rendered at the end of the tab strip (e.g. a workflow selector). */
  right?: ReactNode;
}) {
  const [active, setActive] = useState(initial ?? tabs[0]?.id ?? "");
  const base = useId();
  const refs = useRef<Record<string, HTMLButtonElement | null>>({});

  function onKeyDown(e: React.KeyboardEvent) {
    const i = tabs.findIndex((t) => t.id === active);
    if (i < 0) return;
    let next = i;
    if (e.key === "ArrowRight") next = (i + 1) % tabs.length;
    else if (e.key === "ArrowLeft") next = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = tabs.length - 1;
    else return;
    e.preventDefault();
    const id = tabs[next].id;
    setActive(id);
    refs.current[id]?.focus();
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div role="tablist" aria-label="Sections" onKeyDown={onKeyDown} className="flex shrink-0 gap-1 border-b border-border">
        {tabs.map((t) => {
          const selected = t.id === active;
          return (
            <button
              key={t.id}
              ref={(el) => {
                refs.current[t.id] = el;
              }}
              role="tab"
              id={`${base}-tab-${t.id}`}
              aria-selected={selected}
              aria-controls={`${base}-panel-${t.id}`}
              tabIndex={selected ? 0 : -1}
              type="button"
              onClick={() => setActive(t.id)}
              className={cx(
                "-mb-px cursor-pointer border-b-2 px-3 py-2 text-sm transition-colors",
                selected
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
            </button>
          );
        })}
        {right ? <div className="ml-auto flex items-center gap-2 pb-1">{right}</div> : null}
      </div>
      {tabs.map((t) => (
        <div
          key={t.id}
          role="tabpanel"
          id={`${base}-panel-${t.id}`}
          aria-labelledby={`${base}-tab-${t.id}`}
          hidden={t.id !== active}
          className={cx("min-h-0 flex-1 flex-col overflow-y-auto pt-4", t.id === active ? "flex" : "hidden")}
        >
          {t.content}
        </div>
      ))}
    </div>
  );
}
