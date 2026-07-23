import { Group } from "@visx/group";
import { type hierarchy, Treemap, treemapBinary } from "@visx/hierarchy";
import type { QualityFileTile, QualityRule } from "../../../api/quality";
import type { useTheme } from "../../../ThemeContext";
import { fonts } from "../../../theme";
import { deficitColor, metricLabel, TileDetail, type TreemapDatum } from "./shared";

export interface OverviewTabProps {
  display: {
    signal: number;
    geo_mean: number;
    metrics: { name: string; score: number; raw: number }[];
    tiles: QualityFileTile[];
    rules?: { failed: QualityRule[] };
    scanned_at: string;
    branch: string;
  } | null;
  treemapRoot: ReturnType<typeof hierarchy<TreemapDatum>> | null;
  size: { width: number; height: number };
  containerRef: (el: HTMLDivElement | null) => void;
  selectedTile: QualityFileTile | null;
  setSelectedTile: (t: QualityFileTile | null) => void;
  failedRules: QualityRule[];
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
  btnStyle: React.CSSProperties;
  onSimulateDelete: (path: string) => void;
}

export function OverviewTab({ display, treemapRoot, size, containerRef, selectedTile, setSelectedTile, failedRules, colors, fontSizes, btnStyle, onSimulateDelete }: OverviewTabProps) {
  return (
    <>
      {display && display.metrics.length > 0 && (
        <div style={{ display: "flex", gap: 8, padding: 12, flexWrap: "wrap" }}>
          {display.metrics.map((m) => (
            <div
              key={m.name}
              style={{
                flex: "1 1 100px",
                minWidth: 100,
                padding: "8px 10px",
                border: `1px solid ${colors.border}`,
                borderRadius: 6,
                background: colors.surface,
              }}
            >
              <div style={{ fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5 }}>{metricLabel(m.name)}</div>
              <div style={{ fontSize: 18, color: colors.textLight, fontFamily: fonts.mono, marginTop: 2 }}>{m.score.toFixed(3)}</div>
              <div style={{ fontSize: 10, color: colors.textDim, fontFamily: fonts.mono }}>raw {m.raw.toFixed(3)}</div>
            </div>
          ))}
        </div>
      )}

      {treemapRoot && (
        <>
          <div ref={containerRef} style={{ flex: "1 1 240px", minHeight: 240, padding: "0 12px" }}>
            <svg width={size.width} height={size.height}>
              <Treemap<TreemapDatum> root={treemapRoot} size={[size.width, size.height]} tile={treemapBinary} round paddingInner={2}>
                {(treemap) => (
                  <Group>
                    {treemap.descendants().map((node, i) => {
                      const w = node.x1 - node.x0;
                      const h = node.y1 - node.y0;
                      if (w <= 0 || h <= 0) return null;
                      const isLeaf = !node.children;
                      // Skip non-leaf rects — they cover the whole treemap with
                      // transparent fill and would intercept clicks on leaves below.
                      if (!isLeaf) return null;
                      const tile = node.data.tile;
                      const fill = deficitColor(node.data.deficit ?? 0);
                      const isSelected = !!(tile && selectedTile && selectedTile.path === tile.path);
                      return (
                        <Group key={`tm-${i}`} top={node.y0} left={node.x0}>
                          <rect
                            width={w}
                            height={h}
                            fill={fill}
                            stroke={isSelected ? colors.active : colors.border}
                            strokeWidth={isSelected ? 2 : 0.5}
                            style={{ cursor: tile ? "pointer" : "default" }}
                            onClick={() => tile && setSelectedTile(tile)}
                          >
                            {tile && <title>{`${tile.path}\nLOC ${tile.loc} · deficit ${tile.deficit.toFixed(2)}${tile.top_reason ? ` · ${tile.top_reason}` : ""}`}</title>}
                          </rect>
                          {w > 60 && h > 24 && (
                            <text x={6} y={16} fontSize={11} fontFamily={fonts.sans} fill={colors.white} style={{ pointerEvents: "none" }}>
                              {node.data.name}
                            </text>
                          )}
                          {tile && tile.top_reason && w > 60 && h > 38 && (
                            <text x={6} y={30} fontSize={9} fontFamily={fonts.mono} fill={colors.white} opacity={0.85} style={{ pointerEvents: "none" }}>
                              {metricLabel(tile.top_reason)}
                            </text>
                          )}
                        </Group>
                      );
                    })}
                  </Group>
                )}
              </Treemap>
            </svg>
          </div>

          {selectedTile && <TileDetail tile={selectedTile} onClose={() => setSelectedTile(null)} onSimulateDelete={onSimulateDelete} colors={colors} fontSizes={fontSizes} btnStyle={btnStyle} />}
        </>
      )}

      {failedRules.length > 0 && (
        <div style={{ padding: 12, borderTop: `1px solid ${colors.border}` }}>
          <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 8 }}>Failing rules ({failedRules.length})</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            {failedRules.map((rule) => (
              <div
                key={rule.name}
                style={{
                  padding: "6px 10px",
                  background: "rgba(239, 68, 68, 0.08)",
                  border: `1px solid ${colors.error}`,
                  borderRadius: 4,
                }}
              >
                <div style={{ fontSize: fontSizes.panels, color: colors.error, fontWeight: 600 }}>{rule.name}</div>
                <div style={{ fontSize: fontSizes.panels, color: colors.textLight, marginTop: 2 }}>{rule.message}</div>
                {rule.citations && rule.citations.length > 0 && (
                  <div style={{ fontSize: 11, color: colors.textDim, marginTop: 4, fontFamily: fonts.mono }}>
                    {rule.citations.slice(0, 5).map((c, i) => (
                      <div key={i}>
                        {c.path}
                        {c.note ? ` — ${c.note}` : ""}
                      </div>
                    ))}
                    {rule.citations.length > 5 && <div>… and {rule.citations.length - 5} more</div>}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </>
  );
}
