import { useMemo, useState } from "react";
import { useTheme } from "../../../ThemeContext";
import { fonts } from "../../../theme";
import type { QualityFileTile } from "../../../api/quality";
import { deficitColor, metricLabel, EmptyState, TileDetail } from "./shared";

export interface DiagnosticsTabProps {
  tiles: QualityFileTile[];
  colors: ReturnType<typeof useTheme>["colors"];
  fontSizes: ReturnType<typeof useTheme>["fontSizes"];
  btnStyle: React.CSSProperties;
  onSimulateDelete: (path: string) => void;
}

export function DiagnosticsTab({ tiles, colors, fontSizes, btnStyle, onSimulateDelete }: DiagnosticsTabProps) {
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
