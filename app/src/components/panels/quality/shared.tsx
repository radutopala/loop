import { useEffect, useState } from "react";
import { useTheme } from "../../../ThemeContext";
import { fonts } from "../../../theme";
import type { QualityFileTile, QualityRule } from "../../../api/quality";

// ── Shared types ──────────────────────────────────────────────────────────────

export interface TreemapDatum {
  name: string;
  size?: number;
  deficit?: number;
  tile?: QualityFileTile;
  children?: TreemapDatum[];
}

export interface AsyncTabProps {
  channelId: string;
  scanGeneration: number;
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
}

export interface HotspotSectionProps {
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
}

export interface StateProps {
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
}

export interface TileDetailProps {
  tile: QualityFileTile;
  onClose: () => void;
  onSimulateDelete: (path: string) => void;
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
  btnStyle: React.CSSProperties;
}

// ── Shared helpers ────────────────────────────────────────────────────────────

export function shortPath(path: string): string {
  const parts = path.split("/");
  if (parts.length <= 2) return path;
  return parts.slice(-2).join("/");
}

export function bandColor(signal: number): string {
  if (signal < 5000) return "#ef4444";
  if (signal < 7000) return "#f59e0b";
  return "#22c55e";
}

export function deficitColor(deficit: number): string {
  // 0 = healthy green, 1 = full red drag.
  const clamped = Math.max(0, Math.min(1, deficit));
  const r = Math.round(34 + (239 - 34) * clamped);
  const g = Math.round(197 - (197 - 68) * clamped);
  const b = Math.round(94 - (94 - 68) * clamped);
  return `rgb(${r}, ${g}, ${b})`;
}

export function metricLabel(name: string): string {
  const map: Record<string, string> = {
    modularity: "Modularity",
    cycles: "Cycles",
    depth: "Depth",
    equality: "Equality",
    redundancy: "Redundancy",
    complexity: "Complexity",
  };
  return map[name] ?? name;
}

export function formatScannedAt(ts: string): string {
  if (!ts) return "";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleString();
}

export function buildTreemapData(tiles: QualityFileTile[]): TreemapDatum {
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

// ── Async fetch helper ────────────────────────────────────────────────────────

export function useAsyncFetch<T>(fn: () => Promise<T>, deps: React.DependencyList): { data: T | null; error: string | null; loading: boolean } {
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

// ── Shared mini-states ────────────────────────────────────────────────────────

export function LoadingState({ colors, fontSizes }: StateProps) {
  return (
    <div style={{ padding: 24, color: colors.textDim, fontSize: fontSizes.panels, textAlign: "center" }}>Loading…</div>
  );
}

export function ErrorState({ message, colors, fontSizes }: StateProps & { message: string }) {
  return (
    <div style={{ padding: 12, color: colors.error, fontSize: fontSizes.panels, fontFamily: fonts.mono, wordBreak: "break-word" }}>
      {message}
    </div>
  );
}

export function EmptyState({ text, colors, fontSizes }: StateProps & { text: string }) {
  return (
    <div style={{ padding: 24, color: colors.textDim, fontSize: fontSizes.panels, textAlign: "center", lineHeight: 1.5 }}>
      {text}
    </div>
  );
}

export function SectionHelp({ colors, children }: { colors: ReturnType<typeof useTheme>["colors"]; children: React.ReactNode }) {
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

// --- TileDetail (shared by Overview + Diagnostics) ----------------

export function TileDetail({ tile, onClose, onSimulateDelete, colors, fontSizes, btnStyle }: TileDetailProps) {
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

// ── Types re-exported for QualityPanel shell ──────────────────────────────────

export type { QualityRule };
