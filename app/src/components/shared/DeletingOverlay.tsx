import { useLayoutEffect, useState } from "react";
import { useTheme } from "../../ThemeContext";

/**
 * DeletingOverlay covers the workspace right of the sidebar with message
 * while a channel delete runs on the daemon, so nothing in the workspace is
 * clicked until the channel leaves the sidebar.
 */
export function DeletingOverlay({ message }: { message: string }) {
  const { colors } = useTheme();
  const [left, setLeft] = useState(0);
  useLayoutEffect(() => {
    const sidebar = document.querySelector('[data-testid="sidebar"]');
    setLeft(sidebar ? sidebar.getBoundingClientRect().right : 0);
  }, []);
  return (
    <div
      data-testid="deleting-overlay"
      style={{
        position: "fixed",
        top: 0,
        bottom: 0,
        right: 0,
        left,
        zIndex: 900,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        backgroundColor: `color-mix(in srgb, ${colors.canvas} 75%, transparent)`,
        backdropFilter: "blur(2px)",
        cursor: "wait",
      }}
    >
      <style>{"@keyframes loop-deleting-spin { to { transform: rotate(360deg); } }"}</style>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          padding: "10px 16px",
          borderRadius: 8,
          border: `1px solid ${colors.border}`,
          backgroundColor: colors.surface,
          color: colors.textLight,
          fontSize: 13,
          boxShadow: `0 4px 12px ${colors.shadow}`,
        }}
      >
        <span
          style={{
            width: 14,
            height: 14,
            borderRadius: "50%",
            border: `2px solid ${colors.border}`,
            borderTopColor: colors.textLight,
            animation: "loop-deleting-spin 0.8s linear infinite",
          }}
        />
        {message}
      </div>
    </div>
  );
}
