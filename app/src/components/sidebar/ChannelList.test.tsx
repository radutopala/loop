import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { channel } from "../../test/chat-bits-sidebar-list-channel";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import { ChannelList } from "./ChannelList";

vi.mock("../../api/channels", () => ({ forkThread: vi.fn() }));
vi.mock("../../api/git", () => ({ fetchPR: vi.fn().mockResolvedValue({ present: false }) }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn().mockResolvedValue(null) }));

type Props = Parameters<typeof ChannelList>[0];

const dm = channel({ id: "dm1", name: "dm" });
const a = channel({ id: "a", name: "alpha" });
const b = channel({ id: "b", name: "beta" });
const threads: Record<string, Channel[]> = { a: [channel({ id: "t", name: "thread", parent_id: "a" })] };

function renderList(props: Partial<Props> = {}) {
  const onSelect = vi.fn();
  const getFilteredThreads = vi.fn((id: string) => threads[id] ?? []);
  render(
    <ChannelList
      dmChannel={undefined}
      topLevel={[a, b]}
      selectedId={null}
      onSelect={onSelect}
      onCreateThread={vi.fn()}
      onContextMenu={vi.fn()}
      onDragStart={vi.fn()}
      onDragOver={vi.fn()}
      onDrop={vi.fn()}
      onDragEnd={vi.fn()}
      dragOverId={null}
      getFilteredThreads={getFilteredThreads}
      threadsByParent={threads}
      selectMode={false}
      checkedIds={new Set()}
      onToggleCheck={vi.fn()}
      {...props}
    />,
  );
  return { onSelect, getFilteredThreads };
}

const rows = () => screen.getAllByTestId("sidebar-channel-row");

describe("ChannelList", () => {
  it("lists the top-level channels with their filtered threads", () => {
    const { getFilteredThreads } = renderList();
    expect(rows().map((r) => r.textContent)).toEqual(["#alpha", "#beta"]);
    expect(screen.getByText("thread")).toBeTruthy();
    expect(getFilteredThreads).toHaveBeenCalledWith("a");
    expect(getFilteredThreads).toHaveBeenCalledWith("b");
  });

  it("pins the DM first, selected and marked as the drop target", () => {
    renderList({ dmChannel: dm, selectedId: "dm1", dragOverId: "dm1", selectMode: true });
    expect(rows()).toHaveLength(3);
    expect(rows()[0]!.textContent).toBe("#dm");
    expect(rows()[0]!.style.backgroundColor).toBe(css(darkColors.selectedBg));
    expect(rows()[0]!.style.borderTop).toContain(css(darkColors.active));
    // The pinned DM can't be picked for deletion; the others can.
    expect(rows()[0]!.querySelector("span[style*='width: 12px']")).toBeNull();
    expect(rows()[1]!.querySelector("span[style*='width: 12px']")).toBeTruthy();
  });

  it("marks the selected channel and the drop target among the others", () => {
    const { onSelect } = renderList({ selectedId: "b", dragOverId: "a" });
    expect(rows()[1]!.style.backgroundColor).toBe(css(darkColors.selectedBg));
    expect(rows()[0]!.style.borderTop).toContain(css(darkColors.active));
    fireEvent.click(screen.getByText("alpha"));
    expect(onSelect).toHaveBeenCalledWith("a");
  });
});
