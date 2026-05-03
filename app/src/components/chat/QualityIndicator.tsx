import { useCallback, useEffect, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { useEventStream } from "../../hooks/useEventStream";
import { fetchQualitySnapshot } from "../../api/quality";

interface QualityIndicatorProps {
  channelId: string;
}

function bandColor(signal: number): string {
  if (signal < 5000) return "#ef4444";
  if (signal < 7000) return "#f59e0b";
  return "#22c55e";
}

export function QualityIndicator({ channelId }: QualityIndicatorProps) {
  const { colors } = useTheme();
  const [signal, setSignal] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    try {
      const snap = await fetchQualitySnapshot(channelId);
      setSignal(snap?.signal ?? null);
    } catch {
      setSignal(null);
    }
  }, [channelId]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const onEvent = useCallback(
    (event: { type: string; data: unknown }) => {
      if (event.type === "quality.scanned") {
        const d = event.data as { signal?: number } | null;
        if (d && typeof d.signal === "number") {
          setSignal(d.signal);
        } else {
          refresh();
        }
      }
    },
    [refresh],
  );

  useEventStream({ channelId, onEvent });

  const filled = signal !== null;
  const color = filled ? bandColor(signal) : "transparent";
  const borderColor = filled ? color : colors.border;
  const tooltip = filled
    ? `Quality signal: ${signal}`
    : "Quality — not scanned yet (click to open panel)";

  const handleClick = useCallback(() => {
    window.dispatchEvent(
      new CustomEvent("loop:open-panel", {
        detail: { channelId, panel: "quality" },
      }),
    );
  }, [channelId]);

  return (
    <button
      onClick={handleClick}
      title={tooltip}
      style={{
        width: 28,
        height: 28,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "transparent",
        border: "none",
        cursor: "pointer",
        flexShrink: 0,
        padding: 0,
      }}
    >
      <span
        style={{
          width: 12,
          height: 12,
          borderRadius: "50%",
          background: color,
          border: `2px solid ${borderColor}`,
          display: "block",
        }}
      />
    </button>
  );
}
