import { useCallback, useEffect, useRef, useState } from "react";
import { contentCapBase } from "../api/api";
import { checkFilesExist } from "../api/files";
import type { PluginInfo } from "../api/plugins";
import { pluginXRequest, postPluginNotification } from "../api/plugins";
import type { FileLinkOpenDetail } from "../components/chat/FileLink";
import { useContentCapsEpoch } from "../hooks/useContentCapsEpoch";
import { useTheme } from "../ThemeContext";
import { writeClipboard } from "../utils/copyToClipboard";
import { BRIDGE_INIT, type Bridge, type BridgeHost, connectBridge, pluginTheme } from "./bridge";
import { cardHeight, cardInitialHeight } from "./cardHeight";
import { findPluginCard } from "./PluginPanelsContext";

export interface PluginCardProps {
  pluginId: string;
  cardId: string;
  /** Names the frame. */
  label: string;
  /** What the plugin posted the card with. */
  data: unknown;
  /** The channel or thread the card is about, its name and dir. */
  channelId: string;
  channelName: string;
  dirPath: string;
  /** The installed plugins as the channel's project sees them; null until
   *  they're listed. */
  plugins: PluginInfo[] | null;
}

/** A plugin's card in the notification drawer: its page from the plugin's
 *  bundle in a sandboxed frame, as a panel is, told through the bridge the
 *  data the plugin posted it with. It sizes itself with ui.resize. Its state
 *  lives as long as the frame does. */
export function PluginCard({ pluginId, cardId, label, data, channelId, channelName, dirPath, plugins }: PluginCardProps) {
  const { colors } = useTheme();
  const found = findPluginCard(plugins, pluginId, cardId);
  const entry = found?.card.entry ?? "";

  const capsEpoch = useContentCapsEpoch();
  const [src, setSrc] = useState("");
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (!entry) return;
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

  const [height, setHeight] = useState(cardInitialHeight);
  const state = useRef<unknown>(null);
  const hostRef = useRef<BridgeHost | null>(null);
  hostRef.current = found
    ? {
        pluginId,
        panel: "",
        card: { id: cardId, data },
        channelId,
        channelName,
        dirPath,
        capabilities: found.plugin.capabilities,
        request: (method, path, body) => pluginXRequest(pluginId, channelId, method, path, body),
        theme: () => pluginTheme(colors),
        settings: () => found.plugin.settings ?? {},
        notify: (t, body) => postPluginNotification(pluginId, channelId, t, body),
        openFile: async (path, line) => {
          const [file] = await checkFilesExist(channelId, [path]);
          if (!file?.exists || file.root_index === undefined || file.rel_path === undefined) throw new Error(`no file "${path}" in the channel`);
          const detail: FileLinkOpenDetail = { channelId, target: { rootIndex: file.root_index, relPath: file.rel_path }, line: line ?? null };
          window.dispatchEvent(new CustomEvent<FileLinkOpenDetail>("loop:open-file", { detail }));
        },
        copy: (text) => writeClipboard(text),
        loadState: () => state.current,
        saveState: (value) => {
          state.current = value;
        },
        resize: (h) => setHeight(cardHeight(h)),
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
    const port = new MessageChannel();
    bridgeRef.current = connectBridge(port.port1, () => hostRef.current ?? host);
    // The frame's origin is opaque, so it can only be targeted with "*".
    frame.postMessage({ type: BRIDGE_INIT }, "*", [port.port2]);
  }, []);
  useEffect(() => () => bridgeRef.current?.close(), []);
  useEffect(() => {
    bridgeRef.current?.notify("theme", pluginTheme(colors));
  }, [colors]);

  const message = plugins === null ? null : !found ? `This ${pluginId} card isn't available: the plugin may have been removed, not approved for cards or not enabled here.` : error;
  if (message === null && !src) return null;
  return (
    <div
      data-testid="plugin-card"
      data-plugin={pluginId}
      data-card={cardId}
      style={{ marginTop: 8, border: `1px solid ${colors.border}`, borderRadius: 6, overflow: "hidden", backgroundColor: colors.surface }}
    >
      {message !== null ? (
        <div data-testid="plugin-card-message" style={{ padding: 10, fontSize: 12, color: colors.textDim }}>
          {message}
        </div>
      ) : (
        <iframe
          ref={frameRef}
          data-testid="plugin-card-frame"
          title={label}
          src={src}
          sandbox="allow-scripts allow-forms"
          onLoad={handleLoad}
          style={{ display: "block", width: "100%", height, border: "none", backgroundColor: colors.bg }}
        />
      )}
    </div>
  );
}
