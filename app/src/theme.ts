// Shared color palette and typography constants.

export const colors = {
  bg: "#1a1b26",
  surface: "#1e1e2e",
  sidebar: "#161622",
  border: "#2d2d2d",
  selectedBg: "#2d2d4d",
  inputBorder: "#3d3d5d",

  text: "#a9b1d6",
  textLight: "#e2e8f0",
  textMuted: "#9ca3af",
  textDim: "#6b7280",
  textDisabled: "#4b5563",

  cursor: "#c0caf5",
  active: "#22c55e",
  error: "#ef4444",
  warning: "#f59e0b",
  white: "#fff",
} as const;

export const fonts = {
  sans: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
  mono: "'SF Mono', Menlo, Monaco, 'Courier New', monospace",
} as const;
