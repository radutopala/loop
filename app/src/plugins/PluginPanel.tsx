import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { contentCapBase } from "../api/api";
import { checkFilesExist, type RootEntry } from "../api/files";
import { pluginXRequest, setPluginEnabled } from "../api/plugins";
import type { FileLinkOpenDetail } from "../components/chat/FileLink";
import { LearnSplit, SplitSeamLogo } from "../components/layout/LearnSplit";
import type { ActiveChatState } from "../hooks/useChatStateStore";
import { useContentCapsEpoch } from "../hooks/useContentCapsEpoch";
import { useEventStream } from "../hooks/useEventStream";
import { PaneLeafHeader } from "../splitPane/PaneLeafHeader";
import { useTheme } from "../ThemeContext";
import type { WSEvent } from "../types";
import type { PluginPaneRef } from "../types/panels";
import { storageGetJSON, storageSetJSON } from "../utils/storage";
import { BRIDGE_INIT, type Bridge, type BridgeHost, connectBridge, pluginTheme } from "./bridge";
import { PluginAgentBadge, PluginAgentThread, type SubscribeChannelEvents, usePluginAgent } from "./PluginAgent";
import { findPluginPanel, usePluginPanels } from "./PluginPanelsContext";

export interface PluginPanelProps {
  plugin: PluginPaneRef | undefined;
  /** The channel or thread the pane is in, which the plugin serves with a
   * container of its own. */
  channelId: string;
  channelName: string;
  dirPath: string;
  /** The pane's leaf: its header carries the agent label. */
  leafId: string;
  /** The channel's roots, which the plugin's agent thread shares. */
  roots?: RootEntry[];
  subscribeChannelEvents?: SubscribeChannelEvents;
  /** The agent thread's chat state in the app-level store, restored on
   * opening its chat and saved back on closing it. */
  getChatState?: (channelId: string) => ActiveChatState | undefined;
  onChatStateUnmount?: (channelId: string, state: ActiveChatState) => void;
  /** Where the agent's view opens, over the layout, as the Learn view does. */
  overlayHost?: HTMLElement | null;
}

const NO_SINGLETONS = new Set<never>();

/** A plugin's panel: its bundle in a sandboxed frame, talking to the app
 *  only through the bridge. Once the plugin has run an agent in the channel,
 *  the header's agent label opens the agent's chat beside the panel, over
 *  the layout, as the Learn view does. */
export function PluginPanel({ plugin: ref, channelId, channelName, dirPath, leafId, roots, subscribeChannelEvents, getChatState, onChatStateUnmount, overlayHost }: PluginPanelProps) {
  const { colors } = useTheme();
  const { plugins } = usePluginPanels();
  const found = findPluginPanel(plugins, ref);
  const pluginId = found?.plugin.id ?? "";
  const entry = found?.panel.entry ?? "";
  const panelId = found?.panel.id ?? "";

  const capsEpoch = useContentCapsEpoch();
  const [src, setSrc] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!pluginId) return;
    let cancelled = false;
    setError(null);
    contentCapBase({ kind: "plugin", name: pluginId })
      .then((base) => {
        if (!cancelled) setSrc(base + entry);
      })
      .catch((err) => {
        if (!cancelled) setError(String(err instanceof Error ? err.message : err));
      });
    return () => {
      cancelled = true;
    };
  }, [pluginId, entry, capsEpoch]);

  // The bridge reads the host from a ref, so it outlives renders.
  const hostRef = useRef<BridgeHost | null>(null);
  hostRef.current = found
    ? {
        pluginId,
        panel: panelId,
        channelId,
        channelName,
        dirPath,
        capabilities: found.plugin.capabilities,
        request: (method, path, body) => pluginXRequest(pluginId, channelId, method, path, body),
        theme: () => pluginTheme(colors),
        notify: (title, body) => {
          new Notification(title, { body });
        },
        openFile: async (path, line) => {
          const [file] = await checkFilesExist(channelId, [path]);
          if (!file?.exists || file.root_index === undefined || file.rel_path === undefined) throw new Error(`no file "${path}" in the channel`);
          const detail: FileLinkOpenDetail = { channelId, target: { rootIndex: file.root_index, relPath: file.rel_path }, line: line ?? null };
          window.dispatchEvent(new CustomEvent<FileLinkOpenDetail>("loop:open-file", { detail }));
        },
        loadState: () => storageGetJSON(`plugin-state:${pluginId}:${channelId}:${panelId}`),
        saveState: (value) => storageSetJSON(`plugin-state:${pluginId}:${channelId}:${panelId}`, value),
      }
    : null;

  const frameRef = useRef<HTMLIFrameElement>(null);
  const bridgeRef = useRef<Bridge | null>(null);
  const handleLoad = useCallback(() => {
    bridgeRef.current?.close();
    bridgeRef.current = null;
    const frame = frameRef.current?.contentWindow;
    const host = hostRef.current;
    if (!frame || !host) return;
    const channel = new MessageChannel();
    bridgeRef.current = connectBridge(channel.port1, () => hostRef.current ?? host);
    // The frame's origin is opaque, so it can only be targeted with "*".
    frame.postMessage({ type: BRIDGE_INIT }, "*", [channel.port2]);
  }, []);
  useEffect(() => () => bridgeRef.current?.close(), []);

  useEffect(() => {
    bridgeRef.current?.notify("theme", pluginTheme(colors));
  }, [colors]);

  const agent = usePluginAgent(pluginId, channelId, subscribeChannelEvents);
  const [agentOpen, setAgentOpen] = useState(false);
  const agentThread = agentOpen && overlayHost ? agent.threadId : null;
  const [frameSlot, setFrameSlot] = useState<HTMLDivElement | null>(null);
  const frameBoxRef = useRef<HTMLDivElement>(null);
  const [framePlace, setFramePlace] = useState<React.CSSProperties | null>(null);
  // In the agent's view the frame lies over the view's left half, where the
  // chat lies in the Learn view. It's placed there, not moved: moved, it
  // would load again.
  useLayoutEffect(() => {
    if (!agentThread || !frameSlot) {
      setFramePlace(null);
      return;
    }
    const place = () => {
      const want = frameSlot.getBoundingClientRect();
      setFramePlace((cur) => {
        const box = frameBoxRef.current;
        let { left, top } = want;
        // A transformed ancestor places fixed boxes relative to itself:
        // correct by how far the box landed off its slot.
        if (cur && box) {
          const got = box.getBoundingClientRect();
          left += (cur.left as number) - got.left;
          top += (cur.top as number) - got.top;
        }
        return { position: "fixed", left, top, width: want.width, height: want.height, zIndex: 21 };
      });
    };
    place();
    const raf = requestAnimationFrame(place);
    const ro = new ResizeObserver(place);
    ro.observe(frameSlot);
    window.addEventListener("resize", place);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      window.removeEventListener("resize", place);
    };
  }, [agentThread, frameSlot]);
  const agentEvent = agent.onEvent;
  const handleEvent = useCallback(
    (ev: WSEvent) => {
      agentEvent(ev);
      if (ev.type !== "plugin.event") return;
      const data = ev.data as { plugin_id?: string; type?: string; data?: unknown };
      if (data.plugin_id === pluginId) bridgeRef.current?.notify("event", { type: data.type, data: data.data });
    },
    [pluginId, agentEvent],
  );
  useEventStream({ channelId: pluginId ? channelId : null, onEvent: handleEvent });

  const [enabling, setEnabling] = useState(false);
  const [enableError, setEnableError] = useState<string | null>(null);
  const enable = useCallback(() => {
    setEnabling(true);
    setEnableError(null);
    setPluginEnabled(pluginId, true, channelId)
      .catch((err) => setEnableError(String(err instanceof Error ? err.message : err)))
      .finally(() => setEnabling(false));
  }, [pluginId, channelId]);

  if (found?.plugin.enabled === false) {
    const name = found.plugin.name || pluginId;
    return (
      <div
        data-testid="plugin-panel-disabled"
        style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 10, padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}
      >
        <div>The {name} plugin isn't enabled in this project.</div>
        <button
          data-testid="plugin-panel-enable"
          onClick={enable}
          disabled={enabling}
          style={{
            padding: "6px 14px",
            backgroundColor: colors.bg,
            border: `1px solid ${colors.border}`,
            borderRadius: 6,
            color: colors.text,
            fontSize: 12,
            cursor: enabling ? "default" : "pointer",
            fontFamily: "inherit",
          }}
        >
          {enabling ? "Enabling…" : "Enable in this project"}
        </button>
        <div style={{ fontSize: 11 }}>Sets plugins.{pluginId}.enabled in the project's .loop/config.json.</div>
        {enableError && <div style={{ color: colors.error }}>{enableError}</div>}
      </div>
    );
  }

  const message = plugins === null ? null : !found ? `The ${ref?.label || "plugin"} panel isn't available: its plugin may have been removed or not approved for panels.` : error;
  if (message !== null || !src) {
    return (
      <div
        data-testid="plugin-panel-message"
        style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}
      >
        {message}
      </div>
    );
  }
  const toggleAgent = () => setAgentOpen((o) => !o);
  const badge = (slotLeafId: string) =>
    agent.threadId !== null && overlayHost ? <PluginAgentBadge leafId={slotLeafId} running={agent.running} open={agentThread !== null} onToggle={toggleAgent} /> : null;
  const viewLeafId = `plugin-agent-${leafId}`;
  return (
    <div data-testid="plugin-panel" style={{ flex: 1, minWidth: 0, minHeight: 0, display: "flex" }}>
      <div ref={frameBoxRef} data-testid="plugin-panel-frame-box" style={{ flex: "1 1 0%", minWidth: 0, display: "flex", ...framePlace }}>
        <iframe
          ref={frameRef}
          data-testid="plugin-panel-frame"
          title={ref?.label ?? pluginId}
          src={src}
          sandbox="allow-scripts allow-forms"
          onLoad={handleLoad}
          style={{ flex: 1, border: "none", width: "100%", height: "100%", backgroundColor: colors.bg }}
        />
      </div>
      {badge(leafId)}
      {agentThread &&
        overlayHost &&
        createPortal(
          <>
            <LearnSplit
              noLogo
              testId="plugin-agent-split"
              label={`${ref?.label || pluginId} agent view`}
              shown
              ms={0}
              running={agent.running}
              chatHeader={<PaneLeafHeader leafId={viewLeafId} panel="plugin" plugin={ref} usedSingletons={NO_SINGLETONS} />}
              chat={
                <>
                  <div ref={setFrameSlot} data-testid="plugin-agent-frame-slot" style={{ flex: 1 }} />
                  {badge(viewLeafId)}
                </>
              }
              learnPane={
                <PluginAgentThread
                  key={agentThread}
                  threadId={agentThread}
                  running={agent.running}
                  roots={roots}
                  subscribeChannelEvents={subscribeChannelEvents}
                  getChatState={getChatState}
                  onChatStateUnmount={onChatStateUnmount}
                />
              }
            />
            {/* The frame lies over the view (z-index 21), so the logo on the
              seam goes over both. */}
            <div style={{ position: "absolute", inset: 0, zIndex: 22, pointerEvents: "none" }}>
              <SplitSeamLogo testId="plugin-agent-split-logo" running={agent.running} />
            </div>
          </>,
          overlayHost,
        )}
    </div>
  );
}
