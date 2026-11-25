// @vitest-environment jsdom
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPR, type PRInfo } from "../../api/git";
import { getPRStatus } from "../../api/pr";
import type { Channel } from "../../types";
import { RowInfoPopup } from "./RowInfoPopup";

vi.mock("../../api/git", () => ({ fetchPR: vi.fn() }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn() }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const lookup = vi.mocked(fetchPR);
const statusLookup = vi.mocked(getPRStatus);
const channel = { id: "row", name: "row", branch: "feature" } as Channel;

describe("RowInfoPopup's PR lookup", () => {
  let root: Root;

  const hover = (hovered: boolean, row = channel) =>
    act(() => {
      const anchor = document.createElement("div");
      root.render(createElement(RowInfoPopup, { channel: row, anchorRef: { current: anchor }, hovered }));
    });
  const wait = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
  const shown = (key: string) => document.querySelector(`[data-testid="sidebar-row-info-${key}"]`)?.textContent;

  beforeEach(() => {
    vi.useFakeTimers();
    lookup.mockReset();
    statusLookup.mockReset();
    statusLookup.mockResolvedValue(null);
    lookup.mockResolvedValue({ present: true, pr: { number: 42, state: "OPEN" } as PRInfo });
    root = createRoot(document.body.appendChild(document.createElement("div")));
  });

  afterEach(() => {
    act(() => root.unmount());
    vi.useRealTimers();
    document.body.innerHTML = "";
  });

  it("shows the popup at once and looks the PR up once the pointer has rested", async () => {
    hover(true);
    expect(shown("branch")).toBe("feature");
    await wait(249);
    expect(lookup).not.toHaveBeenCalled();
    expect(shown("pr")).toBeUndefined();
    await wait(1);
    expect(lookup).toHaveBeenCalledTimes(1);
    expect(lookup).toHaveBeenCalledWith("row");
    expect(shown("pr")).toContain("42");
  });

  it("looks nothing up for a row the pointer only crosses", async () => {
    hover(true);
    await wait(249);
    hover(false);
    await wait(1000);
    expect(lookup).not.toHaveBeenCalled();
  });

  it("starts the wait over when the pointer comes back", async () => {
    hover(true);
    await wait(200);
    hover(false);
    hover(true);
    await wait(249);
    expect(lookup).not.toHaveBeenCalled();
    await wait(1);
    expect(lookup).toHaveBeenCalledTimes(1);
  });

  it("drops an answer that arrives after the pointer left", async () => {
    let answer = (_: { present: boolean; pr: PRInfo }) => {};
    lookup.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    hover(true);
    await wait(250);
    hover(false);
    await act(async () => answer({ present: true, pr: { number: 42, state: "OPEN" } as PRInfo }));
    hover(true);
    expect(shown("pr")).toBeUndefined();
  });

  const testId = (id: string) => document.querySelector(`[data-testid="${id}"]`) as HTMLElement | null;

  it("shows the full name with a trimmed description over the lines, ruled off from them", () => {
    hover(true, { ...channel, description: "  what it is for  " });
    expect(testId("sidebar-row-info-title")?.textContent).toBe("row");
    const desc = testId("sidebar-row-info-description");
    expect(desc?.textContent).toBe("what it is for");
    expect(desc?.style.paddingBottom).toBe("6px");
    expect(shown("branch")).toBe("feature");
  });

  it("shows a description alone without a rule under it", () => {
    hover(true, { id: "bare", name: "bare", description: "only this" } as Channel);
    expect(testId("sidebar-row-info-description")?.style.paddingBottom).toBe("");
    expect(document.querySelectorAll("[data-testid='sidebar-row-info'] svg")).toHaveLength(0);
  });

  it("shows only the title for a row with nothing else to say", () => {
    hover(true, { id: "bare", name: "bare", description: "   " } as Channel);
    expect(testId("sidebar-row-info-title")?.textContent).toBe("bare");
    expect(testId("sidebar-row-info-description")).toBeNull();
    expect(testId("sidebar-row-info-title")?.style.paddingBottom).toBe("");
  });

  it("shows a line's detail and asks the watcher's status for a row with a PR pill", async () => {
    hover(true, { ...channel, worktree: true, base_branch: "main", pr_attention: "failed", unresolved_threads: 2 } as Channel);
    expect(testId("sidebar-row-info-branch-detail")?.textContent).toBe(" (from main)");
    expect(shown("ci")).toBe("failed");
    expect(shown("cmt")).toBe("2 unresolved review threads");
    await wait(0);
    expect(statusLookup).toHaveBeenCalledWith("row");
  });

  it("asks the watcher's status for unresolved threads alone, not for a plain row", async () => {
    hover(true);
    await wait(0);
    expect(statusLookup).not.toHaveBeenCalled();
    hover(true, { ...channel, unresolved_threads: 1 } as Channel);
    await wait(0);
    expect(statusLookup).toHaveBeenCalledWith("row");
  });
});
