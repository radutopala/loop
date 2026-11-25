import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { channel } from "../../test/chat-bits-sidebar-list-channel";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import { type PillKind, ShownPillsContext, SIDEBAR_PILLS } from "./pills";
import { SectionTabs, SessionSections } from "./SessionSections";

vi.mock("../../api/git", () => ({ fetchPR: vi.fn().mockResolvedValue({ present: false }) }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn().mockResolvedValue(null) }));

describe("SectionTabs", () => {
  type Props = Parameters<typeof SectionTabs>[0];
  function renderTabs(props: Partial<Props> = {}, shown = SIDEBAR_PILLS) {
    const handlers = { onChange: vi.fn(), onToggleHideTasks: vi.fn(), onToggleNeedsYouOnly: vi.fn(), onPickPills: vi.fn() };
    render(
      <ShownPillsContext.Provider value={shown}>
        <SectionTabs tab="recent" showTabs recentCount={4} hideTasks={{ recent: false, tree: false }} needsYouOnly={false} {...handlers} {...props} />
      </ShownPillsContext.Provider>,
    );
    return handlers;
  }

  it("shows both tabs with Recent's count and switches tab on click", () => {
    const { onChange } = renderTabs();
    expect(screen.getByRole("tab", { name: /Recent/ }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("tab", { name: "Tree" }).getAttribute("aria-selected")).toBe("false");
    expect(screen.getByTestId("sidebar-tab-recent-count").textContent).toBe("4");
    expect(screen.queryByTestId("sidebar-tab-tree-count")).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Tree" }));
    expect(onChange).toHaveBeenCalledWith("tree");
  });

  it("leaves the tabs out when nothing is recent", () => {
    renderTabs({ showTabs: false, tab: "tree" });
    expect(screen.queryByTestId("sidebar-tabs")).toBeNull();
    expect(screen.getByRole("button", { name: "Hide task threads in Tree" })).toBeTruthy();
  });

  it("toggles the needs-you filter and opens the pill picker on right-click", () => {
    const { onToggleNeedsYouOnly, onPickPills } = renderTabs();
    const bell = screen.getByRole("button", { name: "Show only sessions that need you" });
    expect(bell.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(bell);
    fireEvent.contextMenu(bell);
    expect(onToggleNeedsYouOnly).toHaveBeenCalledOnce();
    expect(onPickPills).toHaveBeenCalledOnce();
  });

  it("shows the pressed needs-you filter and hidden task threads", () => {
    const { onToggleHideTasks } = renderTabs({ needsYouOnly: true, hideTasks: { recent: true, tree: false } });
    const bell = screen.getByRole("button", { name: "Show all sessions" });
    expect(bell.style.color).toBe(css(darkColors.warning));
    const tasks = screen.getByRole("button", { name: "Show task threads in Recent" });
    expect(tasks.getAttribute("aria-pressed")).toBe("true");
    expect(tasks.querySelector("line")).toBeTruthy();
    fireEvent.click(tasks);
    expect(onToggleHideTasks).toHaveBeenCalledOnce();
  });

  it("explains each tab on hover", () => {
    renderTabs({ hideTasks: { recent: true, tree: false } });
    fireEvent.mouseEnter(screen.getByRole("tab", { name: /Recent/ }));
    expect(screen.getByTestId("sidebar-tab-recent-info-sessions").textContent).toBe("4");
    expect(screen.getByTestId("sidebar-tab-recent-info-showing").textContent).toBe("all sessions");
    expect(screen.getByTestId("sidebar-tab-recent-info-task-threads").textContent).toBe("hidden");
    fireEvent.mouseLeave(screen.getByRole("tab", { name: /Recent/ }));
    expect(screen.queryByTestId("sidebar-tab-recent-info")).toBeNull();
    fireEvent.mouseEnter(screen.getByRole("tab", { name: "Tree" }));
    expect(screen.getByTestId("sidebar-tab-tree-info-task-threads").textContent).toBe("shown");
  });

  it("explains the tree's hidden task threads and the needs-you filter", () => {
    renderTabs({ needsYouOnly: true, hideTasks: { recent: false, tree: true } });
    fireEvent.mouseEnter(screen.getByRole("tab", { name: "Tree" }));
    expect(screen.getByTestId("sidebar-tab-tree-info-showing").textContent).toBe("only the sessions that need you");
    expect(screen.getByTestId("sidebar-tab-tree-info-task-threads").textContent).toBe("hidden");
  });

  it("lists every pill on the bell's card, marking those turned off", () => {
    renderTabs(
      {},
      SIDEBAR_PILLS.filter((p) => p.kind !== "ci"),
    );
    fireEvent.mouseEnter(screen.getByRole("button", { name: "Show only sessions that need you" }));
    const card = screen.getByTestId("sidebar-needs-you-info");
    expect(card.textContent).toContain("Showing all sessions");
    expect(card.textContent).toContain("only the sessions waiting on you");
    expect(screen.getByTestId("sidebar-needs-you-info-ci").textContent).toBe("a CI failure · turned off");
    expect(screen.getByTestId("sidebar-needs-you-info-gate").textContent).toBe("an approval");
    expect(card.querySelector<HTMLElement>("[data-pill='ci']")!.style.color).toBe(css(darkColors.textDisabled));
  });

  it("says the bell shows all sessions again when the filter is on", () => {
    renderTabs({ needsYouOnly: true });
    fireEvent.mouseEnter(screen.getByRole("button", { name: "Show all sessions" }));
    expect(screen.getByTestId("sidebar-needs-you-info").textContent).toContain("Click to show all sessions.");
  });

  it.each<[Props["hideTasks"], string, string, string]>([
    [{ recent: false, tree: true }, "hide", "shown", "hidden"],
    [{ recent: true, tree: false }, "show", "hidden", "shown"],
  ])("explains the task filter %o", (hideTasks, verb, recent, tree) => {
    renderTabs({ hideTasks });
    fireEvent.mouseEnter(screen.getByTestId("sidebar-hide-tasks"));
    expect(screen.getByTestId("sidebar-hide-tasks-info").textContent).toContain(`Click to ${verb} them in Recent`);
    expect(screen.getByTestId("sidebar-hide-tasks-info-recent").textContent).toBe(recent);
    expect(screen.getByTestId("sidebar-hide-tasks-info-tree").textContent).toBe(tree);
  });
});

describe("SessionSections", () => {
  afterEach(() => vi.useRealTimers());

  const parent = channel({ id: "p", name: "project" });
  const t1 = channel({ id: "t1", name: "first", parent_id: "p", last_activity_at: 0 });
  const t2 = channel({ id: "t2", name: "second", parent_id: "p" });

  type Props = Parameters<typeof SessionSections>[0];
  function renderSections(props: Partial<Props> = {}, pills: Record<string, PillKind[]> = {}) {
    const onSelect = vi.fn();
    const onContextMenu = vi.fn();
    const byId = new Map<string, Channel>([parent, t1, t2].map((c) => [c.id, c]));
    render(
      <SessionSections
        active={[]}
        recent={[t1, t2]}
        byId={byId}
        selectedId={null}
        isRunning={() => false}
        pillsFor={(id) => pills[id] ?? []}
        isUnread={() => false}
        lastActivity={() => undefined}
        onSelect={onSelect}
        onContextMenu={onContextMenu}
        {...props}
      />,
    );
    return { onSelect, onContextMenu };
  }

  const sessionRows = () => screen.getAllByTestId("sidebar-session-row");

  it("says when no session matches", () => {
    renderSections({ recent: [] });
    expect(screen.getByTestId("sidebar-recent-empty").textContent).toBe("No matching sessions");
  });

  it("lists the recent sessions with where they live, selecting and opening their menu", () => {
    const { onSelect, onContextMenu } = renderSections();
    expect(sessionRows().map((r) => r.dataset.channelId)).toEqual(["t1", "t2"]);
    expect(sessionRows()[0]!.textContent).toContain("project");
    fireEvent.click(sessionRows()[1]!);
    fireEvent.contextMenu(sessionRows()[0]!);
    expect(onSelect).toHaveBeenCalledWith("t2");
    expect(onContextMenu).toHaveBeenCalledWith(expect.anything(), t1);
  });

  it("shows a channel without a parents line", () => {
    renderSections({ recent: [parent] });
    expect(sessionRows()[0]!.textContent).toBe("#project");
  });

  it("shows each inactive session's age, kept current every minute, but none for an active one", () => {
    vi.useFakeTimers();
    const now = Date.now();
    renderSections({ active: [t1], lastActivity: () => now - 5 * 60_000 });
    expect(sessionRows()[0]!.textContent).not.toMatch(/\dm$/);
    expect(sessionRows()[1]!.textContent).toMatch(/5m$/);
    act(() => vi.advanceTimersByTime(60_000));
    expect(sessionRows()[1]!.textContent).toMatch(/6m$/);
  });

  it("styles the selected, unread and running rows", () => {
    renderSections({ selectedId: "t1", isUnread: () => true, isRunning: (id) => id === "t2" });
    const [first, second] = sessionRows();
    expect(first!.style.background).toBe(css(darkColors.selectedBg));
    expect(screen.getByText("first").style.fontWeight).toBe("600");
    expect(screen.getAllByTestId("session-running")).toHaveLength(1);
    expect(second!.querySelector("[data-testid='session-running']")).toBeTruthy();
    // The unread dot shows only on the row that isn't open.
    const dots = (r: HTMLElement) => [...r.querySelectorAll<HTMLElement>("span")].filter((s) => s.style.backgroundColor === css("#5b9cf5"));
    expect(dots(first!)).toHaveLength(0);
    expect(dots(second!)).toHaveLength(1);
  });

  it("highlights a row under the pointer", () => {
    renderSections();
    fireEvent.mouseEnter(sessionRows()[0]!);
    expect(sessionRows()[0]!.style.background).toBe(css(darkColors.hoverBg));
    fireEvent.mouseLeave(sessionRows()[0]!);
    expect(sessionRows()[0]!.style.background).toBe("transparent");
  });

  it.each<[Partial<Channel>, string]>([
    [{ diff_additions: 3 }, "+3"],
    [{ diff_deletions: 2 }, "-2"],
    [{ diff_additions: 3, diff_deletions: 2 }, "+3 -2"],
  ])("shows the diff %o", (fields, text) => {
    renderSections({ recent: [{ ...t1, ...fields }] });
    expect(screen.getByTestId("sidebar-session-diff").textContent).toBe(text);
  });

  it("shows the lit pills, review and fixing pills, and no age beside a pill", () => {
    renderSections({ recent: [{ ...t1, review_running: true, pr_attention: "fixing" }], lastActivity: () => Date.now() - 2 * 60_000 }, { t1: ["gate"] });
    const labels = [...sessionRows()[0]!.querySelectorAll<HTMLElement>("[data-pill]")].map((p) => p.dataset.pill);
    expect(labels).toEqual(["gate", "rev…", "ci…"]);
    expect(sessionRows()[0]!.textContent).not.toContain("2m");
  });
});
