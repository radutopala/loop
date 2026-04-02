// Shared color palette and typography constants.

export interface ColorPalette {
  bg: string;
  surface: string;
  sidebar: string;
  sidebarNav: string;
  border: string;
  selectedBg: string;
  hoverBg: string;
  inputBorder: string;
  text: string;
  textLight: string;
  textMuted: string;
  textDim: string;
  textDisabled: string;
  cursor: string;
  active: string;
  error: string;
  warning: string;
  white: string;
  // Semantic tokens (previously hardcoded):
  userBubble: string;
  codeBg: string;
  codeBlockBg: string;
  scrollThumb: string;
  scrollThumbHover: string;
  overlay: string;
  shadow: string;
  // Error banner
  errorBannerBg: string;
  errorBannerText: string;
  // Danger action (delete confirm, context menu)
  dangerText: string;
  dangerBg: string;
  dangerHoverBg: string;
  // Pill / segmented control active state & send button
  pillActiveBg: string;
  pillActiveText: string;
  // Hover alpha for interactive surfaces
  hoverAlpha: string;
  // Pane header label background
  panelLabelBg: string;
  // Editor directory selected highlight
  dirSelectedBg: string;
  // Diff line colors
  diffAddText: string;
  diffDelText: string;
  diffAddBg: string;
  diffAddNumBg: string;
  diffDelBg: string;
  diffDelNumBg: string;
  // Hunk header bg
  diffHunkBg: string;
  isDark: boolean;
  // Island layout tokens
  canvas: string;
  islandRadius: number;
  islandGap: number;
  islandShadow: string;
  islandBorder: string;
}

export const darkColors: ColorPalette = {
  bg: "#212121",
  surface: "#2a2a2a",
  sidebar: "#171717",
  sidebarNav: "#2a2a2a",
  border: "#333333",
  selectedBg: "#333333",
  hoverBg: "#363636",
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
  userBubble: "#2f2f2f",
  codeBg: "rgba(255,255,255,0.06)",
  codeBlockBg: "rgba(0,0,0,0.3)",
  scrollThumb: "rgba(255,255,255,0.15)",
  scrollThumbHover: "rgba(255,255,255,0.25)",
  overlay: "rgba(0, 0, 0, 0.5)",
  shadow: "rgba(0, 0, 0, 0.4)",
  errorBannerBg: "#3b1616",
  errorBannerText: "#fca5a5",
  dangerText: "#f47067",
  dangerBg: "rgba(218, 55, 60, 0.2)",
  dangerHoverBg: "#f47067",
  pillActiveBg: "#fff",
  pillActiveText: "#000",
  hoverAlpha: "rgba(255,255,255,0.08)",
  panelLabelBg: "rgba(255,255,255,0.05)",
  dirSelectedBg: "rgba(78, 154, 106, 0.15)",
  diffAddText: "#86efac",
  diffDelText: "#fca5a5",
  diffAddBg: "rgba(34, 197, 94, 0.12)",
  diffAddNumBg: "rgba(34, 197, 94, 0.2)",
  diffDelBg: "rgba(239, 68, 68, 0.12)",
  diffDelNumBg: "rgba(239, 68, 68, 0.2)",
  diffHunkBg: "rgba(100, 100, 100, 0.1)",
  isDark: true,
  canvas: "#141414",
  islandRadius: 10,
  islandGap: 6,
  islandShadow: "0 2px 8px rgba(0, 0, 0, 0.4), inset 0 1px 0 rgba(255, 255, 255, 0.04)",
  islandBorder: "1px solid rgba(255, 255, 255, 0.06)",
};

export const lightColors: ColorPalette = {
  bg: "#ffffff",
  surface: "#f5f5f5",
  sidebar: "#f0f0f0",
  sidebarNav: "#f0f0f0",
  border: "#e0e0e0",
  selectedBg: "#e8e8e8",
  hoverBg: "#f5f5f5",
  inputBorder: "#cccccc",
  text: "#1e1e1e",
  textLight: "#111111",
  textMuted: "#666666",
  textDim: "#888888",
  textDisabled: "#aaaaaa",
  cursor: "#1e1e1e",
  active: "#16a34a",
  error: "#dc2626",
  warning: "#d97706",
  white: "#fff",
  userBubble: "#e8e8e8",
  codeBg: "rgba(0,0,0,0.05)",
  codeBlockBg: "rgba(0,0,0,0.04)",
  scrollThumb: "rgba(0,0,0,0.15)",
  scrollThumbHover: "rgba(0,0,0,0.25)",
  overlay: "rgba(0, 0, 0, 0.3)",
  shadow: "rgba(0, 0, 0, 0.15)",
  errorBannerBg: "#fef2f2",
  errorBannerText: "#b91c1c",
  dangerText: "#dc2626",
  dangerBg: "rgba(220, 38, 38, 0.1)",
  dangerHoverBg: "#dc2626",
  pillActiveBg: "#1e1e1e",
  pillActiveText: "#fff",
  hoverAlpha: "rgba(0,0,0,0.06)",
  panelLabelBg: "rgba(0,0,0,0.04)",
  dirSelectedBg: "rgba(22, 163, 74, 0.1)",
  diffAddText: "#166534",
  diffDelText: "#991b1b",
  diffAddBg: "rgba(34, 197, 94, 0.08)",
  diffAddNumBg: "rgba(34, 197, 94, 0.15)",
  diffDelBg: "rgba(239, 68, 68, 0.08)",
  diffDelNumBg: "rgba(239, 68, 68, 0.15)",
  diffHunkBg: "rgba(0, 0, 0, 0.04)",
  isDark: false,
  canvas: "#e8e8e8",
  islandRadius: 10,
  islandGap: 6,
  islandShadow: "0 1px 4px rgba(0, 0, 0, 0.08)",
  islandBorder: "1px solid rgba(0, 0, 0, 0.06)",
};

// Claude for Desktop inspired warm palette
export const claudeColors: ColorPalette = {
  bg: "#FAF6F1",
  surface: "#F0EBE4",
  sidebar: "#EDE7DF",
  sidebarNav: "#E5DED5",
  border: "#D9D0C7",
  selectedBg: "#E4DDD4",
  hoverBg: "#F0EBE4",
  inputBorder: "#C5BAB0",
  text: "#2D2B28",
  textLight: "#1A1816",
  textMuted: "#6B6560",
  textDim: "#8C8580",
  textDisabled: "#AEA8A2",
  cursor: "#2D2B28",
  active: "#D97757",
  error: "#C4362C",
  warning: "#C27818",
  white: "#fff",
  userBubble: "#E8E1D8",
  codeBg: "rgba(0,0,0,0.04)",
  codeBlockBg: "rgba(0,0,0,0.03)",
  scrollThumb: "rgba(0,0,0,0.12)",
  scrollThumbHover: "rgba(0,0,0,0.22)",
  overlay: "rgba(0, 0, 0, 0.25)",
  shadow: "rgba(0, 0, 0, 0.12)",
  errorBannerBg: "#FDF0EF",
  errorBannerText: "#A52E25",
  dangerText: "#C4362C",
  dangerBg: "rgba(196, 54, 44, 0.1)",
  dangerHoverBg: "#C4362C",
  pillActiveBg: "#D97757",
  pillActiveText: "#fff",
  hoverAlpha: "rgba(0,0,0,0.05)",
  panelLabelBg: "rgba(0,0,0,0.03)",
  dirSelectedBg: "rgba(217, 119, 87, 0.12)",
  diffAddText: "#1A6B3C",
  diffDelText: "#A52E25",
  diffAddBg: "rgba(34, 197, 94, 0.08)",
  diffAddNumBg: "rgba(34, 197, 94, 0.14)",
  diffDelBg: "rgba(196, 54, 44, 0.08)",
  diffDelNumBg: "rgba(196, 54, 44, 0.14)",
  diffHunkBg: "rgba(0, 0, 0, 0.03)",
  isDark: false,
  canvas: "#E5DED5",
  islandRadius: 10,
  islandGap: 6,
  islandShadow: "0 1px 4px rgba(0, 0, 0, 0.06)",
  islandBorder: "1px solid rgba(0, 0, 0, 0.05)",
};

// Built-in theme registry — extensible at runtime with custom themes
export const builtinThemes: Record<string, ColorPalette> = {
  dark: darkColors,
  light: lightColors,
  claude: claudeColors,
};

// Backward compat: keep `colors` as default export for gradual migration
export const colors = darkColors;

export const fonts = {
  sans: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
  mono: "'SF Mono', Menlo, Monaco, 'Courier New', monospace",
} as const;
