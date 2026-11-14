import { describe, expect, it } from "vitest";
import { dialogButtons, dialogHeading, type PageDialog, withDialog, withoutDialog } from "./browserDialog";

const confirmOn = (target_id: string, message = "Delete it?"): PageDialog => ({ target_id, type: "confirm", message });

describe("withDialog / withoutDialog", () => {
  it("keeps one dialog per tab, the latest winning", () => {
    let d = withDialog({}, confirmOn("t1"));
    d = withDialog(d, confirmOn("t2"));
    d = withDialog(d, confirmOn("t1", "Really?"));
    expect(Object.keys(d)).toEqual(["t1", "t2"]);
    expect(d.t1?.message).toBe("Really?");
  });

  it("drops a tab's dialog and leaves the others", () => {
    const d = withoutDialog(withDialog(withDialog({}, confirmOn("t1")), confirmOn("t2")), "t1");
    expect(Object.keys(d)).toEqual(["t2"]);
  });

  it("returns the same map when the tab had no dialog, so state does not change", () => {
    const d = withDialog({}, confirmOn("t1"));
    expect(withoutDialog(d, "t9")).toBe(d);
  });
});

describe("dialogHeading / dialogButtons", () => {
  it.each<[string, string, { accept: string; dismiss: string }]>([
    ["confirm", "The page asks you to confirm:", { accept: "OK", dismiss: "Cancel" }],
    ["prompt", "The page asks:", { accept: "OK", dismiss: "Cancel" }],
    ["beforeunload", "Leave this page? Changes you made may not be saved.", { accept: "Leave", dismiss: "Stay" }],
  ])("%s", (type, heading, buttons) => {
    const d: PageDialog = { target_id: "t1", type, message: "m" };
    expect(dialogHeading(d)).toBe(heading);
    expect(dialogButtons(d)).toEqual(buttons);
  });
});
