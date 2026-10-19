import { useEffect, useState } from "react";
import { getReviewStats, type ReviewStats, type ReviewStatsGroup } from "../../api/review";
import type { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

// The windows the stats can cover, in days.
export const REVIEW_STATS_WINDOWS = [7, 30, 90, 365];

export function formatStatsRate(rate: number): string {
  return `${Math.round(rate * 100)}%`;
}

// formatStatsDuration renders a run duration as e.g. "3m12s"; 0 (no
// completed runs) as "-".
export function formatStatsDuration(ms: number): string {
  if (!ms) return "-";
  const total = Math.round(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h) return `${h}h${m}m${s}s`;
  if (m) return `${m}m${s}s`;
  return `${s}s`;
}

// The summary cards: a label, the headline number and what it is made of.
export function reviewStatsCards(st: ReviewStats): { label: string; value: string; detail: string }[] {
  const r = st.runs;
  const f = st.findings;
  return [
    {
      label: "Runs",
      value: String(r.total),
      detail: `${r.completed} completed, ${r.failed} failed, ${r.cancelled} cancelled · ${r.prs} PR${r.prs === 1 ? "" : "s"}`,
    },
    { label: "Scope", value: `${r.incremental}/${r.total}`, detail: `incremental · ${r.full} full` },
    { label: "Duration", value: formatStatsDuration(r.median_ms), detail: `median · p90 ${formatStatsDuration(r.p90_ms)}` },
    { label: "Findings", value: String(f.added), detail: `${f.folded} folded, ${f.deleted} deleted, ${f.pushed} pushed` },
    {
      label: "False positives",
      value: formatStatsRate(f.false_positive_rate),
      detail: `${f.real} real, ${f.false_positive} false, ${f.already_fixed} already fixed, ${f.unjudged} unjudged`,
    },
    { label: "Blocking", value: String(f.blocking), detail: `of ${f.real} real · ${f.nice_to_have} nice to have` },
    { label: "Pushed", value: String(f.pushed), detail: `${f.resolved} resolved, ${f.outdated} outdated` },
  ];
}

// The breakdown tables, skipping empty ones. Categories have no run count:
// a run reports findings of many categories.
export function reviewStatsTables(st: ReviewStats): { title: string; runs: boolean; rows: ReviewStatsGroup[] }[] {
  return [
    { title: "Model", runs: true, rows: st.by_model ?? [] },
    { title: "Effort", runs: true, rows: st.by_effort ?? [] },
    { title: "Scope", runs: true, rows: st.by_scope ?? [] },
    { title: "Category", runs: false, rows: st.by_category ?? [] },
  ].filter((t) => t.rows.length > 0);
}

export function ReviewStatsView({ channelId, prNumber, colors }: { channelId: string; prNumber?: number; colors: ReturnType<typeof useTheme>["colors"] }) {
  const [forPR, setForPR] = useState(prNumber !== undefined);
  const [days, setDays] = useState(90);
  const [stats, setStats] = useState<ReviewStats | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setError(null);
    getReviewStats(channelId, { days, pr: forPR ? prNumber : undefined })
      .then((st) => {
        if (!cancelled) setStats(st);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [channelId, prNumber, forPR, days]);

  const selectStyle: React.CSSProperties = {
    background: "transparent",
    color: colors.text,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    padding: "3px 4px",
    fontSize: 11,
    fontFamily: fonts.sans,
  };
  const cell: React.CSSProperties = { padding: "3px 8px", textAlign: "right", fontVariantNumeric: "tabular-nums" };

  return (
    <div data-testid="review-stats" style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12, fontFamily: fonts.sans, fontSize: 11, color: colors.text }}>
      <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
        <select data-testid="review-stats-target" value={forPR ? "pr" : "repo"} onChange={(e) => setForPR(e.target.value === "pr")} aria-label="What the stats cover" style={selectStyle}>
          {prNumber !== undefined && <option value="pr">PR #{prNumber}</option>}
          <option value="repo">Whole repo</option>
        </select>
        <select data-testid="review-stats-days" value={days} onChange={(e) => setDays(Number(e.target.value))} aria-label="How many days back the stats cover" style={selectStyle}>
          {REVIEW_STATS_WINDOWS.map((d) => (
            <option key={d} value={d}>
              Last {d} days
            </option>
          ))}
        </select>
      </div>
      {error && (
        <div data-testid="review-stats-error" style={{ color: colors.dangerText }}>
          {error}
        </div>
      )}
      {!error && stats && stats.runs.total === 0 && <div style={{ color: colors.textDim }}>No review runs in this window.</div>}
      {!error && stats && stats.runs.total > 0 && (
        <>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(150px, 1fr))", gap: 8 }}>
            {reviewStatsCards(stats).map((c) => (
              <div key={c.label} style={{ border: `1px solid ${colors.border}`, borderRadius: 6, padding: "6px 8px" }}>
                <div style={{ color: colors.textDim, fontSize: 10 }}>{c.label}</div>
                <div style={{ fontSize: 16, fontVariantNumeric: "tabular-nums" }}>{c.value}</div>
                <div style={{ color: colors.textDim, fontSize: 10 }}>{c.detail}</div>
              </div>
            ))}
          </div>
          {reviewStatsTables(stats).map((t) => (
            <table key={t.title} style={{ borderCollapse: "collapse", width: "100%" }}>
              <thead>
                <tr style={{ color: colors.textDim, borderBottom: `1px solid ${colors.border}` }}>
                  <th style={{ ...cell, textAlign: "left" }}>{t.title}</th>
                  <th style={cell}>Runs</th>
                  <th style={cell}>Added</th>
                  <th style={cell}>Real</th>
                  <th style={cell}>Blocking</th>
                  <th style={cell}>Nice to have</th>
                  <th style={cell}>False pos</th>
                  <th style={cell}>FP rate</th>
                  <th style={cell}>Pushed</th>
                  <th style={cell}>Resolved</th>
                </tr>
              </thead>
              <tbody>
                {t.rows.map((row) => (
                  <tr key={row.key}>
                    <td style={{ ...cell, textAlign: "left", fontFamily: fonts.mono }}>{row.key || "(unknown)"}</td>
                    <td style={cell}>{t.runs ? row.runs : "-"}</td>
                    <td style={cell}>{row.added}</td>
                    <td style={cell}>{row.real}</td>
                    <td style={cell}>{row.blocking}</td>
                    <td style={cell}>{row.nice_to_have}</td>
                    <td style={cell}>{row.false_positive}</td>
                    <td style={cell}>{formatStatsRate(row.false_positive_rate)}</td>
                    <td style={cell}>{row.pushed}</td>
                    <td style={cell}>{row.resolved}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}
        </>
      )}
    </div>
  );
}
