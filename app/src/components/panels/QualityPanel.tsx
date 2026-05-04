import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Group } from "@visx/group";
import { Treemap, hierarchy, treemapBinary } from "@visx/hierarchy";
import { useTheme } from "../../ThemeContext";
import { useEventStream } from "../../hooks/useEventStream";
import { fonts } from "../../theme";
import {
  fetchQualityC4,
  fetchQualityCycles,
  fetchQualityEvolution,
  fetchQualitySnapshot,
  NoPreviousSignal,
  simulateQualityWhatif,
  triggerQualityScan,
  type QualityC4Response,
  type QualityEvolutionResponse,
  type QualityFileTile,
  type QualityMetric,
  type QualityRule,
  type QualityScanReport,
  type QualitySnapshot,
  type QualityWhatifResponse,
} from "../../api/quality";

interface QualityPanelProps {
  channelId: string;
  dirPath: string;
  branch: string;
  embedded?: boolean;
  onClose: () => void;
}

interface ScanProgress {
  done: number;
  total: number;
}

interface TreemapDatum {
  name: string;
  size?: number;
  deficit?: number;
  tile?: QualityFileTile;
  children?: TreemapDatum[];
}

type TabId = "overview" | "diagnostics" | "cycles" | "evolution" | "whatif" | "c4";

const TABS: { id: TabId; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "diagnostics", label: "Diagnostics" },
  { id: "cycles", label: "Cycles" },
  { id: "evolution", label: "Evolution" },
  { id: "whatif", label: "What-if" },
  { id: "c4", label: "C4" },
];

function shortPath(path: string): string {
  const parts = path.split("/");
  if (parts.length <= 2) return path;
  return parts.slice(-2).join("/");
}

function bandColor(signal: number): string {
  if (signal < 5000) return "#ef4444";
  if (signal < 7000) return "#f59e0b";
  return "#22c55e";
}

function deficitColor(deficit: number): string {
  // 0 = healthy green, 1 = full red drag.
  const clamped = Math.max(0, Math.min(1, deficit));
  const r = Math.round(34 + (239 - 34) * clamped);
  const g = Math.round(197 - (197 - 68) * clamped);
  const b = Math.round(94 - (94 - 68) * clamped);
  return `rgb(${r}, ${g}, ${b})`;
}

function metricLabel(name: string): string {
  const map: Record<string, string> = {
    modularity: "Modularity",
    cycles: "Cycles",
    depth: "Depth",
    equality: "Equality",
    redundancy: "Redundancy",
  };
  return map[name] ?? name;
}

function formatScannedAt(ts: string): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleString();
}

function buildTreemapData(tiles: QualityFileTile[]): TreemapDatum {
  if (tiles.length === 0) {
    return { name: "root", children: [{ name: "no data", size: 1, deficit: 0 }] };
  }
  return {
    name: "root",
    children: tiles.map((t) => ({
      name: shortPath(t.path),
      // Use LOC as size; clamp to ≥1 so zero-LOC files still render a tile.
      size: Math.max(1, t.loc),
      deficit: t.deficit,
      tile: t,
    })),
  };
}

export function QualityPanel({ channelId, embedded, onClose }: QualityPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [snapshot, setSnapshot] = useState<QualitySnapshot | null>(null);
  const [scanReport, setScanReport] = useState<QualityScanReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [scanning, setScanning] = useState(false);
  const [progress, setProgress] = useState<ScanProgress | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [size, setSize] = useState({ width: 600, height: 320 });
  const [selectedTile, setSelectedTile] = useState<QualityFileTile | null>(null);
  const [activeTab, setActiveTab] = useState<TabId>("overview");
  // Bumped on every quality.scanned to invalidate per-tab caches.
  const [scanGeneration, setScanGeneration] = useState(0);
  const [whatifResult, setWhatifResult] = useState<QualityWhatifResponse | null>(null);
  const [whatifTarget, setWhatifTarget] = useState<string | null>(null);
  const [whatifLoading, setWhatifLoading] = useState(false);
  const [whatifError, setWhatifError] = useState<string | null>(null);

  const loadSnapshot = useCallback(async () => {
    try {
      const snap = await fetchQualitySnapshot(channelId);
      setSnapshot(snap);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [channelId]);

  useEffect(() => {
    loadSnapshot();
  }, [loadSnapshot]);

  const handleScan = useCallback(async () => {
    setError(null);
    setScanning(true);
    setProgress(null);
    try {
      const resp = await triggerQualityScan(channelId);
      if (resp.status === "in_progress") {
        // Another scan owns the in-flight slot; UI stays in scanning state
        // until quality.scanned arrives.
      }
    } catch (e) {
      setScanning(false);
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [channelId]);

  const onEvent = useCallback(
    (event: { type: string; data: unknown }) => {
      switch (event.type) {
        case "quality.session_started":
          setScanning(true);
          setProgress(null);
          break;
        case "quality.scan_progress":
          if (event.data && typeof event.data === "object") {
            const d = event.data as { done?: number; total?: number };
            if (typeof d.done === "number" && typeof d.total === "number") {
              setProgress({ done: d.done, total: d.total });
            }
          }
          break;
        case "quality.scanned":
          setScanReport(event.data as QualityScanReport);
          setScanning(false);
          setProgress(null);
          // A new scan invalidates every tab's cached data.
          setScanGeneration((g) => g + 1);
          // Refresh the persisted snapshot so subsequent reloads match.
          loadSnapshot();
          break;
        case "quality.scan_cancelled":
        case "quality.session_ended":
          setScanning(false);
          setProgress(null);
          break;
      }
    },
    [loadSnapshot],
  );

  useEventStream({ channelId, onEvent });

  // Resize observer for the treemap container.
  const containerRef = useCallback((el: HTMLDivElement | null) => {
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const first = entries[0];
      if (!first) return;
      const { width, height } = first.contentRect;
      if (width > 0 && height > 0) setSize({ width, height });
    });
    ro.observe(el);
  }, []);

  const display: { signal: number; previous_signal: number; geo_mean: number; metrics: QualityMetric[]; tiles: QualityFileTile[]; rules?: { failed: QualityRule[] }; scanned_at: string; branch: string } | null =
    scanReport
      ? {
          signal: scanReport.signal,
          previous_signal: scanReport.previous_signal,
          geo_mean: scanReport.geo_mean,
          metrics: scanReport.metrics,
          tiles: scanReport.tiles ?? [],
          rules: { failed: scanReport.rules?.failed ?? [] },
          scanned_at: scanReport.scanned_at,
          branch: scanReport.branch,
        }
      : snapshot
        ? {
            signal: snapshot.signal,
            previous_signal: snapshot.previous_signal,
            geo_mean: snapshot.geo_mean,
            metrics: snapshot.metrics,
            tiles: snapshot.tiles ?? [],
            scanned_at: snapshot.scanned_at,
            branch: snapshot.branch,
          }
        : null;

  const treemapData = useMemo(
    () => (display ? buildTreemapData(display.tiles) : null),
    [display],
  );

  const treemapRoot = useMemo(() => {
    if (!treemapData) return null;
    return hierarchy<TreemapDatum>(treemapData)
      .sum((d) => d.size ?? 0)
      .sort((a, b) => (b.value ?? 0) - (a.value ?? 0));
  }, [treemapData]);

  const headerStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "space-between",
    padding: "8px 12px",
    borderBottom: `1px solid ${colors.border}`,
    fontFamily: fonts.sans,
    fontSize: fontSizes.panels,
    color: colors.textLight,
    flexShrink: 0,
  };

  const btnStyle: React.CSSProperties = {
    padding: "4px 10px",
    fontSize: fontSizes.panels,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    background: "transparent",
    color: colors.textLight,
    cursor: "pointer",
    fontFamily: fonts.sans,
  };

  const containerStyle: React.CSSProperties = {
    flex: 1,
    display: "flex",
    flexDirection: "column",
    minHeight: 0,
    backgroundColor: colors.sidebar,
    color: colors.text,
    fontFamily: fonts.sans,
  };

  const runWhatifDelete = useCallback(
    async (path: string) => {
      setWhatifTarget(path);
      setWhatifLoading(true);
      setWhatifError(null);
      setWhatifResult(null);
      setActiveTab("whatif");
      try {
        const res = await simulateQualityWhatif(channelId, [{ op: "delete", path }]);
        setWhatifResult(res);
      } catch (e) {
        setWhatifError(e instanceof Error ? e.message : String(e));
      } finally {
        setWhatifLoading(false);
      }
    },
    [channelId],
  );

  if (loading) {
    return (
      <div style={containerStyle}>
        <div style={headerStyle}>
          <span>Quality</span>
          {!embedded && <button style={btnStyle} onClick={onClose}>Close</button>}
        </div>
        <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: fontSizes.panels }}>
          Loading…
        </div>
      </div>
    );
  }

  // Empty state: no snapshot ever scanned for this channel.
  if (!display && !scanning) {
    return (
      <div style={containerStyle}>
        <div style={headerStyle}>
          <span>Quality</span>
          {!embedded && <button style={btnStyle} onClick={onClose}>Close</button>}
        </div>
        <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 12, padding: 20, textAlign: "center" }}>
          <button
            onClick={handleScan}
            style={{
              padding: "10px 24px",
              fontSize: 14,
              border: `1px solid ${colors.active}`,
              borderRadius: 6,
              background: colors.active,
              color: colors.white,
              cursor: "pointer",
              fontFamily: fonts.sans,
            }}
          >
            Scan now
          </button>
          <div style={{ color: colors.textDim, fontSize: fontSizes.panels, maxWidth: 360, lineHeight: 1.5 }}>
            Quality scans your codebase for structural issues — cycles, dead code, modularity drift.
          </div>
          {error && <div style={{ color: colors.error, fontSize: fontSizes.panels }}>{error}</div>}
        </div>
      </div>
    );
  }

  const sig = display?.signal ?? 0;
  const sigColor = bandColor(sig);
  const prev = display?.previous_signal ?? NoPreviousSignal;
  const hasPrev = prev !== NoPreviousSignal;
  const delta = hasPrev ? sig - prev : 0;
  const deltaColor = delta > 0 ? "#22c55e" : delta < 0 ? "#ef4444" : colors.textDim;
  const deltaLabel = delta > 0 ? `+${delta}` : `${delta}`;
  const failedRules = display && "rules" in display && display.rules ? display.rules.failed : [];
  const showProgress = scanning && progress !== null;
  const headerLabel = scanning
    ? showProgress
      ? `Scanning… ${progress!.done}/${progress!.total} files`
      : "Scanning…"
    : null;

  return (
    <div style={containerStyle}>
      <div style={headerStyle}>
        <span>Quality</span>
        <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <button
            style={{ ...btnStyle, opacity: scanning ? 0.5 : 1, cursor: scanning ? "not-allowed" : "pointer" }}
            onClick={handleScan}
            disabled={scanning}
          >
            {scanning ? "Scanning…" : "Scan now"}
          </button>
          {!embedded && <button style={btnStyle} onClick={onClose}>Close</button>}
        </div>
      </div>

      {snapshot?.branch_mismatch && (
        <div style={{ padding: "6px 12px", background: "rgba(245, 158, 11, 0.15)", color: "#f59e0b", fontSize: fontSizes.panels, borderBottom: `1px solid ${colors.border}` }}>
          Snapshot taken on “{snapshot.branch}”, current branch “{snapshot.current_branch}” — Scan now to refresh
        </div>
      )}

      {error && (
        <div style={{ padding: "6px 12px", background: "rgba(239, 68, 68, 0.15)", color: colors.error, fontSize: fontSizes.panels, borderBottom: `1px solid ${colors.border}` }}>
          {error}
        </div>
      )}

      {/* Headline (always visible, regardless of tab) */}
      <div style={{ padding: "16px 12px", borderBottom: `1px solid ${colors.border}` }}>
        {headerLabel ? (
          <div>
            <div style={{ fontSize: 16, color: colors.textLight }}>{headerLabel}</div>
            {showProgress && (
              <div style={{ marginTop: 8, height: 4, background: colors.border, borderRadius: 2, overflow: "hidden" }}>
                <div style={{ width: `${(progress!.done / Math.max(1, progress!.total)) * 100}%`, height: "100%", background: colors.active, transition: "width 0.2s" }} />
              </div>
            )}
          </div>
        ) : hasPrev ? (
          // Δ-since-last-scan as the primary headline. Absolute signal
          // and previous value drop below in muted text — useful for
          // context but not the thing the eye lands on. The signal
          // itself stays band-colored so red/amber/green is preserved.
          <div style={{ display: "flex", alignItems: "baseline", gap: 14, flexWrap: "wrap" }}>
            <span
              style={{ fontSize: 36, fontWeight: 600, color: deltaColor, fontFamily: fonts.mono }}
              title="Change in signal since the last scan of this branch"
            >
              {deltaLabel}
            </span>
            <span style={{ color: colors.textDim, fontSize: 12 }}>
              <span style={{ color: sigColor, fontFamily: fonts.mono }}>{sig}</span>
              {" "}from{" "}
              <span style={{ fontFamily: fonts.mono }}>{prev}</span>
              {" · "}geo-mean {display?.geo_mean.toFixed(3)} · branch “{display?.branch}”
              {display?.scanned_at ? ` · ${formatScannedAt(display.scanned_at)}` : ""}
            </span>
          </div>
        ) : (
          // First scan ever for this (channel, branch): no delta to
          // render — fall back to the absolute headline.
          <div style={{ display: "flex", alignItems: "baseline", gap: 12, flexWrap: "wrap" }}>
            <span style={{ fontSize: 36, fontWeight: 600, color: sigColor, fontFamily: fonts.mono }}>{sig}</span>
            <span style={{ color: colors.textDim, fontSize: 12 }}>
              first scan · geo-mean {display?.geo_mean.toFixed(3)} · branch “{display?.branch}”
              {display?.scanned_at ? ` · ${formatScannedAt(display.scanned_at)}` : ""}
            </span>
          </div>
        )}
      </div>

      {/* Tab strip */}
      <div style={{ display: "flex", borderBottom: `1px solid ${colors.border}`, flexShrink: 0, overflow: "auto" }}>
        {TABS.map((t) => {
          const isActive = activeTab === t.id;
          return (
            <button
              key={t.id}
              onClick={() => setActiveTab(t.id)}
              style={{
                padding: "8px 14px",
                fontSize: fontSizes.panels,
                fontFamily: fonts.sans,
                background: "transparent",
                color: isActive ? colors.textLight : colors.textDim,
                border: "none",
                borderBottom: `2px solid ${isActive ? colors.active : "transparent"}`,
                cursor: "pointer",
                whiteSpace: "nowrap",
              }}
            >
              {t.label}
            </button>
          );
        })}
      </div>

      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "auto", opacity: scanning ? 0.5 : 1, transition: "opacity 0.2s" }}>
        {activeTab === "overview" && (
          <OverviewTab
            display={display}
            treemapRoot={treemapRoot}
            size={size}
            containerRef={containerRef}
            selectedTile={selectedTile}
            setSelectedTile={setSelectedTile}
            failedRules={failedRules}
            colors={colors}
            fontSizes={fontSizes}
            btnStyle={btnStyle}
            onSimulateDelete={runWhatifDelete}
          />
        )}

        {activeTab === "diagnostics" && (
          <DiagnosticsTab
            tiles={display?.tiles ?? []}
            colors={colors}
            fontSizes={fontSizes}
            btnStyle={btnStyle}
            onSimulateDelete={runWhatifDelete}
          />
        )}

        {activeTab === "cycles" && (
          <CyclesTab channelId={channelId} scanGeneration={scanGeneration} colors={colors} fontSizes={fontSizes} />
        )}

        {activeTab === "evolution" && (
          <EvolutionTab channelId={channelId} scanGeneration={scanGeneration} colors={colors} fontSizes={fontSizes} />
        )}

        {activeTab === "whatif" && (
          <WhatifTab
            target={whatifTarget}
            loading={whatifLoading}
            error={whatifError}
            result={whatifResult}
            colors={colors}
            fontSizes={fontSizes}
          />
        )}

        {activeTab === "c4" && (
          <C4Tab channelId={channelId} scanGeneration={scanGeneration} colors={colors} fontSizes={fontSizes} />
        )}
      </div>
    </div>
  );
}

// --- Overview tab ---------------------------------------------------

interface OverviewTabProps {
  display: { signal: number; geo_mean: number; metrics: QualityMetric[]; tiles: QualityFileTile[]; rules?: { failed: QualityRule[] }; scanned_at: string; branch: string } | null;
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

function OverviewTab({ display, treemapRoot, size, containerRef, selectedTile, setSelectedTile, failedRules, colors, fontSizes, btnStyle, onSimulateDelete }: OverviewTabProps) {
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
              <div style={{ fontSize: 18, color: colors.textLight, fontFamily: fonts.mono, marginTop: 2 }}>
                {m.score.toFixed(3)}
              </div>
              <div style={{ fontSize: 10, color: colors.textDim, fontFamily: fonts.mono }}>raw {m.raw.toFixed(3)}</div>
            </div>
          ))}
        </div>
      )}

      {treemapRoot && (
        <>
          <div ref={containerRef} style={{ flex: "1 1 240px", minHeight: 240, padding: "0 12px" }}>
            <svg width={size.width} height={size.height}>
              <Treemap<TreemapDatum>
                root={treemapRoot}
                size={[size.width, size.height]}
                tile={treemapBinary}
                round
                paddingInner={2}
              >
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
                            {tile && (
                              <title>{`${tile.path}\nLOC ${tile.loc} · deficit ${tile.deficit.toFixed(2)}${tile.top_reason ? ` · ${tile.top_reason}` : ""}`}</title>
                            )}
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

          {selectedTile && (
            <TileDetail
              tile={selectedTile}
              onClose={() => setSelectedTile(null)}
              onSimulateDelete={onSimulateDelete}
              colors={colors}
              fontSizes={fontSizes}
              btnStyle={btnStyle}
            />
          )}
        </>
      )}

      {failedRules.length > 0 && (
        <div style={{ padding: 12, borderTop: `1px solid ${colors.border}` }}>
          <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 8 }}>
            Failing rules ({failedRules.length})
          </div>
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

// --- TileDetail (shared by Overview + Diagnostics) ----------------

interface TileDetailProps {
  tile: QualityFileTile;
  onClose: () => void;
  onSimulateDelete: (path: string) => void;
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
  btnStyle: React.CSSProperties;
}

function TileDetail({ tile, onClose, onSimulateDelete, colors, fontSizes, btnStyle }: TileDetailProps) {
  return (
    <div style={{ margin: "0 12px 12px 12px", padding: 12, border: `1px solid ${colors.border}`, borderRadius: 6, background: colors.surface }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", gap: 8 }}>
        <div>
          <div style={{ fontFamily: fonts.mono, fontSize: fontSizes.panels, color: colors.textLight, wordBreak: "break-all" }}>
            {tile.path}
          </div>
          <div style={{ marginTop: 4, fontSize: 11, color: colors.textDim }}>
            LOC {tile.loc} · deficit {tile.deficit.toFixed(2)}
            {tile.top_reason && ` · top reason ${metricLabel(tile.top_reason)}`}
          </div>
        </div>
        <div style={{ display: "flex", gap: 6, flexShrink: 0 }}>
          <button style={btnStyle} onClick={() => onSimulateDelete(tile.path)}>
            Simulate delete
          </button>
          <button style={btnStyle} onClick={onClose}>Close</button>
        </div>
      </div>
      <div style={{ marginTop: 10, display: "flex", flexDirection: "column", gap: 4 }}>
        {Object.entries(tile.metric_deficits ?? {})
          .sort((a, b) => b[1] - a[1])
          .map(([name, val]) => (
            <div key={name} style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 11, fontFamily: fonts.mono }}>
              <div style={{ width: 90, color: colors.textDim }}>{metricLabel(name)}</div>
              <div style={{ flex: 1, height: 6, background: colors.border, borderRadius: 3, overflow: "hidden" }}>
                <div style={{ width: `${Math.max(0, Math.min(1, val)) * 100}%`, height: "100%", background: deficitColor(val) }} />
              </div>
              <div style={{ width: 40, textAlign: "right", color: colors.textLight }}>{val.toFixed(2)}</div>
            </div>
          ))}
      </div>
    </div>
  );
}

// --- Diagnostics tab ----------------------------------------------

interface DiagnosticsTabProps {
  tiles: QualityFileTile[];
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
  btnStyle: React.CSSProperties;
  onSimulateDelete: (path: string) => void;
}

function DiagnosticsTab({ tiles, colors, fontSizes, btnStyle, onSimulateDelete }: DiagnosticsTabProps) {
  const [expanded, setExpanded] = useState<string | null>(null);
  const sorted = useMemo(() => [...tiles].sort((a, b) => b.deficit - a.deficit), [tiles]);

  if (sorted.length === 0) {
    return <EmptyState text="No diagnostics available — run a scan first." colors={colors} fontSizes={fontSizes} />;
  }

  return (
    <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 4 }}>
      <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 4 }}>
        Files by deficit ({sorted.length})
      </div>
      {sorted.map((t) => {
        const isOpen = expanded === t.path;
        return (
          <div key={t.path} style={{ border: `1px solid ${colors.border}`, borderRadius: 4, background: colors.surface }}>
            <button
              onClick={() => setExpanded(isOpen ? null : t.path)}
              style={{
                width: "100%",
                display: "flex",
                alignItems: "center",
                gap: 10,
                padding: "6px 10px",
                background: "transparent",
                border: "none",
                cursor: "pointer",
                color: colors.textLight,
                fontFamily: fonts.sans,
                fontSize: fontSizes.panels,
                textAlign: "left",
              }}
            >
              <div style={{ width: 8, height: 8, borderRadius: 2, background: deficitColor(t.deficit), flexShrink: 0 }} />
              <div style={{ flex: 1, fontFamily: fonts.mono, fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {t.path}
              </div>
              <div style={{ fontSize: 11, color: colors.textDim, fontFamily: fonts.mono }}>
                {t.loc} LOC · {t.deficit.toFixed(2)}{t.top_reason ? ` · ${metricLabel(t.top_reason)}` : ""}
              </div>
            </button>
            {isOpen && (
              <TileDetail
                tile={t}
                onClose={() => setExpanded(null)}
                onSimulateDelete={onSimulateDelete}
                colors={colors}
                fontSizes={fontSizes}
                btnStyle={btnStyle}
              />
            )}
          </div>
        );
      })}
    </div>
  );
}

// --- Cycles tab ---------------------------------------------------

interface AsyncTabProps {
  channelId: string;
  scanGeneration: number;
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
}

function CyclesTab({ channelId, scanGeneration, colors, fontSizes }: AsyncTabProps) {
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
          <div style={{ fontSize: 11, color: colors.textDim, marginBottom: 4 }}>Cycle #{idx + 1} ({cyc.length} files)</div>
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

// --- Evolution tab ------------------------------------------------

function EvolutionTab({ channelId, scanGeneration, colors, fontSizes }: AsyncTabProps) {
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

// --- What-if tab --------------------------------------------------

interface WhatifTabProps {
  target: string | null;
  loading: boolean;
  error: string | null;
  result: QualityWhatifResponse | null;
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
}

function WhatifTab({ target, loading, error, result, colors, fontSizes }: WhatifTabProps) {
  if (!target && !loading) {
    return (
      <EmptyState
        text="No simulation yet. Click a tile in Overview or Diagnostics, then ‘Simulate delete’."
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
  const deltaLabel = delta > 0 ? `+${delta}` : `${delta}`;

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

// --- C4 tab -------------------------------------------------------

function C4Tab({ channelId, scanGeneration, colors, fontSizes }: AsyncTabProps) {
  const { data, error, loading } = useAsyncFetch<QualityC4Response>(
    () => fetchQualityC4(channelId),
    [channelId, scanGeneration],
  );
  const { colors: theme } = useTheme();
  const containerEl = useRef<HTMLDivElement | null>(null);
  const [renderError, setRenderError] = useState<string | null>(null);

  useEffect(() => {
    if (!data || !containerEl.current) return;
    let cancelled = false;
    setRenderError(null);
    // Clear any prior SVG (success or error placeholder) so we never show
    // stale output from a previous render while the new one is in flight.
    containerEl.current.innerHTML = "";
    (async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        mermaid.initialize({
          startOnLoad: false,
          theme: theme.isDark ? "dark" : "default",
          securityLevel: "strict",
        });
        const id = `quality-c4-${Date.now()}`;
        const { svg } = await mermaid.render(id, data.mermaid);
        if (!cancelled && containerEl.current) {
          containerEl.current.innerHTML = svg;
        }
      } catch (e) {
        if (!cancelled) setRenderError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [data, theme.isDark]);

  if (loading) return <LoadingState colors={colors} fontSizes={fontSizes} />;
  if (error) return <ErrorState message={error} colors={colors} fontSizes={fontSizes} />;
  if (!data || data.component_count === 0) {
    return <EmptyState text="No components to diagram — run a scan first." colors={colors} fontSizes={fontSizes} />;
  }

  return (
    <div style={{ padding: 12, display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ fontSize: 11, color: colors.textDim }}>
        {data.component_count} components · {data.edge_count} edges
      </div>
      {renderError && <ErrorState message={renderError} colors={colors} fontSizes={fontSizes} />}
      <div ref={containerEl} style={{ overflow: "auto", padding: 12, background: colors.surface, border: `1px solid ${colors.border}`, borderRadius: 4 }} />
    </div>
  );
}

// --- Async fetch helper -------------------------------------------

function useAsyncFetch<T>(fn: () => Promise<T>, deps: React.DependencyList): { data: T | null; error: string | null; loading: boolean } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    fn()
      .then((res) => {
        if (!cancelled) setData(res);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return { data, error, loading };
}

// --- Shared mini-states -------------------------------------------

interface StateProps {
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
}

function LoadingState({ colors, fontSizes }: StateProps) {
  return (
    <div style={{ padding: 24, color: colors.textDim, fontSize: fontSizes.panels, textAlign: "center" }}>Loading…</div>
  );
}

function ErrorState({ message, colors, fontSizes }: StateProps & { message: string }) {
  return (
    <div style={{ padding: 12, color: colors.error, fontSize: fontSizes.panels, fontFamily: fonts.mono, wordBreak: "break-word" }}>
      {message}
    </div>
  );
}

function EmptyState({ text, colors, fontSizes }: StateProps & { text: string }) {
  return (
    <div style={{ padding: 24, color: colors.textDim, fontSize: fontSizes.panels, textAlign: "center", lineHeight: 1.5 }}>
      {text}
    </div>
  );
}

function SectionHelp({ colors, children }: { colors: ReturnType<typeof useTheme>["colors"]; children: React.ReactNode }) {
  return (
    <div
      style={{
        fontSize: 11,
        lineHeight: 1.5,
        color: colors.textDim,
        background: colors.surface,
        border: `1px solid ${colors.border}`,
        borderRadius: 4,
        padding: "6px 10px",
        marginBottom: 8,
      }}
    >
      {children}
    </div>
  );
}
