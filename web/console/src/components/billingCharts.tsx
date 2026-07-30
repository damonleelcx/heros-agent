import type { MeterView, PeriodPoint } from "@/lib/types.generated";
import { Figure } from "@/components/figure";
import { DataTable } from "@/components/primitives";
import { usd2, score, integer } from "@/lib/format";

/**
 * billingCharts.tsx is the SUM and usage graphics on the billing page (P21 task 6.4).
 *
 * # Two charts, two different jobs — which is why they are not one chart
 *
 *   SUM over periods   change over time, one series, few points  →  columns
 *   usage vs allowance magnitude against a THRESHOLD             →  horizontal bars + a limit line
 *
 * Putting them together would need two y-scales, which is the one chart mistake with no defensible
 * case: a dual axis lets the author choose where the lines cross. They are separate figures with one
 * scale each.
 *
 * # Why the allowance is a line and not a second bar
 *
 * It is a threshold, not a quantity. Drawn as a bar it invites a reader to compare two lengths as if
 * both were measurements; drawn as a dashed rule it reads as what it is — the point past which the
 * plan says something different.
 *
 * # No price, anywhere
 *
 * Every figure here is a QUANTITY read back from the billing API: a SUM in the unit the server names,
 * a metered count, an allowance. There is no currency literal and no arithmetic — the payment-UI fence
 * would fail the build on one, and the reason it would is that a number the console computed is a
 * number nobody can justify against an invoice.
 *
 * # Hand-drawn SVG, again
 *
 * Same argument as the Pareto scatter: a charting library is hundreds of kilobytes against a stated
 * payload ceiling, and its tooltip is a hover-only overlay that a keyboard cannot reach. These are
 * server components with no client JavaScript at all — every value is in the `<title>` a browser shows
 * natively, and in the table `Figure` requires.
 */

// The viewBox is WIDE and SHALLOW on purpose. The SVG scales to the container's width, so the ratio
// decides the rendered height: a 720×240 box became a 425px-tall chart for three columns, which pushed
// the invoice — the thing a customer opened this page for — below the fold. Comparing three columns
// needs width, not height.
const TREND_W = 960;
const TREND_H = 220;
const TREND_PAD = 44;
/** The 2px surface gap the mark spec asks for between adjacent bars. */
const BAR_GAP = 2;
/**
 * LABEL_BAND is headroom reserved ABOVE the tallest column for its direct label.
 *
 * Without it the label is drawn six units above a bar that already reaches the top of the plot, which
 * renders as a number jammed against the frame — present in the DOM, unreadable on screen. Reserving
 * the band is the difference between "the label exists" and "the label can be read", and only the
 * second is the requirement.
 */
const LABEL_BAND = 20;
/**
 * BAR_MAX_W keeps a column a MARK rather than a block.
 *
 * With three periods across the plot each slot is nearly 300 units wide, and a bar wider than it is
 * tall stops reading as a measured length — it reads as a filled area, which is the shape people
 * compare by area instead of by height. Thin marks, centred in their slot.
 */
const BAR_MAX_W = 72;

/**
 * SUMTrend plots spend under management per period.
 *
 * One series, so there is no legend: the title names it. Only the newest and the largest column are
 * labelled directly — a number on every bar is noise that hides the shape the chart exists to show.
 */
export function SUMTrend({ points, unit }: { points: PeriodPoint[] | null; unit: string }) {
  const data = points ?? [];
  if (data.length === 0) {
    return (
      <p className="hint">
        No period history yet. This is a real state — a first period has nothing to compare against —
        not a chart that failed to load.
      </p>
    );
  }

  const max = Math.max(...data.map((p) => p.sum), 0);
  const plotW = TREND_W - TREND_PAD * 2;
  const plotH = TREND_H - TREND_PAD * 2;
  const slot = plotW / data.length;
  const barW = Math.max(Math.min(slot - BAR_GAP * 2, BAR_MAX_W), 1);
  // A zero-max period set would divide by zero and draw full-height bars for nothing.
  const scale = (v: number) => (max <= 0 ? 0 : (v / max) * (plotH - LABEL_BAND));
  const newest = data[data.length - 1];
  const largest = data.reduce((a, b) => (b.sum > a.sum ? b : a), data[0]);

  return (
    <Figure
      title={`Spend under management by period, in ${unit}`}
      alt={
        `A column chart of spend under management across ${data.length} period` +
        `${data.length === 1 ? "" : "s"}, from ${data[0].period} to ${newest.period}. ` +
        `The highest is ${largest.period} at ${usd2(largest.sum)} ${unit}; the latest is ` +
        `${newest.period} at ${usd2(newest.sum)} ${unit}. The values are in the table below this graphic.`
      }
      table={<TrendTable points={data} unit={unit} />}
    >
      <svg viewBox={`0 0 ${TREND_W} ${TREND_H}`} width="100%" className="block">
        {/* The baseline. The data-ends are anchored to it, so a column's length is its value. */}
        <line
          x1={TREND_PAD}
          y1={TREND_H - TREND_PAD}
          x2={TREND_W - TREND_PAD}
          y2={TREND_H - TREND_PAD}
          className="axis"
        />
        {data.map((p, i) => {
          const h = scale(p.sum);
          // Centred in its slot, so the spacing between marks is even rather than left-packed.
          const x = TREND_PAD + i * slot + (slot - barW) / 2;
          const y = TREND_H - TREND_PAD - h;
          const labelled = p.period === newest.period || p.period === largest.period;
          return (
            <g key={p.period}>
              {/* rx gives the 4px rounded data-end; the baseline end stays square because the bar is
                  anchored there. A fully rounded bar reads as floating. */}
              <rect x={x} y={y} width={barW} height={h} rx={4} className="bar">
                <title>{`${p.period}: ${usd2(p.sum)} ${unit}`}</title>
              </rect>
              <text
                x={x + barW / 2}
                y={TREND_H - TREND_PAD + 16}
                textAnchor="middle"
                className="axis__tick"
              >
                {p.period}
              </text>
              {labelled ? (
                <text x={x + barW / 2} y={Math.max(y - 6, 12)} textAnchor="middle" className="bar__value">
                  {usd2(p.sum)}
                </text>
              ) : null}
            </g>
          );
        })}
        <text x={TREND_PAD} y={16} className="axis__label">
          {unit}
        </text>
      </svg>
    </Figure>
  );
}

function TrendTable({ points, unit }: { points: PeriodPoint[]; unit: string }) {
  return (
    <DataTable
      caption={`Spend under management per period, in ${unit}`}
      columns={[
        { key: "period", label: "Period" },
        { key: "sum", label: `Spend (${unit})`, numeric: true },
      ]}
    >
      <tbody>
        {points.map((p) => (
          <tr key={p.period}>
            <td className="mono text-sm">{p.period}</td>
            <td className="num mono">{usd2(p.sum)}</td>
          </tr>
        ))}
      </tbody>
    </DataTable>
  );
}

const USAGE_W = 960;
const ROW_H = 30;
const LABEL_W = 210;
// The right gutter holds the value AND the word "over". Sized for the longest of those rather than for
// the number alone — the first render clipped "432.000 over" to "432.000 ove", which turns the
// colour-independent over-allowance signal back into colour alone.
const VALUE_GUTTER = 150;

/**
 * UsageAgainstAllowance draws each metered quantity against the plan's allowance.
 *
 * 🔴 An UNSET allowance is drawn as unlimited — a full-width track with no limit line — never as zero.
 * "Unlimited" and "none" have the same numeric shape and opposite meanings, and rendering the first as
 * the second tells a customer they are over a limit that does not exist.
 */
export function UsageAgainstAllowance({ meters }: { meters: MeterView[] | null }) {
  const rows = meters ?? [];
  if (rows.length === 0) {
    return (
      <p className="hint">
        Nothing metered in this period yet. This is a real state, not a failure to load.
      </p>
    );
  }

  const height = rows.length * ROW_H + 24;
  const trackW = USAGE_W - LABEL_W - VALUE_GUTTER;
  const over = rows.filter((m) => m.over).length;

  return (
    <Figure
      title="Metered usage against the plan's allowance"
      alt={
        `${rows.length} metered quantit${rows.length === 1 ? "y" : "ies"} shown against this plan's ` +
        `allowances. ${over === 0 ? "None is over its allowance." : `${over} ${over === 1 ? "is" : "are"} over allowance.`} ` +
        `A dashed rule marks each allowance; a meter with no allowance is unlimited. The values are in ` +
        `the table below this graphic.`
      }
      table={<UsageTable meters={rows} />}
    >
      <svg viewBox={`0 0 ${USAGE_W} ${height}`} width="100%" className="block">
        {rows.map((m, i) => {
          const y = 12 + i * ROW_H;
          // The scale's denominator is the larger of the allowance and the observed value, so an
          // over-allowance bar is visibly past its rule rather than clipped at it.
          const denom = m.unlimited ? Math.max(m.value, 1) : Math.max(m.allowed, m.value, 1);
          const w = Math.max((m.value / denom) * trackW, m.value > 0 ? 3 : 0);
          const limitX = LABEL_W + (m.unlimited ? trackW : (m.allowed / denom) * trackW);
          return (
            <g key={m.metric}>
              <text x={0} y={y + 15} className="axis__label">
                {m.label}
              </text>
              <rect x={LABEL_W} y={y} width={trackW} height={12} rx={6} className="bar__track" />
              <rect x={LABEL_W} y={y} width={w} height={12} rx={4} className={m.over ? "bar bar--over" : "bar"}>
                <title>
                  {`${m.label}: ${score(m.value)} ${m.unit}` +
                    (m.unlimited ? " (no allowance — unlimited)" : ` of ${integer(m.allowed)} allowed`) +
                    (m.over ? " — over allowance" : "")}
                </title>
              </rect>
              {m.unlimited ? null : (
                <line x1={limitX} y1={y - 3} x2={limitX} y2={y + 15} className="bar__limit" />
              )}
              <text x={USAGE_W - VALUE_GUTTER + 12} y={y + 11} className="bar__value">
                {score(m.value)}
                {/* 🔴 The over state is a WORD as well as a hue. */}
                {m.over ? " over" : ""}
              </text>
            </g>
          );
        })}
      </svg>
    </Figure>
  );
}

function UsageTable({ meters }: { meters: MeterView[] }) {
  return (
    <DataTable
      caption="Every metered quantity for this period, against the plan's allowance"
      columns={[
        { key: "metric", label: "Metric" },
        { key: "value", label: "Used", numeric: true },
        { key: "allowed", label: "Allowance", numeric: true },
        { key: "state", label: "State" },
      ]}
    >
      <tbody>
        {meters.map((m) => (
          <tr key={m.metric}>
            <td>
              <span className="text-sm">{m.label}</span>
              <p className="caption mono mt-1">
                {m.metric} · {m.unit}
              </p>
            </td>
            <td className="num mono">{score(m.value)}</td>
            <td className="num mono">{m.unlimited ? "unlimited" : integer(m.allowed)}</td>
            <td className="caption">{m.over ? "over allowance" : "within allowance"}</td>
          </tr>
        ))}
      </tbody>
    </DataTable>
  );
}
