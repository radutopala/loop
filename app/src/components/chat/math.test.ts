import { describe, expect, it } from "vitest";
import { findMathBlock, inlineMathPattern, inlineMathTeX, isInlineMath, renderMath } from "./math";

describe("findMathBlock", () => {
  const cases: { name: string; lines: string[]; want: { tex: string; source: string; next: number } | null }[] = [
    { name: "$$ on one line", lines: ["$$x = -1$$", "after"], want: { tex: "x = -1", source: "$$x = -1$$", next: 1 } },
    { name: "$$ with padding", lines: ["  $$ \\frac{a}{b} $$  "], want: { tex: "\\frac{a}{b}", source: "$$ \\frac{a}{b} $$", next: 1 } },
    {
      name: "$$ over several lines",
      lines: ["$$", "|A| = \\begin{cases}", "A \\\\ -A", "\\end{cases}", "$$", "after"],
      want: { tex: "|A| = \\begin{cases}\nA \\\\ -A\n\\end{cases}", source: "$$\n|A| = \\begin{cases}\nA \\\\ -A\n\\end{cases}\n$$", next: 5 },
    },
    { name: "$$ opening and closing on content lines", lines: ["$$a +", "b$$"], want: { tex: "a +\nb", source: "$$a +\nb$$", next: 2 } },
    { name: "\\[ on one line", lines: ["\\[ x^2 \\]"], want: { tex: "x^2", source: "\\[ x^2 \\]", next: 1 } },
    { name: "\\[ over several lines", lines: ["\\[", "x^2", "\\]"], want: { tex: "x^2", source: "\\[\nx^2\n\\]", next: 3 } },
    { name: "never closed", lines: ["$$ x", "still going"], want: null },
    { name: "empty", lines: ["$$$$"], want: null },
    { name: "empty over several lines", lines: ["$$", "$$"], want: null },
    { name: "two inline formulas on one line", lines: ["$$a$$ and $$b$$"], want: null },
    { name: "plain text", lines: ["costs $5"], want: null },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(findMathBlock(c.lines, 0)).toEqual(c.want);
    });
  }
});

describe("inlineMathPattern", () => {
  const matches = (text: string) => [...text.matchAll(new RegExp(inlineMathPattern, "g"))].map((m) => m[0]);
  const cases: { name: string; text: string; want: string[] }[] = [
    { name: "dollars", text: "so $x \\in [-2, -1]$ holds", want: ["$x \\in [-2, -1]$"] },
    { name: "double dollars mid-line", text: "then $$S = \\varnothing$$ here", want: ["$$S = \\varnothing$$"] },
    { name: "parens", text: "where \\(a^2\\) is", want: ["\\(a^2\\)"] },
    { name: "two formulas", text: "$a$ and $b$", want: ["$a$", "$b$"] },
    { name: "escaped dollar inside", text: "$\\$5 + x$", want: ["$\\$5 + x$"] },
    { name: "prices", text: "costs $5 and $10", want: [] },
    { name: "space after the opener", text: "$ x$", want: [] },
    { name: "space before the closer", text: "$x $", want: [] },
    { name: "digit after the closer", text: "$x$5", want: [] },
    { name: "shell variables", text: "$HOME/$USER", want: [] },
    { name: "escaped dollar outside", text: "\\$x$", want: [] },
    { name: "no newline inside", text: "$a\nb$", want: [] },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(matches(c.text)).toEqual(c.want);
    });
  }
});

describe("inlineMathTeX", () => {
  const cases: { token: string; want: string }[] = [
    { token: "$x^2$", want: "x^2" },
    { token: "$$ x^2 $$", want: "x^2" },
    { token: "\\( x^2 \\)", want: "x^2" },
  ];
  for (const c of cases) {
    it(c.token, () => {
      expect(inlineMathTeX(c.token)).toBe(c.want);
    });
  }
});

describe("isInlineMath", () => {
  const cases: { token: string; want: boolean }[] = [
    { token: "$x$", want: true },
    { token: "\\(x\\)", want: true },
    { token: "`code`", want: false },
    { token: "**bold**", want: false },
  ];
  for (const c of cases) {
    it(c.token, () => {
      expect(isInlineMath(c.token)).toBe(c.want);
    });
  }
});

describe("renderMath", () => {
  it("renders inline math", () => {
    const html = renderMath("\\sqrt{x}", false);
    expect(html).toContain('class="katex"');
    expect(html).not.toContain("katex-display");
  });

  it("renders display math", () => {
    expect(renderMath("\\frac{a}{b}", true)).toContain("katex-display");
  });

  it("returns the same markup from the cache", () => {
    expect(renderMath("x^2", false)).toBe(renderMath("x^2", false));
  });

  it("shows bad input as an error instead of throwing", () => {
    expect(renderMath("\\frac{a", true)).toContain("katex-error");
  });

  it("doesn't turn \\href into a link", () => {
    expect(renderMath("\\href{javascript:alert(1)}{x}", false)).not.toContain("<a ");
  });

  it("keeps rendering once the cache is full", () => {
    for (let n = 0; n < 600; n++) renderMath(`x_{${n}}`, false);
    expect(renderMath("x_{0}", false)).toContain('class="katex"');
  });
});
