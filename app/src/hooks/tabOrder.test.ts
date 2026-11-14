import { describe, expect, it } from "vitest";
import { moveTab } from "./tabOrder";

const tabs = ["a", "b", "c", "d"];

describe("moveTab", () => {
  it.each<[string, string, string, boolean, string[]]>([
    ["forward, before the target", "a", "c", false, ["b", "a", "c", "d"]],
    ["forward, after the target", "a", "c", true, ["b", "c", "a", "d"]],
    ["backward, before the target", "d", "b", false, ["a", "d", "b", "c"]],
    ["backward, after the target", "d", "b", true, ["a", "b", "d", "c"]],
    ["to the end", "a", "d", true, ["b", "c", "d", "a"]],
    ["to the start", "c", "a", false, ["c", "a", "b", "d"]],
  ])("%s", (_, from, target, after, want) => {
    expect(moveTab(tabs, from, target, after)).toEqual(want);
  });

  it.each<[string, string, string, boolean]>([
    ["onto itself", "b", "b", true],
    ["before the next tab", "b", "c", false],
    ["after the previous tab", "c", "b", true],
    ["a tab that isn't open", "x", "b", false],
    ["onto a tab that isn't open", "b", "x", false],
  ])("leaves the order alone when moving %s", (_, from, target, after) => {
    expect(moveTab(tabs, from, target, after)).toBe(tabs);
  });
});
