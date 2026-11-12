// @vitest-environment jsdom
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GrepFile, GrepResult } from "../../api/loopApi";
import { type ContentSearch, contentHits, contentSummary, highlightParts, useContentSearch } from "./contentSearch";

const grepFiles = vi.hoisted(() => vi.fn());
vi.mock("../../api/loopApi", () => ({ grepFiles }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type ProbeProps = { q: string; on?: boolean; regex?: boolean };
const probe: { current: ContentSearch } = { current: { state: "idle" } };

function Probe(p: ProbeProps) {
  probe.current = useContentSearch("ch", p.q, p.regex === undefined ? {} : { regex: p.regex, caseSensitive: false }, p.on ?? true);
  return null;
}

/** Renders useContentSearch with props, and rerenders with new ones. */
function renderSearch(props: ProbeProps) {
  const root = createRoot(document.createElement("div"));
  act(() => root.render(createElement(Probe, props)));
  return {
    result: probe,
    rerender: (next: ProbeProps) => act(() => root.render(createElement(Probe, next))),
  };
}

const file = (rel: string, lines: number[]): GrepFile => ({
  root_index: 0,
  rel_path: rel,
  matches: lines.map((line) => ({ line, column: 1, length: 1, text: "x" })),
});

describe("contentHits", () => {
  it("lists every match in order", () => {
    const a = file("a", [1, 4]);
    const b = file("b", [2]);
    expect(contentHits([a, b])).toEqual([
      { file: a, line: 1 },
      { file: a, line: 4 },
      { file: b, line: 2 },
    ]);
  });
});

describe("highlightParts", () => {
  it.each([
    ["func Hello()", 6, 5, ["func ", "Hello", "()"]],
    ["héllo wörld", 7, 5, ["héllo ", "wörld", ""]],
    ["emoji 😀 here", 7, 1, ["emoji ", "😀", " here"]],
    ["short", 10, 3, ["short", "", ""]],
    ["abc", 0, -1, ["", "", "abc"]],
  ] as const)("splits %j at %i", (text, column, length, want) => {
    expect(highlightParts(text, column, length)).toEqual(want);
  });
});

describe("contentSummary", () => {
  const result = (files: GrepFile[], truncated = false): GrepResult => ({ files, truncated, engine: "ripgrep" });
  it.each([
    [result([file("a", [1])]), "1 match in 1 file"],
    [result([file("a", [1, 2]), file("b", [3])]), "3 matches in 2 files"],
    [result([file("a", [1])], true), "1 match in 1 file; more not shown, refine the search"],
  ])("sums up %#", (r, want) => {
    expect(contentSummary(r)).toBe(want);
  });
});

describe("useContentSearch", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    grepFiles.mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("stays idle for a short query or when off", () => {
    const { result, rerender } = renderSearch({ q: "a" });
    expect(result.current).toEqual({ state: "idle" });
    rerender({ q: "abc", on: false });
    act(() => vi.runAllTimers());
    expect(result.current).toEqual({ state: "idle" });
    expect(grepFiles).not.toHaveBeenCalled();
  });

  it("searches after the debounce and keeps the last results while the next runs", async () => {
    const done: GrepResult = { files: [file("a", [1])], truncated: false, engine: "ripgrep" };
    grepFiles.mockResolvedValue(done);
    const { result, rerender } = renderSearch({ q: " ab ", regex: true });
    expect(result.current).toEqual({ state: "searching" });
    await act(async () => {
      await vi.runAllTimersAsync();
    });
    expect(grepFiles).toHaveBeenCalledWith("ch", "ab", { regex: true, caseSensitive: false });
    expect(result.current).toEqual({ state: "done", result: done });
    rerender({ q: "abc", regex: true });
    expect(result.current).toEqual({ state: "done", result: done });
  });

  it("reports a failed search", async () => {
    grepFiles.mockRejectedValueOnce(new Error("invalid regex: missing )")).mockRejectedValueOnce("boom");
    const { result, rerender } = renderSearch({ q: "(ab" });
    await act(async () => {
      await vi.runAllTimersAsync();
    });
    expect(result.current).toEqual({ state: "error", message: "invalid regex: missing )" });
    rerender({ q: "(abc" });
    await act(async () => {
      await vi.runAllTimersAsync();
    });
    expect(result.current).toEqual({ state: "error", message: "Search failed" });
  });

  it("drops a search the query moved on from", async () => {
    let resolve: (r: GrepResult) => void = () => {};
    grepFiles.mockReturnValueOnce(new Promise((r) => (resolve = r))).mockReturnValueOnce(new Promise(() => {}));
    const { result, rerender } = renderSearch({ q: "ab" });
    await act(async () => {
      await vi.runAllTimersAsync();
    });
    rerender({ q: "abc" });
    await act(async () => {
      resolve({ files: [], truncated: false, engine: "builtin" });
      await vi.runAllTimersAsync();
    });
    expect(result.current).toEqual({ state: "searching" });
  });
});
