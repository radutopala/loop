import { createContext, useContext, useState, useEffect, type ReactNode } from "react";
import { builtinThemes, darkColors, type ColorPalette } from "./theme";

export interface FontSizes {
  sidebar: number;
  chat: number;
  terminal: number;
  editor: number;
  panels: number;
}

export const DEFAULT_FONT_SIZES: FontSizes = {
  sidebar: 12,
  chat: 13,
  terminal: 13,
  editor: 13,
  panels: 12,
};

interface ThemeContextValue {
  themeName: string;
  colors: ColorPalette;
  setThemeName: (name: string) => void;
  availableThemes: string[];
  fontSizes: FontSizes;
  setFontSizes: (sizes: FontSizes) => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  themeName: "dark",
  colors: darkColors,
  setThemeName: () => {},
  availableThemes: ["dark", "light"],
  fontSizes: DEFAULT_FONT_SIZES,
  setFontSizes: () => {},
});

export function ThemeProvider({ children, initialTheme, initialFontSizes, customThemes }: {
  children: ReactNode;
  initialTheme?: string;
  initialFontSizes?: Partial<FontSizes>;
  customThemes?: Record<string, ColorPalette>;
}) {
  const allThemes = { ...builtinThemes, ...customThemes };
  const availableThemes = Object.keys(allThemes);

  const [themeName, setThemeNameState] = useState<string>(
    initialTheme && allThemes[initialTheme] ? initialTheme : "dark"
  );
  const colors = allThemes[themeName] ?? darkColors;

  const [fontSizes, setFontSizes] = useState<FontSizes>({ ...DEFAULT_FONT_SIZES, ...initialFontSizes });

  // Wrap setThemeName to also persist to localStorage
  const setThemeName = (name: string) => {
    setThemeNameState(name);
    try { localStorage.setItem("loop-theme", name); } catch { /* ignore */ }
  };

  // On initial mount, persist the initial theme to localStorage
  useEffect(() => {
    try { localStorage.setItem("loop-theme", themeName); } catch { /* ignore */ }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Update scrollbar & body background via injected <style>, update meta theme-color
  useEffect(() => {
    // Remove the initial scrollbar style injected by index.html (one-time cleanup)
    const initialStyle = document.getElementById("loop-initial-scrollbar");
    if (initialStyle) initialStyle.remove();

    const id = "loop-theme-globals";
    let el = document.getElementById(id) as HTMLStyleElement | null;
    if (!el) {
      el = document.createElement("style");
      el.id = id;
      document.head.appendChild(el);
    }
    el.textContent = `
      ::-webkit-scrollbar-thumb { background: ${colors.scrollThumb} !important; }
      ::-webkit-scrollbar-thumb:hover { background: ${colors.scrollThumbHover} !important; }
      body { background-color: ${colors.bg}; }
    `;

    // Update color-scheme on <html>
    document.documentElement.style.colorScheme = colors.isDark ? "dark" : "light";
    document.documentElement.style.backgroundColor = colors.bg;

    // Update <meta name="theme-color"> for browser/OS chrome
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute("content", colors.bg);
  }, [themeName, colors]);

  return (
    <ThemeContext.Provider value={{ themeName, colors, setThemeName, availableThemes, fontSizes, setFontSizes }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  return useContext(ThemeContext);
}
