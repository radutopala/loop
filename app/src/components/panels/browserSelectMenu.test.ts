import { describe, expect, it, vi } from "vitest";
import { selectMenuItems } from "./browserSelectMenu";

describe("selectMenuItems", () => {
  it("checks the selected option and picks by the page's index", () => {
    const choose = vi.fn();
    const items = selectMenuItems({ selected: 1, options: [{ label: "One" }, { label: "Two" }] }, choose);
    expect(items.map((i) => [i.label, i.checked])).toEqual([
      ["One", false],
      ["Two", true],
    ]);
    items[1]?.onClick();
    expect(choose).toHaveBeenCalledWith(1);
  });

  it("leaves out disabled options without shifting the indices", () => {
    const choose = vi.fn();
    const items = selectMenuItems({ selected: 0, options: [{ label: "Pick one", disabled: true }, { label: "A" }, { label: "B" }] }, choose);
    expect(items.map((i) => i.label)).toEqual(["A", "B"]);
    items[1]?.onClick();
    expect(choose).toHaveBeenCalledWith(2);
  });

  it("starts each optgroup with a separator and its label", () => {
    const items = selectMenuItems(
      {
        selected: -1,
        options: [{ label: "None" }, { label: "Red", group: "Warm" }, { label: "Orange", group: "Warm" }, { label: "Blue", group: "Cold" }],
      },
      () => {},
    );
    expect(items.map((i) => [i.label, i.separator ?? false, i.hint ?? ""])).toEqual([
      ["None", false, ""],
      ["Red", true, "Warm"],
      ["Orange", false, ""],
      ["Blue", true, "Cold"],
    ]);
  });

  it("puts no separator above a group that opens the list", () => {
    const items = selectMenuItems({ selected: 0, options: [{ label: "Red", group: "Warm" }] }, () => {});
    expect(items[0]).toMatchObject({ label: "Red", hint: "Warm" });
    expect(items[0]?.separator).toBeUndefined();
  });
});
