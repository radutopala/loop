// Shared color palette and typography constants.

export const colors = {
  bg: "#212121",
  surface: "#2a2a2a",
  sidebar: "#171717",
  border: "#333333",
  selectedBg: "#333333",
  hoverBg: "#2a2a2a",
  inputBorder: "#444444",

  text: "#d4d4d4",
  textLight: "#ececec",
  textMuted: "#999999",
  textDim: "#717171",
  textDisabled: "#555555",

  cursor: "#d4d4d4",
  active: "#22c55e",
  error: "#ef4444",
  warning: "#f59e0b",
  white: "#fff",
} as const;

export const fonts = {
  sans: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
  mono: "'SF Mono', Menlo, Monaco, 'Courier New', monospace",
} as const;
