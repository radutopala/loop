// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type FileSearchResult, type RootEntry, searchFiles } from "../../api/loopApi";
import { fileSearchLabel, nextSearchIndex, useFileSearch } from "./fileSearch";

vi.mock("../../api/loopApi", () => ({ searchFiles: vi.fn() }));

const hit: FileSearchResult = { root_index: 0, rel_path: "src/a.ts", name: "a.ts" };

describe("useFileSearch", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(searchFiles).mockReset();
  });
  afterEach(() => vi.useRealTimers());

  it("is null for an empty query and searches nothing", async () => {
    const { result } = renderHook(() => useFileSearch("ch", "   "));
    await act(() => vi.advanceTimersByTimeAsync(500));
    expect(result.current).toBeNull();
    expect(searchFiles).not.toHaveBeenCalled();
  });

  it("searches the trimmed query a moment after the last keystroke", async () => {
    vi.mocked(searchFiles).mockResolvedValue([hit]);
    const { result, rerender } = renderHook(({ q }) => useFileSearch("ch", q), { initialProps: { q: "a" } });
    await act(() => vi.advanceTimersByTimeAsync(50));
    rerender({ q: " a.t " });
    await act(() => vi.advanceTimersByTimeAsync(119));
    expect(searchFiles).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(searchFiles).toHaveBeenCalledExactlyOnceWith("ch", "a.t", 50, true);
    expect(result.current).toEqual([hit]);
  });

  it("gives no results when the search fails", async () => {
    vi.mocked(searchFiles).mockRejectedValue(new Error("down"));
    const { result } = renderHook(() => useFileSearch("ch", "a"));
    await act(() => vi.advanceTimersByTimeAsync(120));
    expect(result.current).toEqual([]);
  });

  it("drops a search that answers after the query moved on", async () => {
    let resolve: (r: FileSearchResult[]) => void = () => {};
    let reject: (e: Error) => void = () => {};
    vi.mocked(searchFiles)
      .mockReturnValueOnce(new Promise((r) => (resolve = r)))
      .mockReturnValueOnce(new Promise((_r, j) => (reject = j)));
    const { result, rerender } = renderHook(({ q }) => useFileSearch("ch", q), { initialProps: { q: "a" } });
    await act(() => vi.advanceTimersByTimeAsync(120));
    rerender({ q: "b" });
    await act(() => vi.advanceTimersByTimeAsync(120));
    rerender({ q: "" });
    await act(async () => {
      resolve([hit]);
      reject(new Error("late"));
    });
    expect(result.current).toBeNull();
  });
});

const roots: RootEntry[] = [
  { index: 0, path: "/w/app", display_path: "~/w/app", name: "app" },
  { index: 1, path: "/w/lib", display_path: "~/w/lib", name: "lib" },
];

describe("fileSearchLabel", () => {
  it.each([
    ["a nested file, one root", { root_index: 0, rel_path: "src/a.ts", name: "a.ts" }, roots.slice(0, 1), { name: "a.ts", where: "src" }],
    ["a top-level file, one root", { root_index: 0, rel_path: "a.ts", name: "a.ts" }, roots.slice(0, 1), { name: "a.ts", where: "" }],
    ["a nested file, two roots", { root_index: 1, rel_path: "x/b.go", name: "b.go" }, roots, { name: "b.go", where: "lib/x" }],
    ["a top-level file, two roots", { root_index: 1, rel_path: "b.go", name: "b.go" }, roots, { name: "b.go", where: "lib" }],
  ])("%s", (_name, result, rs, want) => {
    expect(fileSearchLabel(result, rs)).toEqual(want);
  });
});

describe("nextSearchIndex", () => {
  it.each([
    ["down", "ArrowDown", 0, 3, 1],
    ["down wraps", "ArrowDown", 2, 3, 0],
    ["up wraps", "ArrowUp", 0, 3, 2],
    ["another key", "a", 1, 3, null],
    ["no results", "ArrowDown", 0, 0, null],
  ])("%s", (_name, key, index, count, want) => {
    expect(nextSearchIndex(key, index, count)).toBe(want);
  });
});
