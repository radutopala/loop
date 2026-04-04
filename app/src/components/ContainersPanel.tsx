import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";
import type { WSEvent } from "../types";
import type { ContainerInfo } from "../api/loopApi";
import { fetchContainers } from "../api/loopApi";
import { fonts } from "../theme";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";

function buildHeaderBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: 4,
    lineHeight: 1,
    borderRadius: 4,
    display: "flex",
    alignItems: "center",
  };
}

const TYPE_LABELS: Record<string, string> = {
  agent: "Agent",
  shell: "Shell",
  chrome: "Chrome",
};

function statusColor(status: string, colors: ColorPalette): string {
  if (status === "pending-removal") return colors.warning;
  if (status === "stopped") return colors.textDim;
  return colors.active;
}

interface ContainerEventData {
  container_id: string;
  channel_id: string;
  type: string;
  status: string;
  container_name?: string;
  remove_at?: string;
}

export interface ContainersPanelHandle {
  handleContainerEvent: (event: WSEvent) => void;
}

interface ContainersPanelProps {
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onClose: () => void;
}

export const ContainersPanel = forwardRef<ContainersPanelHandle, ContainersPanelProps>(function ContainersPanel({ sidebarOpen, onToggleSidebar, onOpenPalette, onClose }, ref) {
  const { colors, fontSizes } = useTheme();
  const [containers, setContainers] = useState<ContainerInfo[]>([]);
  const containersRef = useRef(containers);
  containersRef.current = containers;

  const headerBtnStyle = buildHeaderBtnStyle(colors);
  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

  // Load containers on mount.
  useEffect(() => {
    fetchContainers()
      .then(setContainers)
      .catch(() => {});
  }, []);

  const handleContainerEvent = useCallback((event: WSEvent) => {
    const data = event.data as ContainerEventData;
    if (!data?.container_id) return;

    switch (event.type) {
      case "container.registered": {
        const next = containersRef.current.filter((c) => c.container_id !== data.container_id);
        next.push({
          container_id: data.container_id,
          channel_id: data.channel_id,
          type: data.type as ContainerInfo["type"],
          status: (data.status || "running") as ContainerInfo["status"],
          container_name: data.container_name,
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        });
        setContainers(next);
        break;
      }
      case "container.removed": {
        setContainers(containersRef.current.filter((c) => c.container_id !== data.container_id));
        break;
      }
      case "container.status_changed": {
        setContainers(
          containersRef.current.map((c) =>
            c.container_id === data.container_id
              ? { ...c, status: (data.status || c.status) as ContainerInfo["status"], remove_at: data.remove_at, updated_at: new Date().toISOString() }
              : c,
          ),
        );
        break;
      }
    }
  }, []);

  useImperativeHandle(ref, () => ({ handleContainerEvent }), [handleContainerEvent]);

  // Keyboard shortcut: Escape to close.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); onClose(); }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  const formatTime = (ts: string) => {
    try {
      const d = new Date(ts);
      return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
    } catch {
      return "";
    }
  };

  return (
    <div
      style={{
        flex: 1,
        backgroundColor: colors.sidebar,
        zoom: fontSizes.panels / 12,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        borderRadius: colors.islandRadius,
        boxShadow: colors.islandShadow,
        border: colors.islandBorder,
      }}
    >
      {/* Drag region */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: sidebarOpen === false ? 76 : 4,
          WebkitAppRegion: "drag",
        }}
      >
        {onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
        )}
        {onOpenPalette && (
          <button
            onClick={onOpenPalette}
            title="Search messages (Cmd+K)"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontFamily: fonts.mono,
              marginLeft: 6,
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
          </button>
        )}
        <div style={{ flex: 1 }} />
      </div>

      {/* Header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
          boxSizing: "border-box",
          height: 35,
        }}
      >
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          Containers
        </span>
        <button
          onClick={onClose}
          title="Close panel"
          style={headerBtnStyle}
          onMouseEnter={hoverIn}
          onMouseLeave={hoverOut}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      {/* Content */}
      <div style={{ flex: 1, overflow: "auto", padding: 12 }}>
        {containers.length === 0 ? (
          <div style={{ color: colors.textDim, fontSize: 13, textAlign: "center", marginTop: 40 }}>
            No containers
          </div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            {[...containers].sort((a, b) => {
              const ar = a.status === "running" ? 1 : 0;
              const br = b.status === "running" ? 1 : 0;
              if (ar !== br) return br - ar;
              return new Date(b.created_at).getTime() - new Date(a.created_at).getTime();
            }).map((c) => (
              <div
                key={c.container_id}
                style={{
                  backgroundColor: colors.bg,
                  border: `1px solid ${colors.border}`,
                  borderRadius: 8,
                  padding: "10px 12px",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 6 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <span
                      style={{
                        fontSize: 10,
                        fontWeight: 600,
                        textTransform: "uppercase",
                        letterSpacing: 0.5,
                        color: colors.textLight,
                        backgroundColor: colors.surface,
                        padding: "2px 6px",
                        borderRadius: 4,
                      }}
                    >
                      {TYPE_LABELS[c.type] || c.type}
                    </span>
                    <span
                      style={{
                        width: 6,
                        height: 6,
                        borderRadius: "50%",
                        backgroundColor: statusColor(c.status, colors),
                      }}
                    />
                    <span style={{ fontSize: 11, color: colors.textDim }}>
                      {c.status}{c.remove_at && ` (removal at ${formatTime(c.remove_at)})`}
                    </span>
                  </div>
                </div>
                {c.container_name && (
                  <div style={{ fontSize: 12, color: colors.textLight, fontFamily: fonts.mono, marginBottom: 4, wordBreak: "break-all" }}>
                    {c.container_name}
                  </div>
                )}
                <div style={{ display: "flex", gap: 16, fontSize: 11, color: colors.textDim }}>
                  <span title="Container ID">{c.container_id.slice(0, 12)}</span>
                  <span title="Channel ID">ch: {c.channel_id.slice(0, 12)}</span>
                  {c.created_at && <span title="Created">{formatTime(c.created_at)}</span>}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
});
