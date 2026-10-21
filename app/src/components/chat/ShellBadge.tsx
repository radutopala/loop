import { createPortal } from "react-dom";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { usePaneHeaderSlot } from "./LearnBadge";
import { ShellIcon } from "./ShellIcon";

interface ShellBadgeProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  /** The chat's container is running, so there's one to open a shell in. */
  running: boolean;
  /** This badge sits by the open Shell view. */
  open: boolean;
  onToggle: () => void;
}

/**
 * The chat pane's Shell label, at the right of its header after the Explain
 * and Learn ones, while the chat's container runs. It opens the Shell view, a shell in
 * that container beside the chat (open, it closes it).
 */
export function ShellBadge({ leafId, running, open, onToggle }: ShellBadgeProps) {
  const { colors } = useTheme();
  const slot = usePaneHeaderSlot(leafId);
  if (!slot || (!running && !open)) return null;
  return createPortal(
    <button
      data-testid="shell-badge"
      onClick={onToggle}
      title={open ? "Close the Shell view" : "Open a shell in the chat's container"}
      aria-expanded={open}
      style={{
        display: "flex",
        alignItems: "center",
        flexShrink: 0,
        gap: 4,
        height: 16,
        boxSizing: "border-box",
        whiteSpace: "nowrap",
        // After the Explain (1) and Learn (2) badges, whichever mounts first.
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
      <ShellIcon size={9} />
      shell
    </button>,
    slot,
  );
}
