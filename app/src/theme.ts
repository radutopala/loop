// Shared color palette and typography constants.

export interface ColorPalette {
  bg: string;
  surface: string;
  sidebar: string;
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
  isDark: boolean;
}

export const darkColors: ColorPalette = {
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
  userBubble: "#2f2f2f",
  codeBg: "rgba(255,255,255,0.06)",
  codeBlockBg: "rgba(0,0,0,0.3)",
  scrollThumb: "rgba(255,255,255,0.15)",
  scrollThumbHover: "rgba(255,255,255,0.25)",
  overlay: "rgba(0, 0, 0, 0.5)",
  shadow: "rgba(0, 0, 0, 0.4)",
  isDark: true,
};

export const lightColors: ColorPalette = {
  bg: "#ffffff",
  surface: "#f5f5f5",
  sidebar: "#f0f0f0",
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
  isDark: false,
};

// Built-in theme registry — extensible at runtime with custom themes
export const builtinThemes: Record<string, ColorPalette> = {
  dark: darkColors,
  light: lightColors,
};

// Backward compat: keep `colors` as default export for gradual migration
export const colors = darkColors;

export const fonts = {
  sans: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
  mono: "'SF Mono', Menlo, Monaco, 'Courier New', monospace",
} as const;
