import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors } from "../../theme";
import { emptyGitLineChanges, gitGutterColors } from "./editorGitGutter";
import { GitChangeOverview } from "./editorGitOverview";

const renderRuler = (changes = emptyGitLineChanges(), totalLines = 10, onJumpToLine = vi.fn()) => {
  render(
    <ThemeProvider initialTheme="dark">
      <GitChangeOverview changes={changes} totalLines={totalLines} onJumpToLine={onJumpToLine} />
    </ThemeProvider>,
  );
  return onJumpToLine;
};

const someChanges = () => {
  const c = emptyGitLineChanges();
  c.changed.set(1, "added");
  c.changed.set(2, "added");
  c.changed.set(5, "modified");
  c.deletedAbove.add(8);
  return c;
};

describe("GitChangeOverview", () => {
  afterEach(() => vi.restoreAllMocks());

  it("renders nothing without changes or lines", () => {
    renderRuler();
    expect(screen.queryByTestId("git-overview-ruler")).toBeNull();
    renderRuler(someChanges(), 0);
    expect(screen.queryByTestId("git-overview-ruler")).toBeNull();
  });

  it("draws one mark per change block in its kind's colour", () => {
    renderRuler(someChanges());
    const marks = Array.from(screen.getByTestId("git-overview-ruler").children) as HTMLElement[];
    const palette = gitGutterColors(darkColors.isDark);
    const rgb = (hex: string) => {
      const el = document.createElement("div");
      el.style.backgroundColor = hex;
      return el.style.backgroundColor;
    };
    expect(marks.map((m) => [m.style.top, m.style.height, m.style.backgroundColor])).toEqual([
      ["0%", "20%", rgb(palette.added)],
      ["40%", "10%", rgb(palette.modified)],
      ["70%", "2px", rgb(palette.deleted)],
    ]);
  });

  it("jumps to the line under the click, clamped to the document", () => {
    const jump = renderRuler(someChanges());
    const ruler = screen.getByTestId("git-overview-ruler");
    vi.spyOn(ruler, "getBoundingClientRect").mockReturnValue({ top: 100, height: 200 } as DOMRect);
    fireEvent.click(ruler, { clientY: 200 });
    fireEvent.click(ruler, { clientY: 50 });
    fireEvent.click(ruler, { clientY: 400 });
    expect(jump.mock.calls).toEqual([[5], [1], [10]]);
  });

  it("ignores clicks while the ruler has no height", () => {
    const jump = renderRuler(someChanges());
    const ruler = screen.getByTestId("git-overview-ruler");
    fireEvent.click(ruler, { clientY: 10 });
    expect(jump).not.toHaveBeenCalled();
  });
});
