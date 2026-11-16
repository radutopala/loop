import { createPortal } from "react-dom";
import type { PromptEditsView } from "../../hooks/usePromptEdits";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { EditVoiceIcon } from "./DictationControls";
import { usePaneHeaderSlot } from "./LearnBadge";

interface PromptEditsBadgeProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  edits: PromptEditsView;
  /** This badge sits by the open Edits view. */
  open: boolean;
  onToggle: () => void;
}

/**
 * The chat pane's Edits label, at the right of its header after the Learn
 * one, once the channel has edits by voice to look back at. It opens the
 * Edits view, the chat of the channel's prompt-edit thread beside its own
 * (open, it closes it).
 */
export function PromptEditsBadge({ leafId, edits, open, onToggle }: PromptEditsBadgeProps) {
  const { colors } = useTheme();
  const slot = usePaneHeaderSlot(leafId);
  if (!slot || (!edits.editChannelId && !open)) return null;
  return createPortal(
    <button
      data-testid="edits-badge"
      onClick={onToggle}
      title={open ? "Close the Edits view" : "Open the Edits view: the chat of the edits by voice"}
      aria-expanded={open}
      style={{
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
        gap: 4,
        height: 16,
        boxSizing: "border-box",
        whiteSpace: "nowrap",
        // After the Explain (1) and Learn (2) badges, before the Shell one (4).
        order: 3,
        background: open ? colors.hoverBg : "transparent",
        border: `1px solid ${colors.border}`,
        color: colors.textDim,
        cursor: "pointer",
        padding: "0 6px",
        fontSize: 10,
        fontFamily: fonts.mono,
        lineHeight: 1,
        borderRadius: 8,
      }}
    >
      <EditVoiceIcon size={9} />
      edits
    </button>,
    slot,
  );
}
