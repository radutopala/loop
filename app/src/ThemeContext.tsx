import { createContext, useContext, useState, useEffect, type ReactNode } from "react";
import { builtinThemes, darkColors, type ColorPalette } from "./theme";
import { storageSet } from "./utils/storage";

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
  islands: boolean;
  setIslands: (v: boolean) => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  themeName: "dark",
  colors: darkColors,
  setThemeName: () => {},
  availableThemes: ["dark", "light"],
  fontSizes: DEFAULT_FONT_SIZES,
  setFontSizes: () => {},
  islands: true,
  setIslands: () => {},
});

export function ThemeProvider({ children, initialTheme, initialFontSizes, initialIslands, customThemes }: {
  children: ReactNode;
  initialTheme?: string;
  initialFontSizes?: Partial<FontSizes>;
  initialIslands?: boolean;
  customThemes?: Record<string, ColorPalette>;
}) {
  const allThemes = { ...builtinThemes, ...customThemes };
  const availableThemes = Object.keys(allThemes);

  const [themeName, setThemeNameState] = useState<string>(
    initialTheme && allThemes[initialTheme] ? initialTheme : "dark"
  );
  const [fontSizes, setFontSizes] = useState<FontSizes>({ ...DEFAULT_FONT_SIZES, ...initialFontSizes });
  const [islands, setIslands] = useState(initialIslands ?? true);

  const baseColors = allThemes[themeName] ?? darkColors;
  const colors = islands ? baseColors : {
    ...baseColors,
    canvas: baseColors.bg,
    islandRadius: 0,
    islandGap: 0,
    islandShadow: "none",
    islandBorder: "none",
  };

  // Wrap setThemeName to also persist to localStorage
  const setThemeName = (name: string) => {
    setThemeNameState(name);
    storageSet("loop-theme", name);
  };

  // On initial mount, persist the initial theme + islands to localStorage
  useEffect(() => {
    storageSet("loop-theme", themeName);
    storageSet("loop-islands", islands ? "1" : "0");
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
      body { background-color: ${colors.canvas}; }
    `;

    // Update color-scheme on <html>
    document.documentElement.style.colorScheme = colors.isDark ? "dark" : "light";
    document.documentElement.style.backgroundColor = colors.canvas;

    // Update <meta name="theme-color"> for browser/OS chrome
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute("content", colors.canvas);
  }, [themeName, colors, islands]);

  return (
    <ThemeContext.Provider value={{ themeName, colors, setThemeName, availableThemes, fontSizes, setFontSizes, islands, setIslands }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  return useContext(ThemeContext);
}
