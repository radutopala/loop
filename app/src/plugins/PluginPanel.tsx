import { useCallback, useEffect, useRef, useState } from "react";
import { contentCapBase } from "../api/api";
import { checkFilesExist } from "../api/files";
import { pluginXRequest } from "../api/plugins";
import type { FileLinkOpenDetail } from "../components/chat/FileLink";
import { useContentCapsEpoch } from "../hooks/useContentCapsEpoch";
import { useEventStream } from "../hooks/useEventStream";
import { useTheme } from "../ThemeContext";
import type { WSEvent } from "../types";
import type { PluginPaneRef } from "../types/panels";
import { storageGetJSON, storageSetJSON } from "../utils/storage";
import { BRIDGE_INIT, type Bridge, type BridgeHost, connectBridge, pluginTheme } from "./bridge";
import { findPluginPanel, usePluginPanels } from "./PluginPanelsContext";

export interface PluginPanelProps {
  plugin: PluginPaneRef | undefined;
  /** The channel or thread the pane is in. */
  channelId: string;
  /** The channel whose plugin container serves the pane: a thread's parent. */
  containerChannelId: string;
  channelName: string;
  dirPath: string;
}

/** A plugin's panel: its bundle in a sandboxed frame, talking to the app
 *  only through the bridge. */
export function PluginPanel({ plugin: ref, channelId, containerChannelId, channelName, dirPath }: PluginPanelProps) {
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

  const handleEvent = useCallback(
    (ev: WSEvent) => {
      if (ev.type !== "plugin.event") return;
      const data = ev.data as { plugin_id?: string; type?: string; data?: unknown };
      if (data.plugin_id === pluginId) bridgeRef.current?.notify("event", { type: data.type, data: data.data });
    },
    [pluginId],
  );
  useEventStream({ channelId: pluginId ? containerChannelId : null, onEvent: handleEvent });

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
  return (
    <iframe
      ref={frameRef}
      data-testid="plugin-panel-frame"
      title={ref?.label ?? pluginId}
      src={src}
      sandbox="allow-scripts allow-forms"
      onLoad={handleLoad}
      style={{ flex: 1, border: "none", width: "100%", height: "100%", backgroundColor: colors.bg }}
    />
  );
}
