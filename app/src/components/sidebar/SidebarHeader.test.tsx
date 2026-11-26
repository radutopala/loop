import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { expectHover } from "../../test/chat-bits-sidebar-chrome-hover";
import { darkColors as c } from "../../theme";
import { SidebarHeader } from "./SidebarHeader";

type Props = Parameters<typeof SidebarHeader>[0];

function setup(over: Partial<Props> = {}) {
  const props: Props = {
    searchQuery: "",
    onSearchQueryChange: vi.fn(),
    selectMode: false,
    selectedCount: 0,
    onBatchDelete: vi.fn(),
    onEnterSelectMode: vi.fn(),
    onCancelSelectMode: vi.fn(),
    onNewProject: vi.fn(),
    creatingChannel: false,
    newChannelName: "",
    onNewChannelNameChange: vi.fn(),
    onCreateChannel: vi.fn(),
    onCancelCreateChannel: vi.fn(),
    newChannelInputRef: createRef<HTMLInputElement>(),
    ...over,
  };
  const user = userEvent.setup();
  const view = render(<SidebarHeader {...props} />);
  return { props, user, ...view };
}

const byTitle = (t: string) => screen.getByTitle(t) as HTMLButtonElement;
const lit = { backgroundColor: c.hoverBg, color: c.textLight };
const dim = { backgroundColor: "transparent", color: c.textDim };

describe("SidebarHeader", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  describe("toolbar", () => {
    it("offers Select and + new, without Mark read when nothing is unread", async () => {
      const { props, user } = setup({ unreadCount: 0 });
      expect(screen.getByText("Channels")).toBeTruthy();
      expect(screen.queryByTitle("Mark all as read")).toBeNull();
      expectHover(byTitle("Select channels to delete"), lit, dim);
      expectHover(byTitle("New channel"), lit, dim);
      await user.click(byTitle("Select channels to delete"));
      expect(props.onEnterSelectMode).toHaveBeenCalledOnce();
    });

    it("marks everything read when something is unread", async () => {
      const onMarkAllRead = vi.fn();
      const { user } = setup({ unreadCount: 2, onMarkAllRead });
      const btn = byTitle("Mark all as read");
      expectHover(btn, lit, dim);
      await user.click(btn);
      expect(onMarkAllRead).toHaveBeenCalledOnce();
    });

    it("tolerates Mark read without a handler", async () => {
      const { user } = setup({ unreadCount: 1 });
      await user.click(byTitle("Mark all as read"));
      expect(byTitle("Mark all as read")).toBeTruthy();
    });

    it("in select mode offers Cancel, and Delete once something is selected", async () => {
      const { props, user, rerender } = setup({ selectMode: true, unreadCount: 3 });
      expect(screen.queryByTitle("Mark all as read")).toBeNull();
      expect(screen.queryByTitle("New channel")).toBeNull();
      expect(screen.queryByText(/Delete/)).toBeNull();
      const cancel = byTitle("Cancel selection");
      expectHover(cancel, lit, dim);
      await user.click(cancel);
      expect(props.onCancelSelectMode).toHaveBeenCalledOnce();

      rerender(<SidebarHeader {...props} selectMode selectedCount={2} />);
      const del = byTitle("Delete 2 selected");
      expect(del.textContent).toBe("Delete (2)");
      expectHover(del, { backgroundColor: c.hoverBg, color: c.error }, dim);
      await user.click(del);
      expect(props.onBatchDelete).toHaveBeenCalledOnce();
    });
  });

  describe("new menu", () => {
    it("toggles with + new and starts a new project", async () => {
      const { props, user } = setup();
      await user.click(byTitle("New channel"));
      expect(screen.queryByText("Open directory...")).toBeNull();
      // A bare click toggles it shut.
      fireEvent.click(byTitle("New channel"));
      expect(screen.queryByText("New project")).toBeNull();
      await user.click(byTitle("New channel"));
      // A real click on + new while open closes it on mousedown, then reopens it.
      await user.click(byTitle("New channel"));
      const item = screen.getByText("New project");
      expectHover(item, { backgroundColor: c.hoverBg }, { backgroundColor: "transparent" });
      await user.click(item);
      expect(props.onNewProject).toHaveBeenCalledOnce();
      expect(screen.queryByText("New project")).toBeNull();
    });

    it("closes on a left click outside, not on a right click or one inside", async () => {
      const { user } = setup();
      await user.click(byTitle("New channel"));
      fireEvent.mouseDown(document.body, { button: 2 });
      fireEvent.mouseDown(screen.getByText("New project"));
      expect(screen.getByText("New project")).toBeTruthy();
      fireEvent.mouseDown(document.body, { button: 0 });
      expect(screen.queryByText("New project")).toBeNull();
    });

    it("opens a picked directory", async () => {
      const onOpenDirectory = vi.fn();
      vi.stubGlobal("loopAPI", { showOpenDirectoryDialog: vi.fn().mockResolvedValue("/work/repo") });
      const { user } = setup({ onOpenDirectory });
      await user.click(byTitle("New channel"));
      const item = screen.getByText("Open directory...");
      expectHover(item, { backgroundColor: c.hoverBg }, { backgroundColor: "transparent" });
      await user.click(item);
      expect(onOpenDirectory).toHaveBeenCalledWith("/work/repo");
      expect(screen.queryByText("Open directory...")).toBeNull();
    });

    it.each([
      ["a cancelled dialog", { showOpenDirectoryDialog: vi.fn().mockResolvedValue(null) }],
      ["no dialog in the bridge", {}],
      ["no bridge", undefined],
    ])("opens nothing for %s", async (_, bridge) => {
      const onOpenDirectory = vi.fn();
      vi.stubGlobal("loopAPI", bridge);
      const { user } = setup({ onOpenDirectory });
      await user.click(byTitle("New channel"));
      await user.click(screen.getByText("Open directory..."));
      expect(onOpenDirectory).not.toHaveBeenCalled();
    });
  });

  describe("search", () => {
    it("reports typing, and clears on Escape", () => {
      const { props } = setup({ searchQuery: "ab" });
      const input = screen.getByPlaceholderText("Search...");
      fireEvent.change(input, { target: { value: "abc" } });
      expect(props.onSearchQueryChange).toHaveBeenCalledWith("abc");
      fireEvent.keyDown(input, { key: "a" });
      expect(props.onSearchQueryChange).toHaveBeenCalledTimes(1);
      const ev = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
      act(() => {
        input.dispatchEvent(ev);
      });
      expect(ev.defaultPrevented).toBe(true);
      expect(props.onSearchQueryChange).toHaveBeenLastCalledWith("");
    });

    it("has a clear button only with a query, which keeps focus in the input", async () => {
      const { props, user, rerender } = setup();
      expect(screen.queryByTestId("sidebar-search-clear")).toBeNull();
      rerender(<SidebarHeader {...props} searchQuery="foo" />);
      const clear = screen.getByRole("button", { name: "Clear search" });
      expectHover(clear, lit, dim);
      const down = new MouseEvent("mousedown", { bubbles: true, cancelable: true });
      clear.dispatchEvent(down);
      expect(down.defaultPrevented).toBe(true);
      await user.click(clear);
      expect(props.onSearchQueryChange).toHaveBeenCalledWith("");
      expect(document.activeElement).toBe(screen.getByPlaceholderText("Search..."));
    });
  });

  it("explains the search and its tag:x filter on hover", () => {
    setup();
    const box = screen.getByPlaceholderText("Search...").parentElement!;
    expect(screen.queryByTestId("sidebar-search-info")).toBeNull();
    fireEvent.mouseEnter(box);
    expect(screen.getByTestId("sidebar-search-info-title").textContent).toBe("Search");
    expect(screen.getByTestId("sidebar-search-info").textContent).toContain("Esc clears it.");
    expect(screen.getByTestId("sidebar-search-info-tag").textContent).toBe("only the rows carrying that tag");
    fireEvent.mouseLeave(box);
    expect(screen.queryByTestId("sidebar-search-info")).toBeNull();
  });

  describe("new channel input", () => {
    it("shows only while creating, wired to the given ref", () => {
      const ref = createRef<HTMLInputElement>();
      const { props, rerender } = setup({ newChannelInputRef: ref });
      expect(screen.queryByPlaceholderText("Channel name...")).toBeNull();
      rerender(<SidebarHeader {...props} creatingChannel newChannelName="x" />);
      expect(ref.current).toBe(screen.getByPlaceholderText("Channel name..."));
    });

    it("reports typing and creates the trimmed name on Enter", () => {
      const { props } = setup({ creatingChannel: true, newChannelName: "  api  " });
      const input = screen.getByPlaceholderText("Channel name...");
      fireEvent.change(input, { target: { value: "api2" } });
      expect(props.onNewChannelNameChange).toHaveBeenCalledWith("api2");
      fireEvent.keyDown(input, { key: "Enter" });
      expect(props.onCreateChannel).toHaveBeenCalledWith("api");
    });

    it("ignores Enter on a blank name and other keys", () => {
      const { props } = setup({ creatingChannel: true, newChannelName: "   " });
      const input = screen.getByPlaceholderText("Channel name...");
      fireEvent.keyDown(input, { key: "Enter" });
      fireEvent.keyDown(input, { key: "a" });
      expect(props.onCreateChannel).not.toHaveBeenCalled();
      expect(props.onCancelCreateChannel).not.toHaveBeenCalled();
    });

    it("cancels on Escape and on blur", () => {
      const { props } = setup({ creatingChannel: true, newChannelName: "x" });
      const input = screen.getByPlaceholderText("Channel name...");
      fireEvent.keyDown(input, { key: "Escape" });
      expect(props.onCancelCreateChannel).toHaveBeenCalledTimes(1);
      fireEvent.blur(input);
      expect(props.onCancelCreateChannel).toHaveBeenCalledTimes(2);
      expect(props.onCreateChannel).not.toHaveBeenCalled();
    });
  });
});
