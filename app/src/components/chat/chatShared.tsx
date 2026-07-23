import { createContext } from "react";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import { findCandidatePaths } from "../../utils/fileLinks";
import { FileLink } from "./FileLink";

// ChannelContext lets nested renderers (MarkdownContent, ToolActivityIndicator)
// resolve the current channel without prop drilling through every helper.
export const ChannelContext = createContext<string>("");

export function buildMessageStyles(colors: ColorPalette): Record<string, React.CSSProperties> {
  return {
    messages: {
      flex: 1,
      overflowY: "auto",
      padding: "16px 24px",
      // Reserve scrollbar gutter on BOTH sides so the centered children
      // (messageColumn and the sticky TriggerQuote) stay on the same
      // vertical axis as the chat input below — which sits in a sibling
      // container with no scrollbar. Without this, the 8px webkit
      // scrollbar consumes inline-end width only, shifting our centered
      // content ~4px left of the input.
      scrollbarGutter: "stable both-edges",
    },
    messageColumn: {
      maxWidth: 768,
      margin: "0 auto",
      // Mirror the chat input's internal text gutter (left 18 / right 14
      // from ChatInput's inputWrapper padding) so message text starts and
      // ends at the same x as the textarea's text — without this the
      // column begins flush with the input's outer border and the visible
      // text edges drift 18px apart.
      padding: "0 14px 0 18px",
    },
    loadMore: {
      display: "block",
      margin: "0 auto 16px",
      padding: "4px 12px",
      background: "none",
      border: `1px solid ${colors.border}`,
      borderRadius: 4,
      color: colors.textMuted,
      cursor: "pointer",
      fontFamily: fonts.sans,
      fontSize: 12,
    },
    bubble: {},
    header: {
      display: "flex",
      alignItems: "center",
      gap: 8,
      marginBottom: 4,
    },
    author: {
      fontWeight: 600,
      fontSize: 13,
    },
    time: {
      fontSize: 11,
      color: colors.textDim,
    },
    content: {
      fontSize: 14,
      lineHeight: 1.6,
      color: colors.text,
      wordBreak: "break-word" as const,
    },
    paragraph: {
      margin: "2px 0",
    },
    codeBlock: {
      backgroundColor: colors.surface,
      borderRadius: 8,
      padding: "10px 14px",
      margin: "8px 0",
      overflow: "auto",
      fontFamily: fonts.mono,
      fontSize: 13,
      lineHeight: 1.4,
      color: colors.textLight,
    },
    codeLang: {
      fontSize: 11,
      color: colors.textDim,
      marginBottom: 4,
    },
    inlineCode: {
      backgroundColor: colors.surface,
      borderRadius: 3,
      padding: "1px 5px",
      fontFamily: fonts.mono,
      fontSize: 13,
    },
    blockquote: {
      borderLeft: `3px solid ${colors.border}`,
      paddingLeft: 12,
      margin: "6px 0",
      color: colors.textMuted,
      fontSize: 13,
      lineHeight: 1.5,
    },
    table: {
      borderCollapse: "collapse" as const,
      margin: "8px 0",
      fontSize: 13,
      lineHeight: 1.4,
      display: "block",
      maxWidth: "100%",
      overflowX: "auto" as const,
    },
    tableHeaderCell: {
      border: `1px solid ${colors.border}`,
      padding: "6px 10px",
      backgroundColor: colors.surface,
      fontWeight: 600,
      textAlign: "left" as const,
      whiteSpace: "nowrap" as const,
    },
    tableCell: {
      border: `1px solid ${colors.border}`,
      padding: "6px 10px",
      verticalAlign: "top" as const,
    },
  };
}

export function buildActivityStyle(colors: ColorPalette): React.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    gap: 8,
    marginBottom: 8,
    padding: "4px 0",
    fontSize: 12,
    color: colors.textDim,
    fontFamily: fonts.mono,
  };
}

// Tools whose tool_input summary (from internal/container/runner.go's
// summarizeToolInput) is the bare file path string — render it as a single
// FileLink rather than scanning for path-shaped substrings.
export const FILE_PATH_TOOLS = new Set(["Read", "Edit", "Write", "MultiEdit", "NotebookEdit", "NotebookRead"]);

export function renderInputWithLinks(input: string, toolName: string, channelId: string): React.ReactNode {
  if (!input) return input;
  if (!channelId) return input;
  if (FILE_PATH_TOOLS.has(toolName)) {
    return <FileLink channelId={channelId} raw={input} line={null} />;
  }
  const candidates = findCandidatePaths(input);
  if (candidates.length === 0) return input;
  const parts: React.ReactNode[] = [];
  let last = 0;
  candidates.forEach((c, i) => {
    if (c.start > last) parts.push(input.slice(last, c.start));
    parts.push(<FileLink key={`tool-link-${i}`} channelId={channelId} raw={c.raw} line={c.line} />);
    last = c.start + c.length;
  });
  if (last < input.length) parts.push(input.slice(last));
  return <>{parts}</>;
}
