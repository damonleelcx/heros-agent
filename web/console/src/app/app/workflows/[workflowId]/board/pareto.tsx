"use client";

import { useMemo, useState } from "react";
import type { ParetoPoint } from "@/lib/types.generated";
import { Figure, Tooltip } from "@/components/figure";
import { DataTable } from "@/components/primitives";
import { score, usd2, ms, plural } from "@/lib/format";

/**
 * The cost/quality frontier.
 *
 * # Why this is hand-drawn SVG and not a charting library
 *
 * The design bundle reaches for `recharts`. It would be one import and roughly 400KB of shipped
 * JavaScript for a scatter of at most a few dozen points — against a payload ceiling this console
 * states and enforces (R18/FR38). More decisively, a library's tooltip is a hover-only overlay: it is
 * unreachable by keyboard, clipped at a viewport edge, and invisible to a screen reader. This chart's
 * readout is focusable, announced, and rendered in the flow, which is a requirement (P4-27) rather
 * than a preference.
 *
 * # Frontier and dominated differ by SHAPE, not only by colour
 *
 * A diamond and a circle. Two hues would leave a colour-blind reader unable to tell which points are
 * on the frontier, which is the single thing the chart exists to show.
 */
const W = 720;
const H = 360;
const M = 52;

export function ParetoChart({ points }: { points: ParetoPoint[] }) {
  const [active, setActive] = useState<ParetoPoint | null>(null);

  const scale = useMemo(() => {
    const costs = points.map((p) => p.cost_usd);
    const qualities = points.map((p) => p.quality);
    const cLo = Math.min(...costs);
    const cHi = Math.max(...costs);
    const qLo = Math.min(...qualities);
    const qHi = Math.max(...qualities);
    const cPad = (cHi - cLo) * 0.12 || Math.abs(cHi) * 0.12 || 1;
    const qPad = (qHi - qLo) * 0.12 || Math.abs(qHi) * 0.12 || 1;
    const cMin = cLo - cPad;
    const cMax = cHi + cPad;
    const qMin = qLo - qPad;
    const qMax = qHi + qPad;
    const latencies = points.map((p) => p.latency_ms);
    const lMin = Math.min(...latencies);
    const lMax = Math.max(...latencies);
    return {
      cMin,
      cMax,
      qMin,
      qMax,
      x: (cost: number) => M + ((cost - cMin) / (cMax - cMin)) * (W - M * 2),
      y: (quality: number) => H - M - ((quality - qMin) / (qMax - qMin)) * (H - M * 2),
      r: (latency: number) => 5 + (lMax === lMin ? 0 : ((latency - lMin) / (lMax - lMin)) * 9),
    };
  }, [points]);

  const frontier = points.filter((p) => p.non_dominated).length;

  return (
    <>
      <Figure
        title="Quality against cost, with marker size showing latency"
        alt={
          `${points.length} ${plural(points.length, "variant", "variants")} plotted by quality against cost; ` +
          `${frontier} ${plural(frontier, "is", "are")} on the frontier. Marker size shows latency. ` +
          `The values are in the table below this graphic.`
        }
        table={<ParetoTable points={points} />}
      >
        <svg viewBox={`0 0 ${W} ${H}`} width="100%" className="block">
          <line x1={M} y1={H - M} x2={W - M} y2={H - M} className="axis" />
          <line x1={M} y1={M} x2={M} y2={H - M} className="axis" />
          <text x={W / 2} y={H - 12} textAnchor="middle" className="axis__label">
            Cost (USD) — lower is better
          </text>
          <text
            x={16}
            y={H / 2}
            transform={`rotate(-90 16 ${H / 2})`}
            textAnchor="middle"
            className="axis__label"
          >
            Quality — higher is better
          </text>
          <text x={M} y={H - M + 18} className="axis__tick">
            {usd2(scale.cMin)}
          </text>
          <text x={W - M} y={H - M + 18} textAnchor="end" className="axis__tick">
            {usd2(scale.cMax)}
          </text>
          <text x={M - 8} y={H - M} textAnchor="end" className="axis__tick">
            {score(scale.qMin)}
          </text>
          <text x={M - 8} y={M} textAnchor="end" className="axis__tick">
            {score(scale.qMax)}
          </text>
          {points.map((point) => {
            const cx = scale.x(point.cost_usd);
            const cy = scale.y(point.quality);
            const r = scale.r(point.latency_ms);
            const label =
              `${point.label}: quality ${score(point.quality)}, cost ${usd2(point.cost_usd)}, ` +
              `latency ${ms(point.latency_ms)}, ${point.non_dominated ? "on the frontier" : "dominated"}`;
            return (
              <g
                key={point.variant_id}
                tabIndex={0}
                role="img"
                aria-label={label}
                onFocus={() => setActive(point)}
                onBlur={() => setActive(null)}
                onMouseMove={() => setActive(point)}
                onMouseLeave={() => setActive(null)}
              >
                {point.non_dominated ? (
                  <rect
                    x={cx - r}
                    y={cy - r}
                    width={r * 2}
                    height={r * 2}
                    transform={`rotate(45 ${cx} ${cy})`}
                    className="mark mark--frontier"
                  />
                ) : (
                  <circle cx={cx} cy={cy} r={r} className="mark mark--dominated" />
                )}
              </g>
            );
          })}
        </svg>
      </Figure>

      {/* The readout lives beneath the chart, in flow, so it is never clipped by a viewport edge and
          is announced when it changes. */}
      <Tooltip visible={active !== null}>
        {active
          ? `${active.label} — quality ${score(active.quality)}, cost ${usd2(active.cost_usd)}, latency ${ms(active.latency_ms)}, ${active.non_dominated ? "on the frontier" : "dominated"}`
          : null}
      </Tooltip>

      <ul className="legend" aria-label="How to read this chart">
        <li className="caption flex items-center gap-2">
          <span className="legend__mark legend__mark--frontier" aria-hidden="true" /> diamond — on the
          frontier: nothing beats it on both quality and cost
        </li>
        <li className="caption flex items-center gap-2">
          <span className="legend__mark legend__mark--dominated" aria-hidden="true" /> circle
          — dominated: something is better on both
        </li>
        <li className="caption">marker size — latency</li>
      </ul>
    </>
  );
}

function ParetoTable({ points }: { points: ParetoPoint[] }) {
  return (
    <DataTable
      caption="The same points as values"
      columns={[
        { key: "variant", label: "Variant" },
        { key: "quality", label: "Quality", numeric: true },
        { key: "cost", label: "Cost (USD)", numeric: true },
        { key: "latency", label: "Latency", numeric: true },
        { key: "frontier", label: "Frontier" },
      ]}
    >
      <tbody>
        {points.map((point) => (
          <tr key={point.variant_id}>
            <td>{point.label}</td>
            <td className="num">{score(point.quality)}</td>
            <td className="num">{usd2(point.cost_usd)}</td>
            <td className="num">{ms(point.latency_ms)}</td>
            <td>{point.non_dominated ? "on the frontier" : "dominated"}</td>
          </tr>
        ))}
      </tbody>
    </DataTable>
  );
}
