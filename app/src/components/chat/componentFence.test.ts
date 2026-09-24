import { describe, expect, it } from "vitest";
import { componentHeight, parseComponentInfo, readFence } from "./componentFence";

describe("readFence", () => {
  const cases: { name: string; lines: string[]; want: ReturnType<typeof readFence> }[] = [
    { name: "not a fence", lines: ["text"], want: null },
    { name: "two backticks", lines: ["``x``"], want: null },
    { name: "code block", lines: ["```go", "x := 1", "```", "after"], want: { info: "go", body: "x := 1", closed: true, next: 3 } },
    { name: "empty block", lines: ["```", "```"], want: { info: "", body: "", closed: true, next: 2 } },
    { name: "closing fence may be longer", lines: ["```", "a", "`````"], want: { info: "", body: "a", closed: true, next: 3 } },
    { name: "closing fence may have trailing spaces", lines: ["```", "a", "```  "], want: { info: "", body: "a", closed: true, next: 3 } },
    {
      name: "a shorter or tagged fence inside doesn't close it",
      lines: ["````loop-component math T", "```js", "x", "```", "````"],
      want: { info: "loop-component math T", body: "```js\nx\n```", closed: true, next: 5 },
    },
    { name: "a line starting with backticks and text doesn't close", lines: ["```", "```js", "```"], want: { info: "", body: "```js", closed: true, next: 3 } },
    { name: "unclosed runs to the end", lines: ["```loop-component math", "<p>", "x"], want: { info: "loop-component math", body: "<p>\nx", closed: false, next: 3 } },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(readFence(c.lines, 0)).toEqual(c.want);
    });
  }

  it("reads from the given line", () => {
    expect(readFence(["before", "```", "a", "```"], 1)).toEqual({ info: "", body: "a", closed: true, next: 4 });
  });
});

describe("parseComponentInfo", () => {
  const cases: { info: string; want: ReturnType<typeof parseComponentInfo> }[] = [
    { info: "loop-component math Fracții algebrice", want: { template: "math", title: "Fracții algebrice" } },
    { info: "loop-component canvas", want: { template: "canvas", title: "" } },
    { info: "loop-component my_tpl-2  Title ", want: { template: "my_tpl-2", title: "Title" } },
    { info: "loop-component", want: null },
    { info: "loop-component Math", want: null },
    { info: "loop-components math", want: null },
    { info: "html", want: null },
  ];
  for (const c of cases) {
    it(JSON.stringify(c.info), () => {
      expect(parseComponentInfo(c.info)).toEqual(c.want);
    });
  }
});

describe("componentHeight", () => {
  const cases: { name: string; data: unknown; want: number | null }[] = [
    { name: "reported height, rounded up", data: { type: "loop-component-height", height: 240.2 }, want: 241 },
    { name: "clamped to the minimum", data: { type: "loop-component-height", height: 10 }, want: 80 },
    { name: "clamped to the maximum", data: { type: "loop-component-height", height: 5000 }, want: 900 },
    { name: "another message type", data: { type: "other", height: 300 }, want: null },
    { name: "not a number", data: { type: "loop-component-height", height: "300" }, want: null },
    { name: "not finite", data: { type: "loop-component-height", height: Number.POSITIVE_INFINITY }, want: null },
    { name: "not an object", data: "loop-component-height", want: null },
    { name: "null", data: null, want: null },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(componentHeight(c.data)).toBe(c.want);
    });
  }
});
