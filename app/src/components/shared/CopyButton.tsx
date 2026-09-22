import { useCallback, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { writeClipboard } from "../../utils/copyToClipboard";

/**
 * Small square-on-square copy button. Meant to sit inside a hover container
 * (`visible` follows the container's hover state) — it fades in, copies `text`
 * to the clipboard, and briefly flips to a check.
 *
 * Pass `html` to offer the copy as rich text too, so a paste target that
 * rebuilds tables (Slack, a spreadsheet, a doc) gets one. `icon` and `title`
 * distinguish buttons where one element offers more than one kind of copy.
 */
export function CopyButton({ text, html, icon, title, visible, style }: { text: string; html?: string; icon?: React.ReactNode; title?: string; visible: boolean; style?: React.CSSProperties }) {
  const { colors } = useTheme();
  const [copied, setCopied] = useState(false);
  const copy = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      e.preventDefault();
      writeClipboard(text, html)
        .then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 1200);
        })
        .catch(() => {
          /* clipboard blocked — no-op */
        });
    },
    [text, html],
  );
  return (
    <button
      onClick={copy}
      title={copied ? "Copied" : (title ?? "Copy")}
      aria-label={title ?? "Copy to clipboard"}
      style={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        width: 20,
        height: 20,
        padding: 0,
        flexShrink: 0,
        background: "none",
        border: "none",
        borderRadius: 4,
        cursor: "pointer",
        color: copied ? colors.active : colors.textDim,
        opacity: visible || copied ? 1 : 0,
        transition: "opacity 0.12s ease",
        WebkitAppRegion: "no-drag",
        ...style,
      }}
      onMouseEnter={(e) => {
        if (!copied) e.currentTarget.style.color = colors.textLight;
      }}
      onMouseLeave={(e) => {
        if (!copied) e.currentTarget.style.color = colors.textDim;
      }}
    >
      {copied ? (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="20 6 9 17 4 12" />
        </svg>
      ) : (
        (icon ?? (
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <rect x="9" y="9" width="13" height="13" rx="2" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
          </svg>
        ))
      )}
    </button>
  );
}
