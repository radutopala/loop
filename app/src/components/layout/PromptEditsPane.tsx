import type { RootEntry } from "../../api/files";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import type { PromptEditsView } from "../../hooks/usePromptEdits";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { HiddenThreadChat } from "./LearnPane";

interface PromptEditsPaneProps {
  /** The workspace whose side view this is: UI commands reach the
   *  prompt-edit thread's chat as the pane "side". */
  uiChannelId?: string;
  edits: PromptEditsView;
  roots?: RootEntry[];
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
  /** The thread's chat state in the app-level store, restored on opening
   *  the view and saved back on closing it. */
  getChatState?: (channelId: string) => ActiveChatState | undefined;
  onChatStateUnmount?: (channelId: string, state: ActiveChatState) => void;
  onClose: () => void;
}

/**
 * The channel's edits by voice, as a pane beside its chat in the Edits view
 * (the Learn view's, see LearnSplit): the hidden prompt-edit thread's chat,
 * each edit's draft and instruction, the run's tool calls and the rewrite it
 * replied with. Nobody writes there: the composer's voice edit does.
 */
export function PromptEditsPane({ uiChannelId, edits, roots, subscribeChannelEvents, getChatState, onChatStateUnmount, onClose }: PromptEditsPaneProps) {
  const { colors } = useTheme();
  return (
    <div data-testid="edits-pane" style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column", overflow: "hidden" }}>
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
          fontFamily: fonts.sans,
          flexShrink: 0,
        }}
      >
        <span style={{ padding: "1px 4px", borderRadius: 3, fontSize: 10, fontWeight: 500, color: colors.textLight, backgroundColor: colors.panelLabelBg }}>Edits</span>
        <span style={{ color: colors.textDim, fontSize: 10, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
          {edits.running ? "editing the draft…" : "the composer's edits by voice"}
        </span>
        <div style={{ flex: 1 }} />
        <button
          data-testid="edits-close"
          aria-label="Close the Edits view"
          onClick={onClose}
          title="Close the Edits view"
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
      <div data-edits-thread style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        {edits.editChannelId ? (
          <HiddenThreadChat
            key={edits.editChannelId}
            uiChannelId={uiChannelId}
            threadId={edits.editChannelId}
            running={edits.running}
            roots={roots}
            subscribeChannelEvents={subscribeChannelEvents}
            getChatState={getChatState}
            onChatStateUnmount={onChatStateUnmount}
            readOnlyNote="Edits by voice run here: hold Shift with the right Option/Alt key in the composer, or use its pencil button."
          />
        ) : (
          <div data-testid="edits-empty" style={{ padding: 16, color: colors.textDim, fontFamily: fonts.sans, fontSize: 12, lineHeight: 1.5 }}>
            No edit by voice yet. Hold Shift with the right Option/Alt key in the composer, or use its pencil button, and say how to change the draft.
          </div>
        )}
      </div>
    </div>
  );
}
