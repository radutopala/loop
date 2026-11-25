import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_FONT_SIZES, ThemeProvider, useTheme } from "./ThemeContext";
import { builtinThemes, type ColorPalette, darkColors, lightColors } from "./theme";

/** Shows what the context holds, with buttons that change it. */
function Probe() {
  const t = useTheme();
  return (
    <div>
      <output aria-label="theme">{t.themeName}</output>
      <output aria-label="canvas">{t.colors.canvas}</output>
      <output aria-label="radius">{String(t.colors.islandRadius)}</output>
      <output aria-label="themes">{t.availableThemes.join(",")}</output>
      <output aria-label="fonts">{JSON.stringify(t.fontSizes)}</output>
      <output aria-label="islands">{String(t.islands)}</output>
      <output aria-label="pills">{t.hiddenPills.join(",")}</output>
      <button type="button" onClick={() => t.setThemeName("light")}>
        light
      </button>
      <button type="button" onClick={() => t.setThemeName("nope")}>
        unknown
      </button>
      <button type="button" onClick={() => t.setFontSizes({ ...t.fontSizes, chat: 20 })}>
        fonts
      </button>
      <button type="button" onClick={() => t.setIslands(!t.islands)}>
        islands
      </button>
      <button type="button" onClick={() => t.setHiddenPills(["pr"])}>
        pills
      </button>
    </div>
  );
}

const out = (name: string) => screen.getByRole("status", { name }).textContent;

describe("ThemeContext", () => {
  beforeEach(() => {
    localStorage.clear();
    document.head.innerHTML = "";
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("gives defaults without a provider, whose setters do nothing", async () => {
    const user = userEvent.setup();
    render(<Probe />);
    expect(out("theme")).toBe("dark");
    expect(out("themes")).toBe("dark,light");
    expect(out("fonts")).toBe(JSON.stringify(DEFAULT_FONT_SIZES));
    expect(out("islands")).toBe("true");
    for (const name of ["light", "fonts", "islands", "pills"]) await user.click(screen.getByRole("button", { name }));
    expect(out("theme")).toBe("dark");
    expect(out("islands")).toBe("true");
    expect(out("pills")).toBe("");
  });

  it("starts from the initial values and persists the theme and islands", () => {
    const custom: ColorPalette = { ...lightColors, canvas: "#abcdef" };
    render(
      <ThemeProvider initialTheme="mine" initialFontSizes={{ chat: 15 }} initialIslands={false} initialHiddenPills={["git"]} customThemes={{ mine: custom }}>
        <Probe />
      </ThemeProvider>,
    );
    expect(out("theme")).toBe("mine");
    expect(out("themes")).toBe([...Object.keys(builtinThemes), "mine"].join(","));
    expect(out("fonts")).toBe(JSON.stringify({ ...DEFAULT_FONT_SIZES, chat: 15 }));
    expect(out("pills")).toBe("git");
    // Without islands the canvas is the plain background, with no rounding.
    expect(out("canvas")).toBe(custom.bg);
    expect(out("radius")).toBe("0");
    expect(localStorage.getItem("loop-theme")).toBe("mine");
    expect(localStorage.getItem("loop-islands")).toBe("0");
  });

  it("falls back to dark for an unknown initial theme", () => {
    render(
      <ThemeProvider initialTheme="nope">
        <Probe />
      </ThemeProvider>,
    );
    expect(out("theme")).toBe("dark");
    expect(out("canvas")).toBe(darkColors.canvas);
    expect(localStorage.getItem("loop-islands")).toBe("1");
  });

  it("changes, persists and broadcasts the theme, and the other settings", async () => {
    const setTheme = vi.fn();
    vi.stubGlobal("loopAPI", { setTheme });
    const user = userEvent.setup();
    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );
    await user.click(screen.getByRole("button", { name: "light" }));
    expect(out("theme")).toBe("light");
    expect(localStorage.getItem("loop-theme")).toBe("light");
    expect(setTheme).toHaveBeenCalledWith("light");
    expect(document.documentElement.style.colorScheme).toBe("light");

    await user.click(screen.getByRole("button", { name: "fonts" }));
    expect(JSON.parse(out("fonts") ?? "{}").chat).toBe(20);
    await user.click(screen.getByRole("button", { name: "islands" }));
    expect(out("islands")).toBe("false");
    await user.click(screen.getByRole("button", { name: "pills" }));
    expect(out("pills")).toBe("pr");
  });

  it("uses the dark colors for a theme name it doesn't know", async () => {
    const user = userEvent.setup();
    render(
      <ThemeProvider initialTheme="light">
        <Probe />
      </ThemeProvider>,
    );
    await user.click(screen.getByRole("button", { name: "unknown" }));
    expect(out("theme")).toBe("nope");
    expect(out("canvas")).toBe(darkColors.canvas);
  });

  it("follows a theme another window picked, ignoring unknown ones, until unmounted", () => {
    let changed: (name: string) => void = () => {};
    const off = vi.fn();
    vi.stubGlobal("loopAPI", {
      onThemeChanged: (cb: (name: string) => void) => {
        changed = cb;
        return off;
      },
    });
    const { unmount } = render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );
    act(() => changed("nope"));
    expect(out("theme")).toBe("dark");
    act(() => changed("light"));
    expect(out("theme")).toBe("light");
    expect(localStorage.getItem("loop-theme")).toBe("light");
    unmount();
    expect(off).toHaveBeenCalled();
  });

  it("styles the page: scrollbars, background and theme-color", () => {
    const initial = document.createElement("style");
    initial.id = "loop-initial-scrollbar";
    document.head.appendChild(initial);
    const meta = document.createElement("meta");
    meta.setAttribute("name", "theme-color");
    document.head.appendChild(meta);

    const { rerender } = render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );
    expect(document.getElementById("loop-initial-scrollbar")).toBeNull();
    const style = document.getElementById("loop-theme-globals");
    expect(style?.textContent).toContain(darkColors.scrollThumb);
    expect(style?.textContent).toContain(`background-color: ${darkColors.canvas}`);
    expect(meta.getAttribute("content")).toBe(darkColors.canvas);
    expect(document.documentElement.style.colorScheme).toBe("dark");

    // Rendering again reuses the same style element.
    rerender(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );
    expect(document.querySelectorAll("#loop-theme-globals")).toHaveLength(1);
  });
});
