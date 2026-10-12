import { describe, expect, it } from "vitest";
import { clearPaneTitle, paneTitle, setPaneTitle } from "./paneTitles";

describe("paneTitles", () => {
  it("keeps a title per leaf until its owner drops it", () => {
    const a = {};
    const b = {};
    expect(paneTitle("l1")).toBe("");
    setPaneTitle("l1", a, "Quiz");
    setPaneTitle("l2", b, "Stats");
    expect(paneTitle("l1")).toBe("Quiz");
    expect(paneTitle("l2")).toBe("Stats");
    setPaneTitle("l1", a, "");
    expect(paneTitle("l1")).toBe("");
    clearPaneTitle("l2", b);
    expect(paneTitle("l2")).toBe("");
  });

  it("leaves the title of the panel that took the leaf", () => {
    const gone = {};
    const now = {};
    setPaneTitle("l3", gone, "Old");
    setPaneTitle("l3", now, "New");
    clearPaneTitle("l3", gone);
    expect(paneTitle("l3")).toBe("New");
    clearPaneTitle("l3", now);
    expect(paneTitle("l3")).toBe("");
  });
});
