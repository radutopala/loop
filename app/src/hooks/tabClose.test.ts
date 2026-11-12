import { describe, expect, it } from "vitest";
import { type BulkClose, selectionAfterClose, tabsToClose } from "./tabClose";

const tabs = ["a", "b", "c", "d", "e"];

describe("tabsToClose", () => {
  it.each<[string, BulkClose, string, string[], string[]]>([
    ["others", "others", "c", [], ["a", "b", "d", "e"]],
    ["others keep unsaved", "others", "c", ["a", "e"], ["b", "d"]],
    ["others keep the tab even when unsaved", "others", "c", ["c"], ["a", "b", "d", "e"]],
    ["to the right", "right", "c", [], ["d", "e"]],
    ["to the right keep unsaved", "right", "b", ["d"], ["c", "e"]],
    ["to the right of the last tab", "right", "e", [], []],
    ["to the right of a closed tab", "right", "x", [], []],
    ["saved", "saved", "c", ["b", "c"], ["a", "d", "e"]],
    ["saved with every tab unsaved", "saved", "a", tabs, []],
  ])("%s", (_, kind, path, unsaved, want) => {
    expect(tabsToClose(kind, tabs, path, (t) => unsaved.includes(t))).toEqual(want);
  });
});

describe("selectionAfterClose", () => {
  it.each<[string, string | null, string[], string | null | undefined]>([
    ["nothing selected", null, ["a"], undefined],
    ["selected stays open", "c", ["a", "b"], undefined],
    ["single close picks the next", "c", ["c"], "d"],
    ["single close of the last picks the previous", "e", ["e"], "d"],
    ["skips closed tabs after it", "b", ["b", "c", "d"], "e"],
    ["falls back before it", "c", ["c", "d", "e"], "b"],
    ["every tab closed", "c", tabs, null],
  ])("%s", (_, selected, closing, want) => {
    expect(selectionAfterClose(tabs, selected, closing)).toBe(want);
  });
});
