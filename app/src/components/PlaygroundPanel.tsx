import { useCallback, useEffect, useRef, useState } from "react";
import { useTheme } from "../ThemeContext";
import { fetchPlayground, fetchPlaygroundItems, getApiUrl, type PlaygroundItem } from "../api/loopApi";
import { useEventStream } from "../hooks/useEventStream";
import type { WSEvent } from "../types";

interface PlaygroundCode {
  html: string;
  name?: string;
  title?: string;
  description?: string;
}

interface ConsoleEntry {
  level: number; // 0=debug, 1=info, 2=warn, 3=error
  message: string;
  time: number;
}

interface PlaygroundPanelProps {
  channelId: string;
}

export function PlaygroundPanel({ channelId }: PlaygroundPanelProps) {
  const { colors } = useTheme();
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const [iframeVersion, setIframeVersion] = useState(0);
  const [code, setCode] = useState<PlaygroundCode>({ html: "" });
  const [consoleMessages, setConsoleMessages] = useState<ConsoleEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [showConsole, setShowConsole] = useState(false);
  const [showInfo, setShowInfo] = useState(false);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [items, setItems] = useState<PlaygroundItem[]>([]);
  const [activeItem, setActiveItem] = useState<string>("");

  // Load items list on mount.
  useEffect(() => {
    fetchPlaygroundItems().then(setItems).catch(() => {});
  }, [channelId]);

  // Load content when active item changes.
  useEffect(() => {
    if (!activeItem) return;
    fetchPlayground(activeItem).then((data) => {
      if (data) {
        setCode(data);
        setTitle(data.title || "");
        setDescription(data.description || "");
      } else {
        setCode({ html: "" });
        setTitle("");
        setDescription("");
      }
    }).catch(() => {});
  }, [activeItem]);

  // Listen for live updates from agent via EventsHub.
  useEventStream({
    channelId,
    onEvent: useCallback((event: WSEvent) => {
      if (event.type === "playground.update") {
        const data = event.data as PlaygroundCode;
        const eventName = data.name || "";
        // Refresh items list (a new item may have been created).
        fetchPlaygroundItems().then((list) => {
          setItems(list);
          // Auto-switch to newly created items.
          if (eventName && !items.some((i) => i.name === eventName)) {
            setActiveItem(eventName);
          }
        }).catch(() => {});
        // Update rendered code if it matches the active item.
        if (eventName === activeItem) {
          setCode(data);
          setTitle(data.title || "");
          setDescription(data.description || "");
        }
      }
    }, [activeItem, items]),
  });

  // Listen for console messages from iframe via postMessage.
  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.data && e.data.type === "playground-console") {
        setConsoleMessages((prev) => [
          ...prev.slice(-199),
          { level: e.data.level, message: e.data.message, time: Date.now() },
        ]);
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  // Reload iframe when code changes (server serves the latest files).
  useEffect(() => {
    setIframeVersion((v) => v + 1);
  }, [code]);

  function handleReset() {
    setError(null);
    setConsoleMessages([]);
    setIframeVersion((v) => v + 1);
  }

  // Empty state — no item selected.
  if (!activeItem) {
    return (
      <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", backgroundColor: colors.bg, gap: 12 }}>
        <div style={{ color: colors.textDim, fontSize: 13 }}>
          {items.length > 0 ? "Select a playground" : "No playgrounds yet — ask an agent to create one, or run onboard:global to install examples"}
        </div>
        {items.length > 0 && (
          <select
            value=""
            onChange={(e) => setActiveItem(e.target.value)}
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              borderRadius: 6,
              color: colors.textLight,
              fontSize: 13,
              padding: "6px 12px",
            }}
          >
            <option value="" disabled>Choose playground...</option>
            {items.map((item) => (
              <option key={item.name} value={item.name}>{item.title || item.name}</option>
            ))}
          </select>
        )}
      </div>
    );
  }

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", backgroundColor: colors.bg, overflow: "hidden" }}>
      {/* Toolbar */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "4px 8px",
          backgroundColor: colors.surface,
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
          fontSize: 12,
        }}
      >
        {items.length > 0 && (
          <select
            value={activeItem}
            onChange={(e) => setActiveItem(e.target.value)}
            style={{
              background: "none",
              border: "1px solid rgba(128,128,128,0.3)",
              borderRadius: 4,
              color: colors.textLight,
              fontSize: 11,
              padding: "2px 4px",
            }}
          >
            {items.map((item) => (
              <option key={item.name} value={item.name}>{item.title || item.name}</option>
            ))}
          </select>
        )}
        <ToolbarButton onClick={handleReset} title="Reset" colors={colors}>
          Reset
        </ToolbarButton>
        <ToolbarButton onClick={() => setShowConsole(!showConsole)} title="Toggle console" colors={colors}>
          Console {consoleMessages.length > 0 && `(${consoleMessages.length})`}
        </ToolbarButton>
        {description && (
          <ToolbarButton onClick={() => setShowInfo(!showInfo)} title="Toggle info" colors={colors}>
            Info
          </ToolbarButton>
        )}
        {title && (
          <span style={{ marginLeft: 8, color: colors.textDim, fontSize: 11, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>
            {title}
          </span>
        )}
      </div>

      {/* Info panel (collapsible) */}
      {showInfo && description && (
        <div
          style={{
            padding: "8px 12px",
            fontSize: 12,
            color: colors.textLight,
            backgroundColor: colors.isDark ? "#1a1a2e" : "#f0f4ff",
            borderBottom: `1px solid ${colors.border}`,
            whiteSpace: "pre-wrap",
            maxHeight: 150,
            overflowY: "auto",
          }}
        >
          {description}
        </div>
      )}

      {/* Error banner */}
      {error && (
        <div style={{ padding: "4px 12px", fontSize: 11, color: colors.error, backgroundColor: "rgba(239,68,68,0.1)", borderBottom: `1px solid ${colors.border}` }}>
          {error}
        </div>
      )}

      {/* Sandbox iframe — served from the backend so relative imports work */}
      <iframe
        ref={iframeRef}
        src={`${getApiUrl()}/api/playground/serve/${encodeURIComponent(activeItem)}?v=${iframeVersion}`}
        sandbox="allow-scripts allow-same-origin allow-forms"
        style={{ flex: 1, border: "none", width: "100%" }}
      />

      {/* Console (collapsible) */}
      {showConsole && (
        <div
          style={{
            maxHeight: 150,
            overflowY: "auto",
            backgroundColor: colors.isDark ? "#1a1a1a" : "#f5f5f5",
            borderTop: `1px solid ${colors.border}`,
            padding: "4px 8px",
            fontSize: 11,
            fontFamily: "JetBrains Mono, Courier New, monospace",
          }}
        >
          {consoleMessages.length === 0 && (
            <div style={{ color: colors.textDim }}>No console output</div>
          )}
          {consoleMessages.map((msg, i) => (
            <div key={i} style={{ color: msg.level >= 3 ? colors.error : msg.level >= 2 ? "#f59e0b" : colors.textLight }}>
              <span style={{ color: colors.textDim }}>[{new Date(msg.time).toLocaleTimeString()}]</span>{" "}
              {msg.message}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ToolbarButton({ onClick, title, colors, children }: {
  onClick: () => void;
  title: string;
  colors: { textDim: string; textLight: string };
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      style={{
        background: "none",
        border: "1px solid rgba(128,128,128,0.3)",
        borderRadius: 4,
        color: colors.textLight,
        cursor: "pointer",
        padding: "2px 8px",
        fontSize: 11,
      }}
    >
      {children}
    </button>
  );
}
