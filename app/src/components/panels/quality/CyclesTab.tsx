import { fetchQualityCycles } from "../../../api/quality";
import { fonts } from "../../../theme";
import { type AsyncTabProps, EmptyState, ErrorState, LoadingState, useAsyncFetch } from "./shared";

export function CyclesTab({ channelId, scanGeneration, colors, fontSizes }: AsyncTabProps) {
  const { data, error, loading } = useAsyncFetch(() => fetchQualityCycles(channelId), [channelId, scanGeneration]);

  if (loading) return <LoadingState colors={colors} fontSizes={fontSizes} />;
  if (error) return <ErrorState message={error} colors={colors} fontSizes={fontSizes} />;
  if (!data || data.cycles.length === 0) {
    return <EmptyState text="No import cycles detected — graph is acyclic." colors={colors} fontSizes={fontSizes} />;
  }

  return (
    <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5 }}>
        {data.cycles.length} cycle{data.cycles.length === 1 ? "" : "s"} · largest {data.largest_cycle_size} files · {data.total_nodes_in_cycles} files in cycles
      </div>
      {data.cycles.map((cyc, idx) => (
        <div key={idx} style={{ border: `1px solid ${colors.border}`, borderRadius: 4, padding: "8px 10px", background: colors.surface }}>
          <div style={{ fontSize: 11, color: colors.textDim, marginBottom: 4 }}>
            Cycle #{idx + 1} ({cyc.length} files)
          </div>
          <div style={{ display: "flex", flexDirection: "column", gap: 2, fontFamily: fonts.mono, fontSize: 11, color: colors.textLight }}>
            {cyc.map((f, j) => (
              <div key={j} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                <span style={{ color: colors.textDim, width: 16, textAlign: "right" }}>{j === 0 ? "↻" : "↓"}</span>
                <span style={{ wordBreak: "break-all" }}>{f}</span>
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
