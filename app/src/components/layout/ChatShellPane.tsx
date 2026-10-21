import { useState } from "react";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { ShellIcon } from "../chat/ShellIcon";
import { Terminal } from "../panels/Terminal";

// The shell the Shell view opens in the chat's container.
const CHAT_SHELL_CMD = ["/bin/bash"];

/**
 * A shell in the container the channel's chat runs in, as a pane beside its
 * chat in the Shell view (the Learn view's, see LearnSplit): what the chat's
 * agent sees, its files and processes, from beside it. Closing the view
 * leaves the shell running to come back to; Stop ends it, never the
 * container. uiPane names it to the UI bridge (see Terminal).
 *
 * While the container isn't running there's nothing to exec into: the pane
 * says so, and the shell starts once it runs. Started, it stays, so a missed
 * stats poll doesn't end it; a container that stops ends the shell.
 */
export function ChatShellPane({ channelId, running, uiPane, onClose }: { channelId: string; running: boolean; uiPane?: string; onClose: () => void }) {
  const { colors } = useTheme();
  const [started, setStarted] = useState(running);
  if (running && !started) setStarted(true);
  return (
    <div data-testid="chat-shell-pane" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden", fontFamily: fonts.sans }}>
      {/* Header, like the Learn pane's; docked on a canvas, it drags the pair. */}
      <div
        data-learn-pane-header
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "2px 8px",
          height: 22,
          boxSizing: "border-box",
          backgroundColor: colors.surface,
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
        }}
      >
        <span
          style={{ display: "flex", alignItems: "center", gap: 4, padding: "1px 4px", borderRadius: 3, fontSize: 10, fontWeight: 500, color: colors.textLight, backgroundColor: colors.panelLabelBg }}
        >
          <ShellIcon size={9} />
          Shell
        </span>
        <span style={{ color: colors.textDim, fontSize: 10, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>in the chat's container</span>
        <div style={{ flex: 1 }} />
        <button
          data-testid="chat-shell-close"
          aria-label="Close the Shell view"
          onClick={onClose}
          title="Close the Shell view"
          style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: "0 2px", lineHeight: 1, display: "flex", alignItems: "center", borderRadius: 2 }}
          onMouseEnter={(e) => {
            e.currentTarget.style.backgroundColor = colors.hoverBg;
            e.currentTarget.style.color = colors.textLight;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.backgroundColor = "transparent";
            e.currentTarget.style.color = colors.textDim;
          }}
        >
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>
      {started ? (
        <Terminal channelId={channelId} target="chat" cmd={CHAT_SHELL_CMD} uiPane={uiPane} />
      ) : (
        <div data-testid="chat-shell-idle" style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", padding: 24, textAlign: "center", fontSize: 12, color: colors.textDim }}>
          The chat's container isn't running. The shell opens here once it starts, with the chat's next turn.
        </div>
      )}
    </div>
  );
}
