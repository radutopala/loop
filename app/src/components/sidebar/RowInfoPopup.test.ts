// @vitest-environment jsdom
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPR, type PRInfo } from "../../api/git";
import type { Channel } from "../../types";
import { RowInfoPopup } from "./RowInfoPopup";

vi.mock("../../api/git", () => ({ fetchPR: vi.fn() }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn() }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const lookup = vi.mocked(fetchPR);
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
});
