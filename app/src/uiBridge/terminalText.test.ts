import { describe, expect, it } from "vitest";
import { type TextBuffer, tailLines } from "./terminalText";

const buffer = (lines: string[]): TextBuffer => ({
  length: lines.length,
  getLine: (y) => (y < lines.length ? { translateToString: () => lines[y] ?? "" } : undefined),
});

describe("tailLines", () => {
  it.each([
    { name: "the last lines", lines: ["a", "b", "c"], n: 2, want: "b\nc" },
    { name: "all when there are fewer", lines: ["a", "b"], n: 5, want: "a\nb" },
    { name: "without the blank screen below", lines: ["a", "", "b", "", ""], n: 2, want: "\nb" },
    { name: "nothing from an empty screen", lines: ["", ""], n: 3, want: "" },
  ])("reads $name", ({ lines, n, want }) => {
    expect(tailLines(buffer(lines), n)).toBe(want);
  });

  it("reads a missing line as blank", () => {
    const b: TextBuffer = { length: 3, getLine: (y) => (y === 1 ? undefined : { translateToString: () => "x" }) };
    expect(tailLines(b, 3)).toBe("x\n\nx");
  });
});
