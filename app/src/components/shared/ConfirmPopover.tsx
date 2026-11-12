import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

/**
 * ConfirmPopover is a small Yes/No bubble below its positioned parent, with
 * an arrow pointing up at it: the confirmation for a destructive click, such
 * as deleting a layout tab or a channel. It centers on the parent, or with
 * align "start" lines up with its left edge (for a parent near the window's
 * left, such as a context menu item).
 */
export function ConfirmPopover({ label = "Delete?", onYes, onNo, testId, align = "center" }: { label?: string; onYes: () => void; onNo: () => void; testId?: string; align?: "center" | "start" }) {
  const start = align === "start";
  const { colors } = useTheme();
  const smallButton = { cursor: "pointer", padding: "1px 6px", fontSize: 9, fontFamily: fonts.sans, borderRadius: 4, lineHeight: 1.4 };
  return (
    <div
      data-confirm-popover
      data-testid={testId}
      onMouseDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
      style={{
        position: "absolute",
        top: "100%",
        left: start ? 4 : "50%",
        transform: start ? undefined : "translateX(-50%)",
        marginTop: start ? 6 : 10,
        backgroundColor: colors.surface,
        border: `1px solid ${colors.textLight}`,
        borderRadius: 6,
        padding: "0 8px",
        height: 22,
        boxSizing: "border-box",
        zIndex: 1000,
        boxShadow: `0 4px 12px ${colors.shadow}`,
        display: "flex",
        alignItems: "center",
        gap: 6,
        whiteSpace: "nowrap",
        fontFamily: fonts.sans,
        fontSize: 9,
      }}
    >
      {/* Arrow pointing up */}
      <svg
        width="16"
        height="9"
        viewBox="0 0 16 9"
        style={{ position: "absolute", top: -8, left: start ? 12 : "50%", transform: start ? undefined : "translateX(-50%)", filter: "drop-shadow(0 -2px 4px rgba(0,0,0,0.3))" }}
      >
        <path d="M1 9 L7 2.5 Q8 1.5 9 2.5 L15 9 Z" fill={colors.surface} stroke={colors.textLight} strokeWidth="0.75" />
        <rect x="0" y="8" width="16" height="2" fill={colors.surface} />
      </svg>
      <span style={{ color: colors.textLight }}>{label}</span>
      <button
        data-testid="confirm-yes"
        onClick={(e) => {
          e.stopPropagation();
          onYes();
        }}
        style={{ ...smallButton, background: colors.dangerBg, border: `1px solid ${colors.dangerText}`, color: colors.dangerText }}
        onMouseEnter={(e) => {
          e.currentTarget.style.background = colors.dangerHoverBg;
          e.currentTarget.style.color = colors.white;
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.background = colors.dangerBg;
          e.currentTarget.style.color = colors.dangerText;
        }}
      >
        Yes
      </button>
      <button
        data-testid="confirm-no"
        onClick={(e) => {
          e.stopPropagation();
          onNo();
        }}
        style={{ ...smallButton, background: "none", border: `1px solid ${colors.border}`, color: colors.textDim }}
        onMouseEnter={(e) => {
          e.currentTarget.style.color = colors.textLight;
          e.currentTarget.style.borderColor = colors.textDim;
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.color = colors.textDim;
          e.currentTarget.style.borderColor = colors.border;
        }}
      >
        No
      </button>
    </div>
  );
}
