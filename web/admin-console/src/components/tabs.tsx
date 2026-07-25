"use client";

import { useId, useRef, useState, type ReactNode } from "react";

/**
 * Tabs splits a stacked operator page into in-page tabs so it fits one screen (P8 viewport-first). One
 * section is visible at a time; the active panel is the single scroll owner, the chrome/alarm never
 * move. It is a real ARIA tablist — `role="tablist"`, roving tabindex, arrow/Home/End keys — so it
 * meets the console's accessibility floor. No section is removed by moving it into a tab.
 */
export type TabItem = { id: string; label: string; content: ReactNode };

export function Tabs({ tabs, initial }: { tabs: TabItem[]; initial?: string }) {
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
    <div className="tabs">
      <div role="tablist" aria-label="Sections" className="tabs__list" onKeyDown={onKeyDown}>
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
              className="tabs__tab"
              onClick={() => setActive(t.id)}
            >
              {t.label}
            </button>
          );
        })}
      </div>
      {tabs.map((t) => (
        <div
          key={t.id}
          role="tabpanel"
          id={`${base}-panel-${t.id}`}
          aria-labelledby={`${base}-tab-${t.id}`}
          hidden={t.id !== active}
          className="tabs__panel"
        >
          {t.content}
        </div>
      ))}
    </div>
  );
}
