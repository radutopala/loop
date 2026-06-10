import { fetchQualityEvolution, type QualityEvolutionResponse } from "../../../api/quality";
import { fonts } from "../../../theme";
import {
  useAsyncFetch,
  LoadingState,
  ErrorState,
  EmptyState,
  SectionHelp,
  formatScannedAt,
  type AsyncTabProps,
} from "./shared";

export function EvolutionTab({ channelId, scanGeneration, colors, fontSizes }: AsyncTabProps) {
  const { data, error, loading } = useAsyncFetch<QualityEvolutionResponse | null>(
    () => fetchQualityEvolution(channelId),
    [channelId, scanGeneration],
  );

  if (loading) return <LoadingState colors={colors} fontSizes={fontSizes} />;
  if (error) return <ErrorState message={error} colors={colors} fontSizes={fontSizes} />;
  if (!data) {
    return <EmptyState text="No git history available — evolution analysis requires a git repository." colors={colors} fontSizes={fontSizes} />;
  }

  const card: React.CSSProperties = {
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    padding: "6px 10px",
    background: colors.surface,
    fontFamily: fonts.mono,
    fontSize: 11,
    color: colors.textLight,
  };

  return (
    <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 16 }}>
      <div style={{ fontSize: 11, color: colors.textDim }}>
        {data.commits_scanned} commit{data.commits_scanned === 1 ? "" : "s"} scanned
        {data.shallow_warning ? " · shallow clone — try git fetch --unshallow" : ""}
      </div>

      <div>
        <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 6 }}>
          Coupling pairs ({data.coupling_pairs.length})
        </div>
        <SectionHelp colors={colors}>
          Files that change together in git history. <b>Jaccard</b> = co-changes ÷ (commits touching either) — 1.0 means
          they always move as one. Pairs ≥ 0.5 surface here. High Jaccard means two physical files behave as one
          conceptual unit; <b>cross-module</b> pairs flag a leaking architectural boundary. Action: consolidate, or
          define a stable interface so changes don&apos;t propagate.
        </SectionHelp>
        {data.coupling_pairs.length === 0 ? (
          <div style={{ color: colors.textDim, fontSize: fontSizes.panels }}>No strong coupling detected.</div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {data.coupling_pairs.map((p, i) => (
              <div key={i} style={card}>
                <div style={{ wordBreak: "break-all" }}>{p.file_a}</div>
                <div style={{ wordBreak: "break-all" }}>{p.file_b}</div>
                <div style={{ color: colors.textDim, marginTop: 2 }}>
                  jaccard {p.jaccard.toFixed(3)} · {p.co_change_count} co-changes
                  {p.cross_module ? " · cross-module" : ""}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <div>
        <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 6 }}>
          Churn hotspots ({data.churn_hotspots.length})
        </div>
        <SectionHelp colors={colors}>
          Files with the most commits in the window. High churn alone isn&apos;t a defect — but a churn hotspot that
          also shows up red on the treemap (large structural deficit) is a bug factory: every change risks defects, and
          the structure makes each change costlier than it should be. Cross-reference with the Diagnostics tab to
          prioritise refactor budget.
        </SectionHelp>
        {data.churn_hotspots.length === 0 ? (
          <div style={{ color: colors.textDim, fontSize: fontSizes.panels }}>No churn hotspots.</div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {data.churn_hotspots.map((h, i) => (
              <div key={i} style={card}>
                <div style={{ wordBreak: "break-all" }}>{h.file}</div>
                <div style={{ color: colors.textDim, marginTop: 2 }}>
                  {h.change_count} changes · last {formatScannedAt(h.last_changed_at)}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <div>
        <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 6 }}>
          Bus factor risk ({data.bus_factor.length})
        </div>
        <SectionHelp colors={colors}>
          Files where one author owns ≥ 80% of commits. Surfaces institutional-knowledge concentration: if that person
          leaves, the file stalls, and lack of a second perspective means review pressure has been thin.{" "}
          <b>Days since other author</b> is measured from the most recent commit in the window (deterministic across
          re-runs). Action: pair-program the next change, or assign as an onboarding target.
        </SectionHelp>
        {data.bus_factor.length === 0 ? (
          <div style={{ color: colors.textDim, fontSize: fontSizes.panels }}>No bus-factor risk detected.</div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {data.bus_factor.map((b, i) => (
              <div key={i} style={card}>
                <div style={{ wordBreak: "break-all" }}>{b.file}</div>
                <div style={{ color: colors.textDim, marginTop: 2 }}>
                  {b.sole_author} · {(b.sole_author_ratio * 100).toFixed(0)}% of {b.total_commits} commits
                  {b.days_since_last_other_author > 0 ? ` · ${b.days_since_last_other_author}d since other author` : ""}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
