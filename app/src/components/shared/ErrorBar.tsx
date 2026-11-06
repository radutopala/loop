import { useTheme } from "../../ThemeContext";

/** A dismissible error across the top of a view. */
export function ErrorBar({ message, onDismiss }: { message: string; onDismiss: () => void }) {
  const { colors } = useTheme();
  return (
    <div
      role="alert"
      style={{
        padding: "8px 12px",
        backgroundColor: colors.errorBannerBg,
        color: colors.errorBannerText,
        fontSize: "13px",
        display: "flex",
        justifyContent: "space-between",
        alignItems: "center",
      }}
    >
      <span>{message}</span>
      <button
        onClick={onDismiss}
        style={{
          background: "none",
          border: "none",
          color: colors.errorBannerText,
          cursor: "pointer",
          fontSize: "16px",
        }}
      >
        &times;
      </button>
    </div>
  );
}
