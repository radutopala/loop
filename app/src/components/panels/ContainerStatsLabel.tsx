import type { ContainerStatsEntry } from "../../api/channels";
import { fmtBytes } from "../../hooks/useContainerStats";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

/** A pane header's CPU and memory label for the container backing it. */
export function ContainerStatsLabel({ entry }: { entry?: ContainerStatsEntry }) {
  const { colors } = useTheme();
  if (!entry) return null;
  return (
    <span
      data-testid="container-stats"
      title={`Container ${entry.container_id.slice(0, 12)} — CPU ${entry.cpu_percent.toFixed(1)}%, memory ${fmtBytes(entry.mem_usage)} of ${fmtBytes(entry.mem_limit)}`}
      style={{ fontSize: 9, fontFamily: fonts.mono, color: colors.textDim, flexShrink: 0, marginRight: 2, whiteSpace: "nowrap" }}
    >
      {Math.round(entry.cpu_percent)}% · {fmtBytes(entry.mem_usage)}
    </span>
  );
}
