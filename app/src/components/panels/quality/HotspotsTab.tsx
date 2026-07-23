import { useState } from "react";
import { fetchQualityClones, fetchQualityComplexity, type QualityCloneCluster, type QualityClonesResponse, type QualityComplexityFunction, type QualityComplexityResponse } from "../../../api/quality";
import { fonts } from "../../../theme";
import { type AsyncTabProps, deficitColor, ErrorState, type HotspotSectionProps, LoadingState, SectionHelp, useAsyncFetch } from "./shared";

const HOTSPOT_FUNCTIONS_LIMIT = 50;
const HOTSPOT_CLUSTERS_LIMIT = 25;

export function HotspotsTab({ channelId, scanGeneration, colors, fontSizes }: AsyncTabProps) {
  const complexity = useAsyncFetch<QualityComplexityResponse>(() => fetchQualityComplexity(channelId, { limit: HOTSPOT_FUNCTIONS_LIMIT }), [channelId, scanGeneration]);
  const clones = useAsyncFetch<QualityClonesResponse>(() => fetchQualityClones(channelId, { limit: HOTSPOT_CLUSTERS_LIMIT }), [channelId, scanGeneration]);

  const loading = complexity.loading || clones.loading;
  const error = complexity.error || clones.error;
  if (loading) return <LoadingState colors={colors} fontSizes={fontSizes} />;
  if (error) return <ErrorState message={error} colors={colors} fontSizes={fontSizes} />;

  return (
    <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 20 }}>
      <ComplexityHotspots data={complexity.data} colors={colors} fontSizes={fontSizes} />
      <CloneHotspots data={clones.data} colors={colors} fontSizes={fontSizes} />
    </div>
  );
}

function ComplexityHotspots({ data, colors, fontSizes }: HotspotSectionProps & { data: QualityComplexityResponse | null }) {
  if (!data) return null;
  return (
    <div>
      <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 6 }}>
        Complex functions ({data.over_threshold} of {data.total_functions} over threshold · score {data.score.toFixed(3)})
      </div>
      <SectionHelp colors={colors}>
        Per-function score is the worst of cyclomatic, cognitive, max nesting, parameter count, and LOC against their soft thresholds. The dimension score is <code>T/raw</code> above threshold —{" "}
        <b>0.50</b> at 2× T,
        <b>0.25</b> at 4× T, <b>0.10</b> at 10× T — so badly-saturated functions stay distinguishable instead of clamping to a wall of zeros. <b>1.0</b> means everything fits comfortably. The list is
        worst-first, capped at {HOTSPOT_FUNCTIONS_LIMIT}.
      </SectionHelp>
      {data.functions.length === 0 ? (
        <div style={{ color: colors.textDim, fontSize: fontSizes.panels }}>No functions over the complexity threshold.</div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {data.functions.map((fc, i) => (
            <ComplexityRow key={`${fc.path}:${fc.start_line}:${i}`} fn={fc} colors={colors} fontSizes={fontSizes} />
          ))}
        </div>
      )}
    </div>
  );
}

function ComplexityRow({ fn, colors }: HotspotSectionProps & { fn: QualityComplexityFunction }) {
  const deficit = 1 - fn.score;
  const accent = deficitColor(deficit);
  return (
    <div
      style={{
        border: `1px solid ${colors.border}`,
        borderLeft: `3px solid ${accent}`,
        borderRadius: 4,
        padding: "6px 10px",
        background: colors.surface,
        fontFamily: fonts.mono,
        fontSize: 11,
        color: colors.textLight,
      }}
    >
      <div style={{ display: "flex", justifyContent: "space-between", gap: 8, alignItems: "baseline" }}>
        <div style={{ wordBreak: "break-all", flex: 1 }}>
          <span>{fn.path}</span>
          <span style={{ color: colors.textDim }}>:{fn.start_line}</span>
          <span> {fn.name}</span>
        </div>
        <div style={{ color: accent, whiteSpace: "nowrap" }} title="Per-function score (1.0 healthy, 0 worst)">
          {fn.score.toFixed(2)}
        </div>
      </div>
      <div style={{ color: colors.textDim, fontSize: 10, marginTop: 2 }}>
        cyc {fn.cyclomatic} · cog {fn.cognitive} · nest {fn.max_nesting} · params {fn.param_count} · LOC {fn.loc}
      </div>
    </div>
  );
}

function CloneHotspots({ data, colors, fontSizes }: HotspotSectionProps & { data: QualityClonesResponse | null }) {
  if (!data) return null;
  const showing = Math.min(data.clusters.length, HOTSPOT_CLUSTERS_LIMIT);
  return (
    <div>
      <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 6 }}>
        Clone clusters ({data.cluster_count} total · {data.duplicated_loc.toLocaleString()} of {data.total_loc.toLocaleString()} LOC duplicated)
      </div>
      <SectionHelp colors={colors}>
        Functions whose AST shape is near-identical, grouped by SimHash + Hamming distance. <b>Max-distance 0</b> is an exact-shape duplicate; higher numbers are looser matches. Showing the {showing}{" "}
        largest clusters by total LOC; expand to see members.
      </SectionHelp>
      {data.clusters.length === 0 ? (
        <div style={{ color: colors.textDim, fontSize: fontSizes.panels }}>No clone clusters detected.</div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {data.clusters.map((cl, i) => (
            <CloneClusterRow key={i} cluster={cl} index={i} colors={colors} fontSizes={fontSizes} />
          ))}
        </div>
      )}
    </div>
  );
}

function CloneClusterRow({ cluster, index, colors, fontSizes }: HotspotSectionProps & { cluster: QualityCloneCluster; index: number }) {
  const [open, setOpen] = useState(index === 0);
  return (
    <div style={{ border: `1px solid ${colors.border}`, borderRadius: 4, background: colors.surface }}>
      <button
        onClick={() => setOpen((o) => !o)}
        style={{
          width: "100%",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "baseline",
          padding: "6px 10px",
          background: "transparent",
          border: "none",
          color: colors.textLight,
          cursor: "pointer",
          fontFamily: fonts.sans,
          fontSize: fontSizes.panels,
          textAlign: "left",
        }}
      >
        <span>
          <span style={{ color: colors.textDim, marginRight: 6 }}>{open ? "▾" : "▸"}</span>
          Cluster #{index + 1} — {cluster.members.length} members
        </span>
        <span style={{ color: colors.textDim, fontSize: 11 }}>
          {cluster.loc.toLocaleString()} LOC · max-distance {cluster.max_distance}
        </span>
      </button>
      {open && (
        <div style={{ padding: "0 10px 8px 10px", display: "flex", flexDirection: "column", gap: 2, fontFamily: fonts.mono, fontSize: 11, color: colors.textLight }}>
          {cluster.members.map((m, j) => (
            <div key={j} style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
              <span style={{ wordBreak: "break-all", flex: 1 }}>
                {m.path}
                <span style={{ color: colors.textDim }}>:{m.start_line}</span> {m.name}
              </span>
              <span style={{ color: colors.textDim, whiteSpace: "nowrap" }}>LOC {m.loc}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
