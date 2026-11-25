import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CommitEntry } from "../../api/loopApi";
import { CommitHistory } from "./CommitHistory";

const withBody: CommitEntry = { hash: "aaa111", short: "aaa111", subject: "Add login", author: "Ann", date: "2026-01-02T10:00:00Z", body: "  Longer text\nsecond line  " };
const noBody: CommitEntry = { hash: "bbb222", short: "bbb222", subject: "Fix typo", author: "Bob", date: "2026-01-03T10:00:00Z" };
const blankBody: CommitEntry = { hash: "ccc333", short: "ccc333", subject: "Bump", author: "Cy", date: "2026-01-04T10:00:00Z", body: "   " };

// The element at i, failing the test when there's none.
function rowAt(els: HTMLElement[], i: number): HTMLElement {
  const el = els[i];
  if (!el) throw new Error(`no element ${i}`);
  return el;
}

function renderHistory(props: Partial<Parameters<typeof CommitHistory>[0]> = {}) {
  const onLoadMore = vi.fn();
  const onSelect = vi.fn();
  render(<CommitHistory commits={[withBody, noBody, blankBody]} commitsLoading={false} onLoadMore={onLoadMore} onSelect={onSelect} {...props} />);
  return { onLoadMore, onSelect };
}

describe("CommitHistory", () => {
  it("shows a loading line while the first page loads", () => {
    renderHistory({ commits: [], commitsLoading: true });
    expect(screen.getByText("Loading...")).toBeTruthy();
    expect(screen.queryByText("No commits")).toBeNull();
  });

  it("says there are no commits once loading finished empty", () => {
    renderHistory({ commits: [] });
    expect(screen.getByText("No commits")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("lists each commit with its author and shows a loading-more line under a non-empty list", () => {
    renderHistory({ commitsLoading: true });
    const rows = screen.getAllByTestId("commit-row");
    const [first, second] = rows as [HTMLElement, HTMLElement, HTMLElement];
    expect(rows).toHaveLength(3);
    expect(first.textContent).toContain("aaa111");
    expect(first.textContent).toContain("Add login");
    expect(second.textContent).toContain("Bob");
    expect(screen.getByText("Loading more...")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("offers a body toggle only for commits with a non-blank body, expanding and collapsing it", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderHistory();
    const toggles = screen.getAllByTestId("commit-body-toggle");
    expect(toggles).toHaveLength(1);
    expect(rowAt(toggles, 0).title).toBe("Expand full message");

    await user.click(rowAt(toggles, 0));
    expect(screen.getByText(/Longer text/).textContent).toBe("Longer text\nsecond line");
    expect(screen.getByTestId("commit-body-toggle").title).toBe("Collapse message");
    // The toggle doesn't open the commit.
    expect(onSelect).not.toHaveBeenCalled();

    await user.click(screen.getByTestId("commit-body-toggle"));
    expect(screen.queryByText(/Longer text/)).toBeNull();
  });

  it("opens the commit a row is clicked on", async () => {
    const user = userEvent.setup();
    const { onSelect } = renderHistory();
    await user.click(rowAt(screen.getAllByTestId("commit-row"), 1));
    expect(onSelect).toHaveBeenCalledWith(noBody);
  });

  it("highlights a row on hover and clears it on leave", () => {
    renderHistory();
    const row = rowAt(screen.getAllByTestId("commit-row"), 0);
    fireEvent.mouseEnter(row);
    expect(row.style.background).not.toBe("transparent");
    expect(row.style.background).not.toBe("");
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe("transparent");
  });

  it("loads more when scrolled near the bottom, not before", () => {
    const { onLoadMore } = renderHistory();
    const scroller = rowAt(screen.getAllByTestId("commit-row"), 0).parentElement as HTMLDivElement;
    Object.defineProperty(scroller, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(scroller, "clientHeight", { value: 300, configurable: true });

    scroller.scrollTop = 100;
    fireEvent.scroll(scroller);
    expect(onLoadMore).not.toHaveBeenCalled();

    scroller.scrollTop = 650;
    fireEvent.scroll(scroller);
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });
});
