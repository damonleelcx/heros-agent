import type { AggregateRow } from "@/lib/types";
import { quantity } from "@/lib/format";
import { DataTable } from "./primitives";

/**
 * chart.tsx renders a cross-tenant aggregate as a bar chart WITH an accessible tabular equivalent.
 *
 * # Why the table is not optional
 *
 * A chart communicates through position and length, which a screen reader cannot convey and a
 * colour-vision-deficient reader may not distinguish. The tabular fallback is the same data, in the
 * same order, in a form every reader and every assistive technology can consume. It is rendered
 * always — collapsed, not absent — so it cannot be dropped by a later visual change without somebody
 * deleting it on purpose.
 *
 * # Why the bars carry no colour meaning, and why the value sits beside the bar
 *
 * Every bar uses one chart ink. Encoding a value's meaning in hue alone would fail the same readers
 * the fallback exists for. And the number is printed at the end of each row in tabular figures (FR30):
 * a bar answers "which is bigger" at a glance, but only the digits answer "by how much", and an
 * operator should not have to open a disclosure to get them.
 */
export function AggregateChart({
  caption,
  rows,
  unitLabel,
}: {
  caption: string;
  rows: AggregateRow[];
  unitLabel?: string;
}) {
  const max = rows.reduce((m, r) => (r.value > m ? r.value : m), 0);
  const unit = unitLabel ?? rows[0]?.unit;
  const uniformUnit = rows.every((r) => (unitLabel ?? r.unit) === unit);

  return (
    <figure>
      <figcaption className="hint">
        {caption}
        {uniformUnit && unit ? ` — all values in ${unit}` : ""}
      </figcaption>
      <div className="bar-chart" role="presentation">
        {rows.map((row) => (
          <div className="bar-chart__row" key={`${row.label}-${row.detail ?? ""}`}>
            <span className="bar-chart__label" title={row.label}>
              {row.label}
            </span>
            <span className="bar-chart__track">
              <span
                className="bar-chart__fill"
                style={{ width: max > 0 ? `${Math.max(2, (row.value / max) * 100)}%` : "2%" }}
              />
            </span>
            <span className="bar-chart__value">{quantity(row.value)}</span>
          </div>
        ))}
      </div>
      <details className="tabular-fallback" open={rows.length <= 3}>
        <summary>Table of the same data</summary>
        <DataTable
          caption={caption}
          columns={[
            { label: "Measure" },
            { label: "Value", numeric: true, unit: uniformUnit ? unit : undefined },
            ...(uniformUnit ? [] : [{ label: "Unit" }]),
            { label: "Detail" },
          ]}
        >
          {rows.map((row) => (
            <tr key={`row-${row.label}-${row.detail ?? ""}`}>
              <th scope="row">{row.label}</th>
              <td className="num">{quantity(row.value)}</td>
              {uniformUnit ? null : <td>{unitLabel ?? row.unit}</td>}
              <td>{row.detail ?? "—"}</td>
            </tr>
          ))}
        </DataTable>
      </details>
    </figure>
  );
}
