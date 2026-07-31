import Link from "next/link";
import type { ReactNode } from "react";
import { Tabs } from "@/components/tabs";
import type { Block, Inline } from "@/lib/reading/markdown";

/**
 * prose.tsx renders a parsed Markdown tree as REACT ELEMENTS (task 3.2).
 *
 * # Why a tree and not an HTML string
 *
 * `scripts/scan-markup.mjs` bans `dangerouslySetInnerHTML` across `src/`, and the reason it gives applies
 * here with more force than anywhere else in the console: every value on this surface is prose that will
 * one day be edited by somebody who is not an engineer, on the page where a customer reads what they are
 * agreeing to. React escapes by default; keeping the pipeline in element space means there is no string
 * concatenation for an escape to be forgotten in.
 *
 * # Why the multi-language sample uses the console's existing `<Tabs>`
 *
 * Task 3.5, and the reason is not tidiness: a second tab implementation is a second set of
 * keyboard-navigation, focus and `aria-controls` bugs, discovered separately, fixed separately, and
 * probably not at all on the surface nobody demos.
 *
 * # What this does NOT do
 *
 * No syntax highlighting. A highlighter is a dependency, a bundle, and a per-language grammar that goes
 * stale — for colour. The code is monospaced, scrollable in its own box, and copyable, which is what a
 * reader running a command actually needs. Stated here so its absence reads as a decision.
 */

function renderInline(nodes: Inline[], keyPrefix: string): ReactNode[] {
  return nodes.map((node, index) => {
    const key = `${keyPrefix}-${index}`;
    switch (node.kind) {
      case "text":
        return <span key={key}>{node.value}</span>;
      case "strong":
        return (
          <strong key={key} className="font-semibold text-foreground">
            {renderInline(node.children, key)}
          </strong>
        );
      case "em":
        return (
          <em key={key} className="italic">
            {renderInline(node.children, key)}
          </em>
        );
      case "code":
        return (
          <code key={key} className="prose-code">
            {node.value}
          </code>
        );
      case "link": {
        const external = /^https?:\/\//.test(node.href);
        if (external) {
          /*
           * An external link is MARKED, not merely styled (task 4.10's other half). A reader who is
           * about to leave a surface that contacts no third party should be told so before they click,
           * not after — and `rel="noreferrer"` keeps this page's URL out of the destination's logs,
           * which is the same promise the CSP makes on the rendering side.
           */
          return (
            <a
              key={key}
              className="prose-link"
              href={node.href}
              rel="noreferrer noopener"
              target="_blank"
              data-external="true"
            >
              {renderInline(node.children, key)}
              <span className="visually-hidden"> (opens an external site)</span>
              <span aria-hidden="true"> ↗</span>
            </a>
          );
        }
        return (
          <Link key={key} className="prose-link" href={node.href}>
            {renderInline(node.children, key)}
          </Link>
        );
      }
    }
  });
}

function Heading({ block, index }: { block: Extract<Block, { kind: "heading" }>; index: number }) {
  const children = (
    <>
      {renderInline(block.children, `h-${index}`)}
      {/*
       * A self-link on every heading, because anchors are a published contract (Decision 8): a CLI error
       * message deep-links into one, and a reader who wants to cite a section should not have to read the
       * source to find its id. It is a real link with a real accessible name, not a hover-only glyph.
       */}
      <Link className="prose-anchor" href={`#${block.slug}`}>
        <span className="visually-hidden">Link to this section: {block.text}</span>
        <span aria-hidden="true">#</span>
      </Link>
    </>
  );
  const className = "prose-heading";
  switch (block.level) {
    case 2:
      return (
        <h2 id={block.slug} className={`${className} prose-h2`}>
          {children}
        </h2>
      );
    case 3:
      return (
        <h3 id={block.slug} className={`${className} prose-h3`}>
          {children}
        </h3>
      );
    case 4:
      return (
        <h4 id={block.slug} className={`${className} prose-h4`}>
          {children}
        </h4>
      );
    case 5:
      return (
        <h5 id={block.slug} className={`${className} prose-h5`}>
          {children}
        </h5>
      );
    default:
      return (
        <h6 id={block.slug} className={`${className} prose-h6`}>
          {children}
        </h6>
      );
  }
}

function CodeBlock({ lang, value }: { lang: string; value: string }) {
  return (
    <div className="prose-code-frame">
      {lang ? <span className="prose-code-lang">{lang}</span> : null}
      <pre className="prose-pre">
        <code>{value}</code>
      </pre>
    </div>
  );
}

function renderBlock(block: Block, index: number): ReactNode {
  const key = `b-${index}`;
  switch (block.kind) {
    case "heading":
      return <Heading key={key} block={block} index={index} />;
    case "paragraph":
      return (
        <p key={key} className="prose-p">
          {renderInline(block.children, key)}
        </p>
      );
    case "code":
      return <CodeBlock key={key} lang={block.lang} value={block.value} />;
    case "tabs":
      return (
        <div key={key} className="prose-tabs">
          <Tabs
            tabs={block.samples.map((sample) => ({
              id: sample.label.toLowerCase().replace(/[^a-z0-9]+/g, "-"),
              label: sample.label,
              content: <CodeBlock lang={sample.lang} value={sample.value} />,
            }))}
          />
        </div>
      );
    case "list":
      return block.ordered ? (
        <ol key={key} className="prose-list prose-list--ordered">
          {block.items.map((item, i) => (
            <li key={`${key}-${i}`}>{renderInline(item, `${key}-${i}`)}</li>
          ))}
        </ol>
      ) : (
        <ul key={key} className="prose-list">
          {block.items.map((item, i) => (
            <li key={`${key}-${i}`}>{renderInline(item, `${key}-${i}`)}</li>
          ))}
        </ul>
      );
    case "quote":
      return (
        <blockquote key={key} className="prose-quote">
          {block.children.map((child, i) => renderBlock(child, i))}
        </blockquote>
      );
    case "table":
      /*
       * The table scrolls inside its OWN box. A wide table that widens the document is the defect that
       * makes a phone scroll sideways forever and a print stylesheet clip the last column — the same rule
       * the console's data tables already follow.
       */
      return (
        <div key={key} className="prose-table-frame">
          <table className="prose-table">
            <thead>
              <tr>
                {block.head.map((cell, i) => (
                  <th key={`${key}-h-${i}`} scope="col">
                    {renderInline(cell, `${key}-h-${i}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, r) => (
                <tr key={`${key}-r-${r}`}>
                  {row.map((cell, c) => (
                    <td key={`${key}-r-${r}-${c}`}>{renderInline(cell, `${key}-r-${r}-${c}`)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    case "rule":
      return <hr key={key} className="prose-rule" />;
  }
}

/** Prose renders a parsed document body. */
export function Prose({ blocks }: { blocks: Block[] }) {
  return <div className="prose">{blocks.map((block, index) => renderBlock(block, index))}</div>;
}
