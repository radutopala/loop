import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { darkColors } from "../theme";
import { buildBtnSecondaryStyle, buildBtnStyle, buildHeaderBtnStyle, buildInputStyle, elapsed, hoverIn, hoverOut, STATUS_COLORS, timeAgo } from "./workflowHelpers";

const NOW = new Date("2026-01-02T00:00:00Z").getTime();
const ago = (secs: number) => new Date(NOW - secs * 1000).toISOString();

describe("timeAgo", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it.each([
    [null, "-"],
    ["", "-"],
    ["not a date", "-"],
    [ago(5), "5s ago"],
    [ago(125), "2m ago"],
    [ago(3 * 3600 + 10), "3h ago"],
    [ago(2 * 86400 + 5), "2d ago"],
  ])("%s -> %s", (input, want) => {
    expect(timeAgo(input)).toBe(want);
  });
});

describe("elapsed", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it.each([
    [null, null, "-"],
    [ago(42), null, "42s"],
    [ago(125), ago(0), "2m 5s"],
    [ago(2 * 3600 + 7 * 60), null, "2h 7m"],
    [ago(0), ago(10), "0s"],
  ])("%s to %s -> %s", (start, end, want) => {
    expect(elapsed(start, end)).toBe(want);
  });
});

describe("styles", () => {
  it("colors each status", () => {
    expect(STATUS_COLORS.failed).toBe("#ef4444");
  });

  it("builds button and input styles from the palette", () => {
    expect(buildHeaderBtnStyle(darkColors)).toMatchObject({ color: darkColors.textDim, background: "none" });
    expect(buildBtnStyle(darkColors)).toMatchObject({ background: darkColors.active, color: "#fff" });
    expect(buildBtnSecondaryStyle(darkColors)).toMatchObject({ background: "transparent", border: `1px solid ${darkColors.border}`, color: darkColors.text, padding: "4px 10px" });
    expect(buildInputStyle(darkColors)).toMatchObject({ background: darkColors.surface, color: darkColors.text, boxSizing: "border-box" });
  });

  it("sets and clears the hover colors", () => {
    const style = { backgroundColor: "", color: "" };
    const e = { currentTarget: { style } } as unknown as React.MouseEvent<HTMLButtonElement>;
    hoverIn(e, darkColors);
    expect(style).toEqual({ backgroundColor: darkColors.hoverBg, color: darkColors.textLight });
    hoverOut(e, darkColors);
    expect(style).toEqual({ backgroundColor: "transparent", color: darkColors.textDim });
  });
});
