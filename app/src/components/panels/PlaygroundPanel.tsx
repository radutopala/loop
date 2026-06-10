import { useCallback, useEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fetchPlayground, fetchPlaygroundItems, getApiUrl, type PlaygroundItem } from "../../api/loopApi";
import { useEventStream } from "../../hooks/useEventStream";
import type { WSEvent } from "../../types";
import { storageGetJSON, storageSetJSON } from "../../utils/storage";
import { logErr } from "../../utils/log";

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

function playgroundSelectionKey(name: string, scope: "global" | "project"): string {
  return `${scope}:${name}`;
}

export function PlaygroundPanel({ channelId }: PlaygroundPanelProps) {
  const { colors, fontSizes } = useTheme();
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
  const itemsRef = useRef<PlaygroundItem[]>([]);
  itemsRef.current = items;
  const storageKey = `playground-active:${channelId}`;
  const [activeItem, setActiveItem] = useState<string>(() => {
    return storageGetJSON<{ name?: string }>(storageKey)?.name || "";
  });
  const activeItemRef = useRef("");
  activeItemRef.current = activeItem;
  const [activeScope, setActiveScope] = useState<"global" | "project">(() => {
    return storageGetJSON<{ scope?: "global" | "project" }>(storageKey)?.scope || "global";
  });
  const activeScopeRef = useRef<"global" | "project">("global");
  activeScopeRef.current = activeScope;

  const selectItem = useCallback((name: string, scope?: "global" | "project") => {
    setActiveItem(name);
    const resolved = scope ?? itemsRef.current.find((i) => i.name === name)?.scope ?? "global";
    setActiveScope(resolved);
    storageSetJSON(storageKey, { name, scope: resolved });
  }, [storageKey]);

  // Load items list on mount.
  useEffect(() => {
    fetchPlaygroundItems(channelId).then(setItems).catch(logErr("fetching playground items"));
  }, [channelId]);

  // Load content when active item changes.
  useEffect(() => {
    if (!activeItem) return;
    fetchPlayground(activeItem, activeScope, channelId).then((data) => {
      if (data) {
        setCode(data);
        setTitle(data.title || "");
        setDescription(data.description || "");
      } else {
        setCode({ html: "" });
        setTitle("");
        setDescription("");
      }
    }).catch(logErr("loading playground item"));
  }, [activeItem, activeScope, channelId]);

  // Listen for live updates from agent via EventsHub.
  useEventStream({
    channelId,
    onEvent: useCallback((event: WSEvent) => {
      if (event.type === "playground.update") {
        const data = event.data as Record<string, string>;
        const eventName = data.name || "";
        if (!eventName) return;
        const eventScope = data.scope === "project" ? "project" : "global";
        const eventChannelId = data.channel_id || "";
        if (eventScope === "project" && eventChannelId !== channelId) return;
        // Refresh items list (a new item may have been created).
        fetchPlaygroundItems(channelId).then((list) => {
          setItems(list);
          // Auto-switch to the playground being worked on.
          if (eventName !== activeItemRef.current || eventScope !== activeScopeRef.current) {
            const found = list.find((i) => i.name === eventName && i.scope === eventScope);
            selectItem(eventName, found?.scope);
          }
        }).catch(logErr("refreshing playground items"));
        // Reload active item with fresh content from server.
        if (eventName === activeItemRef.current && eventScope === activeScopeRef.current) {
          if (data.html) {
            // Full playground update — update metadata (triggers iframe reload via code change).
            setCode(data as unknown as PlaygroundCode);
            setTitle(data.title || "");
            setDescription(data.description || "");
          } else {
            // File-level update — just reload iframe (server serves fresh files from disk).
            setIframeVersion((v) => v + 1);
          }
        }
      }
    }, [channelId, selectItem]),
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
      <div data-testid="playground-panel" style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", backgroundColor: colors.bg, gap: 12 }}>
        <div style={{ color: colors.textDim, fontSize: 13 }}>
          {items.length > 0 ? "Select a playground" : "No playgrounds yet — ask an agent to create one, or run onboard:global to install examples"}
        </div>
        {items.length > 0 && (
          <PlaygroundSelector items={items} value="" onChange={selectItem} colors={colors} style={{ fontSize: 13, padding: "6px 12px" }} />
        )}
      </div>
    );
  }

  return (
    <div data-testid="playground-panel" style={{ flex: 1, display: "flex", flexDirection: "column", backgroundColor: colors.bg, overflow: "hidden", zoom: fontSizes.panels / 12 }}>
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
          <PlaygroundSelector items={items} value={playgroundSelectionKey(activeItem, activeScope)} onChange={selectItem} colors={colors} />
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
        src={activeScope === "project"
          ? `${getApiUrl()}/api/playground/serve-project/${encodeURIComponent(channelId)}/${encodeURIComponent(activeItem)}?v=${iframeVersion}`
          : `${getApiUrl()}/api/playground/serve/${encodeURIComponent(activeItem)}?v=${iframeVersion}`
        }
        sandbox="allow-scripts allow-same-origin allow-forms"
        style={{ flex: 1, border: "none", width: "100%" }}
        onMouseEnter={() => {
          // Blur any focused element (e.g. chat textarea) so keyboard events reach the iframe.
          if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
          iframeRef.current?.focus();
        }}
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

function PlaygroundSelector({ items, value, onChange, colors, style }: {
  items: PlaygroundItem[];
  value: string;
  onChange: (name: string, scope: "global" | "project") => void;
  colors: { border: string; textLight: string };
  style?: React.CSSProperties;
}) {
  const globalItems = items.filter((i) => i.scope !== "project");
  const projectItems = items.filter((i) => i.scope === "project");

  return (
    <select
      value={value}
      onChange={(e) => {
        const selectedKey = e.target.value;
        const item = items.find((i) => playgroundSelectionKey(i.name, i.scope) === selectedKey);
        if (item) {
          onChange(item.name, item.scope);
        }
      }}
      style={{
        background: "none",
        border: `1px solid rgba(128,128,128,0.3)`,
        borderRadius: 4,
        color: colors.textLight,
        fontSize: 11,
        padding: "2px 4px",
        ...style,
      }}
    >
      {!value && <option value="" disabled>Choose playground...</option>}
      {projectItems.length > 0 && (
        <optgroup label="Project">
          {projectItems.map((item) => (
            <option key={playgroundSelectionKey(item.name, item.scope)} value={playgroundSelectionKey(item.name, item.scope)}>{item.title || item.name}</option>
          ))}
        </optgroup>
      )}
      {globalItems.length > 0 && (
        <optgroup label="Global">
          {globalItems.map((item) => (
            <option key={playgroundSelectionKey(item.name, item.scope)} value={playgroundSelectionKey(item.name, item.scope)}>{item.title || item.name}</option>
          ))}
        </optgroup>
      )}
    </select>
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
