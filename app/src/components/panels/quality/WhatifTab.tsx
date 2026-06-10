import { useTheme } from "../../../ThemeContext";
import { fonts } from "../../../theme";
import type { QualityWhatifResponse } from "../../../api/quality";
import { bandColor, LoadingState, ErrorState, EmptyState, SectionHelp, metricLabel } from "./shared";

export interface WhatifTabProps {
  target: string | null;
  loading: boolean;
  error: string | null;
  result: QualityWhatifResponse | null;
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
}

export function WhatifTab({ target, loading, error, result, colors, fontSizes }: WhatifTabProps) {
  if (!target && !loading) {
    return (
      <EmptyState
        text="No simulation yet. Click a tile in Overview or Diagnostics, then 'Simulate delete'."
        colors={colors}
        fontSizes={fontSizes}
      />
    );
  }
  if (loading) return <LoadingState colors={colors} fontSizes={fontSizes} />;
  if (error) return <ErrorState message={error} colors={colors} fontSizes={fontSizes} />;
  if (!result) return null;

  const delta = result.delta_signal;
  const deltaColor = delta > 0 ? "#22c55e" : delta < 0 ? "#ef4444" : colors.textDim;
  const deltaLabel = delta > 0 ? `Δ +${delta}` : `Δ ${delta}`;

  return (
    <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 12 }}>
      <SectionHelp colors={colors}>
        Simulates a mutation against a clone of the in-memory graph and recomputes all five metrics on the shadow.
        The real graph isn&apos;t touched. Use it to A/B refactor candidates before committing — positive{" "}
        <b>delta</b> means the change improves the signal; the per-metric breakdown shows which dimensions move.
        Predictions are exact for this snapshot, not closed-form approximations.
      </SectionHelp>
      <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5 }}>
        Mutation
      </div>
      <div style={{ fontFamily: fonts.mono, fontSize: 12, color: colors.textLight, wordBreak: "break-all" }}>
        delete {target}
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 14, padding: "12px", border: `1px solid ${colors.border}`, borderRadius: 6, background: colors.surface }}>
        <div style={{ flex: 1, textAlign: "center" }}>
          <div style={{ fontSize: 11, color: colors.textDim }}>baseline</div>
          <div style={{ fontSize: 24, fontFamily: fonts.mono, color: bandColor(result.baseline_signal) }}>
            {result.baseline_signal}
          </div>
        </div>
        <div style={{ fontSize: 18, color: colors.textDim }}>→</div>
        <div style={{ flex: 1, textAlign: "center" }}>
          <div style={{ fontSize: 11, color: colors.textDim }}>predicted</div>
          <div style={{ fontSize: 24, fontFamily: fonts.mono, color: bandColor(result.predicted_signal) }}>
            {result.predicted_signal}
          </div>
        </div>
        <div style={{ flex: 1, textAlign: "center" }}>
          <div style={{ fontSize: 11, color: colors.textDim }}>delta</div>
          <div style={{ fontSize: 24, fontFamily: fonts.mono, color: deltaColor }}>{deltaLabel}</div>
        </div>
      </div>

      <div>
        <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 6 }}>
          Per-metric breakdown
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 3, fontFamily: fonts.mono, fontSize: 11 }}>
          {result.baseline_metrics.map((bm) => {
            const pm = result.predicted_metrics.find((p) => p.name === bm.name);
            const ps = pm?.score ?? bm.score;
            const ds = ps - bm.score;
            const dsColor = ds > 0.001 ? "#22c55e" : ds < -0.001 ? "#ef4444" : colors.textDim;
            return (
              <div key={bm.name} style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <div style={{ width: 90, color: colors.textDim }}>{metricLabel(bm.name)}</div>
                <div style={{ width: 60, color: colors.textLight, textAlign: "right" }}>{bm.score.toFixed(3)}</div>
                <div style={{ color: colors.textDim }}>→</div>
                <div style={{ width: 60, color: colors.textLight, textAlign: "right" }}>{ps.toFixed(3)}</div>
                <div style={{ width: 60, textAlign: "right", color: dsColor }}>
                  {ds > 0 ? `+${ds.toFixed(3)}` : ds.toFixed(3)}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
