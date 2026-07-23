import { useCallback, useEffect, useState } from "react";
import { fetchPlaygroundShares, type PlaygroundShare, unsharePlayground } from "../../api/loopApi";
import { useEventStream } from "../../hooks/useEventStream";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import type { Channel, WSEvent } from "../../types";
import { logErr } from "../../utils/log";
import { openExternalUrl } from "../../utils/openExternal";
import { ChannelHeaderInfo } from "../layout/ChannelHeaderInfo";

interface GlobalSharesPanelProps {
  channel?: Channel;
  sidebarOpen?: boolean;
  onOpenPalette?: () => void;
  onClose: () => void;
}

/**
 * GlobalSharesPanel lists every active public playground share across the
 * daemon and lets the user revoke any of them. Backed by GET/DELETE
 * /api/playground/share; live-updated via playground.update (kind=share).
 */
export function GlobalSharesPanel({ channel, sidebarOpen, onOpenPalette, onClose }: GlobalSharesPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [shares, setShares] = useState<PlaygroundShare[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  const load = useCallback(() => {
    fetchPlaygroundShares().then(setShares).catch(logErr("fetching playground shares"));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // Refresh whenever a share is added/removed anywhere. Global events bypass
  // channel filtering server-side, so any subscribed channel receives them.
  useEventStream({
    channelId: channel?.id ?? null,
    onEvent: useCallback(
      (event: WSEvent) => {
        if (event.type === "playground.update") {
          const data = event.data as { kind?: string } | undefined;
          if (data?.kind === "share") load();
        }
      },
      [load],
    ),
  });

  async function handleUnshare(sh: PlaygroundShare) {
    const key = `${sh.scope}:${sh.channel_id}:${sh.name}`;
    setBusy(key);
    try {
      await unsharePlayground(sh.name, sh.scope, sh.channel_id);
      setShares((prev) => prev.filter((s) => !(s.name === sh.name && s.scope === sh.scope && s.channel_id === sh.channel_id)));
    } catch (e) {
      logErr("unsharing playground")(e);
    } finally {
      setBusy(null);
    }
  }

  function handleCopy(url: string) {
    navigator.clipboard.writeText(url).then(() => {
      setCopied(url);
      setTimeout(() => setCopied(null), 1500);
    }, logErr("copying share url"));
  }

  return (
    <div
      data-testid="global-shares-panel"
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
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "⌘K" : "Ctrl+K"}</span>
          </button>
        )}
        {channel && <ChannelHeaderInfo channel={channel} colors={colors} />}
        <div style={{ flex: 1 }} />
      </div>

      {/* Header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "8px 14px",
          borderBottom: `1px solid ${colors.border}`,
        }}
      >
        <span style={{ fontSize: 13, fontWeight: 600, color: colors.text }}>
          Playground Shares {shares.length > 0 && <span style={{ color: colors.textDim, fontWeight: 400 }}>({shares.length})</span>}
        </span>
        <button onClick={onClose} title="Close" style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", fontSize: 16, lineHeight: 1 }}>
          ×
        </button>
      </div>

      {/* List */}
      <div style={{ flex: 1, overflowY: "auto", padding: 8 }}>
        {shares.length === 0 ? (
          <div style={{ color: colors.textDim, fontSize: 12, padding: 16, textAlign: "center" }}>
            No playgrounds are shared publicly. Open a playground and click Share to expose it over the internet.
          </div>
        ) : (
          shares.map((sh) => {
            const key = `${sh.scope}:${sh.channel_id}:${sh.name}`;
            return (
              <div
                key={key}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  padding: "8px 10px",
                  borderRadius: 6,
                  border: `1px solid ${colors.border}`,
                  marginBottom: 6,
                  fontSize: 12,
                }}
              >
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ color: colors.text, fontWeight: 600 }}>
                    {sh.name}
                    <span style={{ color: colors.textDim, fontWeight: 400, marginLeft: 6, fontSize: 11 }}>{sh.scope}</span>
                  </div>
                  <div
                    onClick={() => openExternalUrl(sh.url)}
                    title="Open in external browser"
                    style={{
                      color: colors.active,
                      cursor: "pointer",
                      textDecoration: "underline",
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                      fontSize: 11,
                    }}
                  >
                    {sh.url}
                  </div>
                </div>
                <button
                  onClick={() => handleCopy(sh.url)}
                  title="Copy share URL"
                  style={{
                    background: "none",
                    color: colors.textDim,
                    border: `1px solid ${colors.border}`,
                    borderRadius: 4,
                    padding: "4px 10px",
                    cursor: "pointer",
                    fontSize: 11,
                    flexShrink: 0,
                  }}
                >
                  {copied === sh.url ? "copied!" : "Copy"}
                </button>
                <button
                  onClick={() => handleUnshare(sh)}
                  disabled={busy === key}
                  style={{
                    background: colors.dangerBg,
                    color: colors.dangerText,
                    border: "none",
                    borderRadius: 4,
                    padding: "4px 10px",
                    cursor: busy === key ? "default" : "pointer",
                    fontSize: 11,
                    flexShrink: 0,
                  }}
                >
                  {busy === key ? "…" : "Disable"}
                </button>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
