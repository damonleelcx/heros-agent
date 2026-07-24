/**
 * Diff renders a unified patch with the colourisation `p2.html` already gets right (P2-15).
 *
 * # Why this is a server component that maps lines to elements
 *
 * The legacy page colourises by building an HTML string. This maps each line to an element, so React
 * escapes every one of them — which matters more here than anywhere else in the console: a diff is
 * *customer source code*, arbitrary by construction, and it is the single most likely place for a
 * `<script>` to appear as legitimate content. Rendering it as markup would execute it (R7, FR24).
 *
 * The five classes are the ones the legacy page uses: `+++`/`---` file headers muted, `@@` hunk
 * headers marked, additions and deletions distinguished by BOTH a colour and a leading character that
 * is already in the text — so the diff still reads correctly in greyscale and in a screen reader's
 * linear pass.
 */
export function Diff({ patch, filename }: { patch: string; filename?: string }) {
  const lines = patch.split("\n");
  const added = lines.filter((line) => line.startsWith("+") && !line.startsWith("+++")).length;
  const removed = lines.filter((line) => line.startsWith("-") && !line.startsWith("---")).length;
  return (
    <div className="overflow-hidden rounded-xl border border-border">
      {/*
        The header states the two counts up front. It is not decoration: "what is the size of this
        change?" is the first question a reviewer asks, and making them scroll a patch to count lines
        is asking them to compute something the data already knows.
      */}
      <div className="flex items-center justify-between gap-4 border-b border-border bg-muted/30 px-4 py-2.5">
        <span className="mono truncate text-xs text-muted-foreground">{filename ?? "unified diff"}</span>
        <span className="flex shrink-0 items-center gap-3 font-mono text-xs">
          <span className="text-bad">
            −{removed}
            <span className="visually-hidden"> lines removed</span>
          </span>
          <span className="text-ok">
            +{added}
            <span className="visually-hidden"> lines added</span>
          </span>
        </span>
      </div>
      <pre className="diff mono rounded-none border-0" tabIndex={0} aria-label="Unified diff">
        {lines.map((line, index) => (
          <span key={index} className={`diff__line diff__line--${classOf(line)}`}>
            {line}
            {"\n"}
          </span>
        ))}
      </pre>
    </div>
  );
}

function classOf(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) return "file";
  if (line.startsWith("@@")) return "hunk";
  if (line.startsWith("+")) return "add";
  if (line.startsWith("-")) return "del";
  return "ctx";
}
