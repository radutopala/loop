import { useCallback, useEffect, useMemo, useState } from "react";
import { hierarchy } from "@visx/hierarchy";
import { useTheme } from "../../ThemeContext";
import { useEventStream } from "../../hooks/useEventStream";
import { fonts } from "../../theme";
import {
  fetchQualitySnapshot,
  NoPreviousSignal,
  simulateQualityWhatif,
  triggerQualityScan,
  type QualityFileTile,
  type QualityMetric,
  type QualityRule,
  type QualityScanReport,
  type QualitySnapshot,
  type QualityWhatifResponse,
} from "../../api/quality";
import {
  bandColor,
  buildTreemapData,
  formatScannedAt,
  type TreemapDatum,
} from "./quality/shared";
import { OverviewTab } from "./quality/OverviewTab";
import { DiagnosticsTab } from "./quality/DiagnosticsTab";
import { HotspotsTab } from "./quality/HotspotsTab";
import { CyclesTab } from "./quality/CyclesTab";
import { EvolutionTab } from "./quality/EvolutionTab";
import { WhatifTab } from "./quality/WhatifTab";
import { C4Tab } from "./quality/C4Tab";

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

type TabId = "overview" | "diagnostics" | "hotspots" | "cycles" | "evolution" | "whatif" | "c4";

const TABS: { id: TabId; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "diagnostics", label: "Diagnostics" },
  { id: "hotspots", label: "Hotspots" },
  { id: "cycles", label: "Cycles" },
  { id: "evolution", label: "Evolution" },
  { id: "whatif", label: "What-if" },
  { id: "c4", label: "C4" },
];

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
  const deltaLabel = delta > 0 ? `Δ +${delta}` : `Δ ${delta}`;
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
          Snapshot taken on "{snapshot.branch}", current branch "{snapshot.current_branch}" — Scan now to refresh
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
          // Absolute signal is the primary headline (band-coloured so
          // red/amber/green is the first thing the eye lands on); the
          // Δ-since-last-scan rides alongside as a secondary chip and
          // the previous value drops below in muted text.
          <div style={{ display: "flex", alignItems: "baseline", gap: 14, flexWrap: "wrap" }}>
            <span style={{ fontSize: 48, fontWeight: 700, color: sigColor, fontFamily: fonts.mono, lineHeight: 1 }}>
              {sig}
            </span>
            <span
              style={{ fontSize: 18, fontWeight: 600, color: deltaColor, fontFamily: fonts.mono }}
              title="Change in signal since the last scan of this branch"
            >
              {deltaLabel}
            </span>
            <span style={{ color: colors.textDim, fontSize: 12 }}>
              from <span style={{ fontFamily: fonts.mono }}>{prev}</span>
              {" · "}geo-mean {display?.geo_mean.toFixed(3)} · branch "{display?.branch}"
              {display?.scanned_at ? ` · ${formatScannedAt(display.scanned_at)}` : ""}
            </span>
          </div>
        ) : (
          // First scan ever for this (channel, branch): no delta to
          // render — fall back to the absolute headline.
          <div style={{ display: "flex", alignItems: "baseline", gap: 12, flexWrap: "wrap" }}>
            <span style={{ fontSize: 48, fontWeight: 700, color: sigColor, fontFamily: fonts.mono, lineHeight: 1 }}>{sig}</span>
            <span style={{ color: colors.textDim, fontSize: 12 }}>
              first scan · geo-mean {display?.geo_mean.toFixed(3)} · branch "{display?.branch}"
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

        {activeTab === "hotspots" && (
          <HotspotsTab channelId={channelId} scanGeneration={scanGeneration} colors={colors} fontSizes={fontSizes} />
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
