import type { ReactNode } from "react";
import { Prose } from "@/components/reading/prose";
import { Toc } from "@/components/reading/toc";
import type { Block, Heading } from "@/lib/reading/markdown";

/**
 * DocumentFrame is the one layout every readable document uses — legal and documentation alike.
 *
 * They share it because they are the same reading problem (ADR-011's opening argument): long-form text,
 * no session, read linearly for an hour, and printed. A second frame for legal would drift from this one
 * in exactly the details that matter — the measure, the identity block, and what the print stylesheet
 * hides.
 */
export function DocumentFrame({
  eyebrow,
  title,
  lede,
  identity,
  headings,
  blocks,
  aside,
  footer,
  printFooter,
}: {
  eyebrow: string;
  title: string;
  lede?: string;
  /** The identity strip rendered under the title, on screen. */
  identity?: ReactNode;
  headings: Heading[];
  blocks: Block[];
  /** Extra content above the table of contents (search, a version list). */
  aside?: ReactNode;
  footer?: ReactNode;
  /**
   * printFooter is the document identity as it appears in the PRINT running footer (task 3.4). It is a
   * plain string rather than a node so a test can assert its exact text, and so it cannot acquire a link
   * that means nothing on paper.
   */
  printFooter?: string;
}) {
  return (
    <div className="reading__frame">
      <article className="reading__doc">
        <p className="stat__label">{eyebrow}</p>
        <h1 className="page__title font-display font-light tracking-tight">{title}</h1>
        {lede ? <p className="hint mt-2 max-w-none text-sm">{lede}</p> : null}
        {identity ? <div className="reading__identity mt-4">{identity}</div> : null}
        <div className="mt-8">
          <Prose blocks={blocks} />
        </div>
        {footer ? <div className="mt-10">{footer}</div> : null}
      </article>

      <aside className="reading__aside">
        {aside}
        <Toc headings={headings} />
      </aside>

      {printFooter ? (
        <p className="print-footer font-mono text-xs text-muted-foreground" data-print-identity="true">
          {printFooter}
        </p>
      ) : null}
    </div>
  );
}
