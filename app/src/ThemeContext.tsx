import { createContext, useContext, useState, useEffect, type ReactNode } from "react";
import { builtinThemes, darkColors, type ColorPalette } from "./theme";

interface ThemeContextValue {
  themeName: string;
  colors: ColorPalette;
  setThemeName: (name: string) => void;
  availableThemes: string[];
}

const ThemeContext = createContext<ThemeContextValue>({
  themeName: "dark",
  colors: darkColors,
  setThemeName: () => {},
  availableThemes: ["dark", "light"],
});

export function ThemeProvider({ children, initialTheme, customThemes }: {
  children: ReactNode;
  initialTheme?: string;
  customThemes?: Record<string, ColorPalette>;
}) {
  const allThemes = { ...builtinThemes, ...customThemes };
  const availableThemes = Object.keys(allThemes);

  const [themeName, setThemeName] = useState<string>(
    initialTheme && allThemes[initialTheme] ? initialTheme : "dark"
  );
  const colors = allThemes[themeName] ?? darkColors;

  // Update scrollbar & body background via injected <style>
  useEffect(() => {
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
  }, [themeName, colors]);

  return (
    <ThemeContext.Provider value={{ themeName, colors, setThemeName, availableThemes }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme() {
  return useContext(ThemeContext);
}
