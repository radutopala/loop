import { useEffect, useRef, useState } from "react";
import { fetchQualityC4, type QualityC4Response } from "../../../api/quality";
import { useTheme } from "../../../ThemeContext";
import {
  useAsyncFetch,
  LoadingState,
  ErrorState,
  EmptyState,
  type AsyncTabProps,
} from "./shared";

export function C4Tab({ channelId, scanGeneration, colors, fontSizes }: AsyncTabProps) {
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
