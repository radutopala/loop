import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { forkThread } from "../../api/channels";
import { setGlobalConfigValue } from "../../api/configApi";
import { ThemeProvider } from "../../ThemeContext";
import { channel } from "../../test/chat-bits-sidebar-list-channel";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import { openExternalUrl } from "../../utils/openExternal";
import type { PillKind } from "./pills";
import { Sidebar } from "./Sidebar";

vi.mock("../../api/channels", () => ({ forkThread: vi.fn() }));
vi.mock("../../api/configApi", () => ({ setGlobalConfigValue: vi.fn() }));
vi.mock("../../api/git", () => ({ fetchPR: vi.fn().mockResolvedValue({ present: false }), fetchBranches: vi.fn().mockResolvedValue({ branches: [], current: "" }) }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn().mockResolvedValue(null) }));
vi.mock("../../utils/openExternal", () => ({ openExternalUrl: vi.fn() }));

const forkMock = vi.mocked(forkThread);
const configMock = vi.mocked(setGlobalConfigValue);
const openMock = vi.mocked(openExternalUrl);

const dm = channel({ id: "dm", name: "dm" });
const alpha = channel({ id: "a", name: "alpha", dir_path: "/src/alpha" });
const beta = channel({ id: "b", name: "beta" });
const nameless = channel({ id: "x", name: "" });
const a1 = channel({ id: "a1", name: "one", parent_id: "a" });
const a2 = channel({ id: "a2", name: "two", parent_id: "a", worktree: true });
const a3 = channel({ id: "a3", name: "three", parent_id: "a" });
const b1 = channel({ id: "b1", name: "bee", parent_id: "b" });
const sub = channel({ id: "s1", name: "deep", parent_id: "a1" });
const all = [dm, alpha, beta, nameless, a1, a2, a3, b1, sub];

type Props = Parameters<typeof Sidebar>[0];

function pillsRef(entries: [PillKind, string[]][]) {
  return { current: new Map(entries.map(([k, ids]) => [k, new Set(ids)])) };
}

function setup(props: Partial<Props> = {}, theme: { initialIslands?: boolean; initialHiddenPills?: string[] } = {}) {
  const handlers = {
    onSelect: vi.fn(),
    onCreateChannel: vi.fn(),
    onCreateThread: vi.fn(),
    onDeleteThread: vi.fn(),
  };
  const user = userEvent.setup();
  const ui = (p: Partial<Props>): ReactElement => (
    <ThemeProvider {...theme}>
      <Sidebar channels={all} selectedId={null} collapsed={false} {...handlers} {...props} {...p} />
    </ThemeProvider>
  );
  const view = render(ui({}));
  return { ...view, ...handlers, user, rerender: (p: Partial<Props>) => view.rerender(ui(p)) };
}

const channelRowNames = () => screen.queryAllByTestId("sidebar-channel-row").map((r) => (r.textContent ?? "").replace(/^#/, ""));
const threadNames = () => screen.queryAllByTestId("sidebar-thread-row").map((r) => r.textContent);
const channelRow = (name: string) => screen.getAllByTestId("sidebar-channel-row").find((r) => r.textContent === `#${name}`)!;
const threadRow = (name: string) => screen.getAllByTestId("sidebar-thread-row").find((r) => r.textContent?.startsWith(name))!;
const threadButton = (name: string) => threadRow(name).querySelector("button")!;
const menuLabels = () => [...screen.getByTestId("context-menu").querySelectorAll<HTMLElement>("button[data-label]")].map((b) => b.dataset.label);
const recent = (fields: Partial<Channel> = {}) => ({ last_activity_at: Date.now() - 60_000, ...fields });

describe("Sidebar", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    configMock.mockResolvedValue(undefined as never);
  });
  afterEach(() => vi.restoreAllMocks());

  it("renders nothing when collapsed", () => {
    setup({ collapsed: true });
    expect(screen.queryByTestId("sidebar")).toBeNull();
  });

  it("pins the DM above the named channels and their threads", () => {
    setup();
    expect(channelRowNames()).toEqual(["dm", "alpha", "beta"]);
    expect(threadNames()).toEqual(["one", "deep", "two", "three", "bee"]);
  });

  it("collapses itself from its toggle", () => {
    const onToggleSidebar = vi.fn();
    setup({ onToggleSidebar });
    fireEvent.click(screen.getByTitle("Collapse sidebar"));
    expect(onToggleSidebar).toHaveBeenCalledOnce();
  });

  it("starts at the saved width and borders a theme without islands", () => {
    localStorage.setItem("loop-sidebar-width", "300");
    setup({}, { initialIslands: false });
    const bar = screen.getByTestId("sidebar");
    expect(bar.style.width).toBe("300px");
    expect(bar.style.borderRight).toContain("1px solid");
  });

  it("drags its edge to resize, within bounds, and saves the width", () => {
    setup();
    const bar = screen.getByTestId("sidebar");
    const handle = [...bar.querySelectorAll<HTMLElement>("div")].find((d) => d.style.cursor === "col-resize")!;
    fireEvent.mouseDown(handle, { clientX: 280 });
    expect(bar.style.userSelect).toBe("none");
    fireEvent.mouseMove(document, { clientX: 0 });
    expect(bar.style.width).toBe("180px");
    fireEvent.mouseMove(document, { clientX: 290 });
    expect(bar.style.width).toBe("256px");
    fireEvent.mouseUp(document);
    expect(localStorage.getItem("loop-sidebar-width")).toBe("256");
    expect(bar.style.userSelect).toBe("");
    fireEvent.mouseMove(document, { clientX: 0 });
    expect(bar.style.width).toBe("256px");
  });

  it("creates a channel from the new project input, and cancels it on Escape", async () => {
    const { user, onCreateChannel } = setup();
    await user.click(screen.getByTitle("New channel"));
    await user.click(screen.getByText("New project"));
    const input = screen.getByPlaceholderText("Channel name...");
    await waitFor(() => expect(document.activeElement).toBe(input));
    await user.type(input, "fresh{Enter}");
    expect(onCreateChannel).toHaveBeenCalledWith("fresh");
    expect(screen.queryByPlaceholderText("Channel name...")).toBeNull();
    await user.click(screen.getByTitle("New channel"));
    await user.click(screen.getByText("New project"));
    await user.type(screen.getByPlaceholderText("Channel name..."), "x{Escape}");
    expect(screen.queryByPlaceholderText("Channel name...")).toBeNull();
    expect(onCreateChannel).toHaveBeenCalledOnce();
  });

  describe("search", () => {
    it("keeps a matching channel with all its threads", async () => {
      const { user } = setup();
      await user.type(screen.getByPlaceholderText("Search..."), "ALPHA");
      // The pinned DM stays through a search.
      expect(channelRowNames()).toEqual(["dm", "alpha"]);
      expect(threadNames()).toEqual(["one", "deep", "two", "three"]);
    });

    it("keeps a channel for a matching thread, with only that thread", async () => {
      const { user } = setup({ channels: [...all, channel({ id: "g", name: "gamma" })] });
      await user.type(screen.getByPlaceholderText("Search..."), "three");
      expect(channelRowNames()).toEqual(["dm", "alpha"]);
      expect(threadNames()).toEqual(["three"]);
    });

    it("keeps a thread for a matching sub-thread", async () => {
      const { user } = setup();
      await user.type(screen.getByPlaceholderText("Search..."), "deep");
      expect(channelRowNames()).toEqual(["dm", "alpha"]);
      expect(threadNames()).toEqual(["one", "deep"]);
    });

    it("filters by tag:x, every tag and the text alike", async () => {
      const channels = all.map((c) => (c.id === "a3" ? { ...c, tags: ["api", "web"] } : c.id === "b" ? { ...c, tags: ["api"] } : c));
      const { user } = setup({ channels });
      const search = screen.getByPlaceholderText("Search...");
      await user.type(search, "tag:api");
      // The untagged DM goes, unlike in a text search.
      expect(channelRowNames()).toEqual(["alpha", "beta"]);
      // A tagged channel keeps all its threads, as a matching one does.
      expect(threadNames()).toEqual(["three", "bee"]);
      await user.type(search, " tag:web");
      expect(channelRowNames()).toEqual(["alpha"]);
      await user.type(search, " nothing");
      expect(channelRowNames()).toEqual([]);
    });

    it("keeps the DM for a tag:x a thread in it carries, with only that thread", async () => {
      const dmThreads = [channel({ id: "dt", name: "tagged", parent_id: "dm", tags: ["api"] }), channel({ id: "du", name: "plain", parent_id: "dm" })];
      const { user } = setup({ channels: [...all, ...dmThreads] });
      await user.type(screen.getByPlaceholderText("Search..."), "tag:api");
      expect(channelRowNames()).toEqual(["dm"]);
      expect(threadNames()).toEqual(["tagged"]);
    });

    it("keeps all the DM's threads when the DM matches", async () => {
      const dmThreads = [channel({ id: "dt", name: "first", parent_id: "dm" }), channel({ id: "du", name: "second", parent_id: "dm" })];
      const { user } = setup({ channels: [...all, ...dmThreads] });
      await user.type(screen.getByPlaceholderText("Search..."), "dm");
      expect(channelRowNames()).toEqual(["dm"]);
      expect(threadNames()).toEqual(["first", "second"]);
    });
  });

  describe("channel order", () => {
    it("sorts by the saved order, unknown channels last", () => {
      localStorage.setItem("loop-channel-order", JSON.stringify(["b"]));
      setup();
      expect(channelRowNames()).toEqual(["dm", "beta", "alpha"]);
    });

    it("moves a dragged channel to where it's dropped and saves the order", () => {
      setup();
      const drag = (from: string, to: string) => {
        const src = channelRow(from).parentElement!;
        const dst = channelRow(to).parentElement!;
        fireEvent.dragStart(src);
        fireEvent.dragOver(dst);
        expect(channelRow(to).style.borderTop).toContain(css(darkColors.active));
        fireEvent.drop(dst);
      };
      drag("beta", "alpha");
      expect(channelRowNames()).toEqual(["dm", "beta", "alpha"]);
      expect(JSON.parse(localStorage.getItem("loop-channel-order")!)).toEqual(["dm", "b", "a"]);
      expect(channelRow("alpha").style.borderTop).toBe("2px solid transparent");
    });

    it("ignores a drop on itself and a drag that ended", () => {
      setup();
      const row = (n: string) => channelRow(n).parentElement!;
      fireEvent.dragStart(row("alpha"));
      fireEvent.drop(row("alpha"));
      fireEvent.dragStart(row("beta"));
      fireEvent.dragOver(row("alpha"));
      fireEvent.dragEnd(row("beta"));
      expect(channelRow("alpha").style.borderTop).toBe("2px solid transparent");
      // With no drag under way, a drag over marks nothing and a drop moves nothing.
      fireEvent.dragOver(row("alpha"));
      expect(channelRow("alpha").style.borderTop).toBe("2px solid transparent");
      fireEvent.drop(row("alpha"));
      expect(channelRowNames()).toEqual(["dm", "alpha", "beta"]);
      expect(localStorage.getItem("loop-channel-order")).toBeNull();
    });

    it("drops nothing when the dragged channel is gone before the drop", () => {
      const { rerender } = setup();
      fireEvent.dragStart(channelRow("beta").parentElement!);
      rerender({ channels: all.filter((c) => c.id !== "b") });
      fireEvent.drop(channelRow("alpha").parentElement!);
      expect(localStorage.getItem("loop-channel-order")).toBeNull();
    });
  });

  describe("thread order", () => {
    it("sorts threads by their parent's saved order", () => {
      localStorage.setItem("loop-thread-order", JSON.stringify({ a: ["a3", "a1"], b: [] }));
      setup();
      expect(threadNames()).toEqual(["three", "one", "deep", "two", "bee"]);
    });

    it("moves a thread among its siblings and saves the order", () => {
      setup();
      fireEvent.dragStart(threadButton("three"));
      fireEvent.dragOver(threadButton("one"));
      expect(threadRow("one").style.boxShadow).toContain("inset");
      fireEvent.drop(threadButton("one"));
      expect(threadNames()).toEqual(["three", "one", "deep", "two", "bee"]);
      expect(JSON.parse(localStorage.getItem("loop-thread-order")!)).toEqual({ a: ["a3", "a1", "a2"] });
      expect(threadRow("one").style.boxShadow).toBe("");
    });

    it("refuses a drop under another parent or on itself, and clears on drag end", () => {
      setup();
      fireEvent.dragStart(threadButton("three"));
      fireEvent.dragOver(threadButton("bee"));
      expect(threadRow("bee").style.boxShadow).toBe("");
      fireEvent.drop(threadButton("bee"));
      fireEvent.dragStart(threadButton("three"));
      fireEvent.drop(threadButton("three"));
      fireEvent.dragStart(threadButton("three"));
      fireEvent.dragOver(threadButton("one"));
      fireEvent.dragEnd(threadButton("three"));
      expect(threadRow("one").style.boxShadow).toBe("");
      // No thread drag under way: a drag over marks nothing.
      fireEvent.dragOver(threadButton("one"));
      expect(threadRow("one").style.boxShadow).toBe("");
      expect(localStorage.getItem("loop-thread-order")).toBeNull();
    });

    it("lets a channel dropped on a thread row reach the channel", () => {
      setup();
      fireEvent.dragStart(channelRow("beta").parentElement!);
      fireEvent.drop(threadButton("one"));
      expect(channelRowNames()).toEqual(["dm", "beta", "alpha"]);
    });

    it("drops nothing when the dragged thread is gone before the drop", () => {
      const { rerender } = setup();
      fireEvent.dragStart(threadButton("three"));
      rerender({ channels: all.filter((c) => c.id !== "a3") });
      fireEvent.drop(threadButton("one"));
      expect(localStorage.getItem("loop-thread-order")).toBeNull();
    });
  });

  describe("context menu", () => {
    const open = (row: HTMLElement) => fireEvent.contextMenu(row, { clientX: 10, clientY: 20 });

    it("offers a channel's copy, ticket, lock and delete actions", async () => {
      const onSetTicketURL = vi.fn();
      const onSetLocked = vi.fn();
      const { user, onDeleteThread } = setup({ onSetTicketURL, onSetLocked, onRenameThread: vi.fn(), onSetDescription: vi.fn() });
      open(channelRow("alpha"));
      expect(menuLabels()).toEqual(["Copy Link", "Copy Channel ID", "Edit Ticket", "Lock", "Delete Channel"]);
      await user.click(screen.getByRole("menuitem", { name: "Copy Link" }));
      expect(await navigator.clipboard.readText()).toBe("loop://channel/a");
      open(channelRow("alpha"));
      await user.click(screen.getByRole("menuitem", { name: "Copy Channel ID" }));
      expect(await navigator.clipboard.readText()).toBe("a");
      open(channelRow("alpha"));
      await user.click(screen.getByRole("menuitem", { name: "Lock" }));
      expect(onSetLocked).toHaveBeenCalledWith("a", true);
      open(channelRow("alpha"));
      await user.click(screen.getByRole("menuitem", { name: "Delete Channel" }));
      expect(screen.getByTestId("context-menu-confirm").textContent).toContain("Delete this channel?");
      await user.click(screen.getByTestId("confirm-yes"));
      expect(onDeleteThread).toHaveBeenCalledWith("a");
    });

    it("gives the DM only its copy actions", () => {
      setup({ onSetTicketURL: vi.fn(), onSetLocked: vi.fn() });
      open(channelRow("dm"));
      expect(menuLabels()).toEqual(["Copy Link", "Copy Channel ID"]);
    });

    it("offers a thread's actions and opens its ticket", async () => {
      const channels = all.map((c) => (c.id === "a1" ? { ...c, ticket_url: " https://tickets.example/1 " } : c));
      const { user } = setup({ channels, onRenameThread: vi.fn(), onSetDescription: vi.fn(), onSetTicketURL: vi.fn() });
      open(threadButton("one"));
      expect(menuLabels()).toEqual(["Copy Link", "Copy Thread ID", "Open Ticket", "Fork Thread", "Rename Thread", "Edit Description", "Edit Ticket", "Delete Thread"]);
      await user.click(screen.getByRole("menuitem", { name: "Open Ticket" }));
      expect(openMock).toHaveBeenCalledWith("https://tickets.example/1");
    });

    it("hides rename and delete on a locked worktree, and offers unlock", async () => {
      const onSetLocked = vi.fn();
      const channels = all.map((c) => (c.id === "a2" ? { ...c, locked: true } : c));
      const { user } = setup({ channels, onRenameThread: vi.fn(), onSetLocked });
      open(threadButton("two"));
      expect(menuLabels()).toEqual(["Copy Link", "Copy Thread ID", "Fork Worktree", "Unlock"]);
      await user.click(screen.getByRole("menuitem", { name: "Unlock" }));
      expect(onSetLocked).toHaveBeenCalledWith("a2", false);
    });

    it("forks a thread and opens the fork, logging a failure", async () => {
      forkMock.mockResolvedValueOnce("new").mockRejectedValueOnce(new Error("nope"));
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const { user, onSelect } = setup();
      open(threadButton("one"));
      await user.click(screen.getByRole("menuitem", { name: "Fork Thread" }));
      await waitFor(() => expect(onSelect).toHaveBeenCalledWith("new"));
      open(threadButton("two"));
      await user.click(screen.getByRole("menuitem", { name: "Fork Worktree" }));
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[sidebar] fork thread failed:", expect.any(Error)));
      expect(forkMock).toHaveBeenNthCalledWith(2, "a2");
    });

    it("renames a thread through its dialog, or cancels it", async () => {
      const onRenameThread = vi.fn();
      const { user } = setup({ onRenameThread });
      open(threadButton("two"));
      await user.click(screen.getByRole("menuitem", { name: "Rename Worktree" }));
      expect(screen.getByTestId("rename-thread-dialog").textContent).toContain("Rename Worktree");
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      expect(screen.queryByTestId("rename-thread-dialog")).toBeNull();
      open(threadButton("one"));
      await user.click(screen.getByRole("menuitem", { name: "Rename Thread" }));
      const input = screen.getByTestId("rename-thread-input");
      await user.clear(input);
      await user.type(input, "renamed{Enter}");
      expect(onRenameThread).toHaveBeenCalledWith("a1", "renamed");
      expect(screen.queryByTestId("rename-thread-dialog")).toBeNull();
    });

    it("edits a thread's description, or cancels it", async () => {
      const onSetDescription = vi.fn();
      const channels = all.map((c) => (c.id === "a3" ? { ...c, description: "old" } : c));
      const { user } = setup({ channels, onSetDescription });
      open(threadButton("one"));
      await user.click(screen.getByRole("menuitem", { name: "Edit Description" }));
      expect((screen.getByTestId("description-input") as HTMLTextAreaElement).value).toBe("");
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      open(threadButton("three"));
      await user.click(screen.getByRole("menuitem", { name: "Edit Description" }));
      const input = screen.getByTestId("description-input") as HTMLTextAreaElement;
      expect(input.value).toBe("old");
      await user.type(input, " new{Enter}");
      expect(onSetDescription).toHaveBeenCalledWith("a3", "old new");
      expect(screen.queryByTestId("description-input")).toBeNull();
    });

    it("edits a thread's tags from its menu, or cancels it", async () => {
      const onEditTags = vi.fn();
      const loadTags = vi.fn(() => Promise.resolve([{ tag: "api", count: 1 }]));
      const channels = all.map((c) => (c.id === "a1" ? { ...c, tags: ["web"] } : c));
      const { user } = setup({ channels, onEditTags, loadTags });
      open(channelRow("alpha"));
      await user.click(screen.getByRole("menuitem", { name: "Edit Tags" }));
      expect(screen.getByTestId("tags-current").textContent).toBe("No tags");
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      expect(screen.queryByTestId("tags-dialog")).toBeNull();
      open(threadButton("one"));
      await user.click(screen.getByRole("menuitem", { name: "Edit Tags" }));
      await user.click(await screen.findByTestId("tags-pick-api"));
      await user.click(screen.getByTestId("tags-remove-web"));
      await user.click(screen.getByTestId("tags-submit"));
      expect(onEditTags).toHaveBeenCalledWith("a1", ["api"], ["web"]);
      expect(screen.queryByTestId("tags-dialog")).toBeNull();
      // The DM has none to edit.
      open(channelRow("dm"));
      expect(menuLabels()).toEqual(["Copy Link", "Copy Channel ID"]);
    });

    it("edits a channel's ticket, or cancels it", async () => {
      const onSetTicketURL = vi.fn();
      const channels = all.map((c) => (c.id === "b" ? { ...c, ticket_url: "https://tickets.example/2" } : c));
      const { user } = setup({ channels, onSetTicketURL });
      open(channelRow("alpha"));
      await user.click(screen.getByRole("menuitem", { name: "Edit Ticket" }));
      expect((screen.getByTestId("ticket-input") as HTMLInputElement).value).toBe("");
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      open(channelRow("beta"));
      await user.click(screen.getByRole("menuitem", { name: "Edit Ticket" }));
      const input = screen.getByTestId("ticket-input") as HTMLInputElement;
      expect(input.value).toBe("https://tickets.example/2");
      await user.clear(input);
      await user.type(input, "https://tickets.example/3{Enter}");
      expect(onSetTicketURL).toHaveBeenCalledWith("b", "https://tickets.example/3");
      expect(screen.queryByTestId("ticket-input")).toBeNull();
    });
  });

  describe("select mode", () => {
    it("picks channels and threads and deletes them in a batch", async () => {
      const onDeleteBatch = vi.fn();
      const { user } = setup({ onDeleteBatch });
      await user.click(screen.getByTitle("Select channels to delete"));
      const box = (row: HTMLElement) => row.querySelector<HTMLElement>("span[style*='cursor: pointer']")!;
      fireEvent.click(box(channelRow("beta")));
      fireEvent.click(box(threadButton("one")));
      fireEvent.click(box(threadButton("two")));
      fireEvent.click(box(threadButton("two")));
      await user.click(screen.getByTitle("Delete 2 selected"));
      expect(onDeleteBatch).toHaveBeenCalledWith(["b", "a1"]);
      expect(screen.getByTitle("Select channels to delete")).toBeTruthy();
    });

    it("leaves select mode on delete without a batch handler", async () => {
      const { user } = setup();
      await user.click(screen.getByTitle("Select channels to delete"));
      fireEvent.click(channelRow("beta").querySelector("span")!);
      await user.click(screen.getByTitle("Delete 1 selected"));
      expect(screen.getByTitle("Select channels to delete")).toBeTruthy();
    });

    it("cancels picking and clears the picks", async () => {
      const { user } = setup();
      await user.click(screen.getByTitle("Select channels to delete"));
      fireEvent.click(channelRow("beta").querySelector("span")!);
      await user.click(screen.getByTitle("Cancel selection"));
      await user.click(screen.getByTitle("Select channels to delete"));
      expect(screen.queryByTitle(/Delete \d selected/)).toBeNull();
    });
  });

  describe("tabs", () => {
    const withRecent = () => all.map((c) => (c.id === "a1" ? { ...c, ...recent() } : c.id === "b" ? { ...c, last_activity_at: Date.now() - 120_000 } : c));

    it("shows only the tree, without tabs, while nothing is recent", () => {
      localStorage.setItem("loop-sidebar-tab", JSON.stringify("recent"));
      setup();
      expect(screen.queryByTestId("sidebar-tabs")).toBeNull();
      expect(screen.queryByTestId("sidebar-recent")).toBeNull();
      expect(channelRowNames()).toContain("alpha");
    });

    it("opens on Tree and switches to Recent, saving the tab", () => {
      setup({ channels: withRecent() });
      expect(screen.getByTestId("sidebar-tab-tree").getAttribute("aria-selected")).toBe("true");
      fireEvent.click(screen.getByTestId("sidebar-tab-recent"));
      expect(localStorage.getItem("loop-sidebar-tab")).toBe('"recent"');
      expect(screen.getAllByTestId("sidebar-session-row").map((r) => r.dataset.channelId)).toEqual(["a1", "b"]);
      expect(screen.getByTestId("sidebar-tab-recent-count").textContent).toBe("2");
      expect(screen.queryAllByTestId("sidebar-channel-row")).toHaveLength(0);
    });

    it("lists running and waiting sessions first, and keeps a finished run recent", () => {
      localStorage.setItem("loop-sidebar-tab", JSON.stringify("recent"));
      const channels = withRecent().map((c) => (c.id === "a3" ? { ...c, agent_running: true } : c.id === "a2" ? { ...c, review_running: true } : c));
      const { rerender } = setup({ channels, isRunningMapRef: { current: new Map([["b", "x"]]) }, pillsRef: pillsRef([["gate", ["b1"]]]) });
      const ids = () => screen.getAllByTestId("sidebar-session-row").map((r) => r.dataset.channelId);
      // Running and waiting sessions count as active now, so they top the older ones.
      expect(ids().slice(0, 4).sort()).toEqual(["a2", "a3", "b", "b1"]);
      expect(ids()[4]).toBe("a1");
      rerender({ channels: withRecent(), pillsRef: pillsRef([]) });
      expect(ids()).toContain("a3");
    });

    it("filters Recent by search, including where a session lives", async () => {
      localStorage.setItem("loop-sidebar-tab", JSON.stringify("recent"));
      const { user } = setup({ channels: withRecent() });
      await user.type(screen.getByPlaceholderText("Search..."), "alpha");
      expect(screen.getAllByTestId("sidebar-session-row").map((r) => r.dataset.channelId)).toEqual(["a1"]);
      await user.clear(screen.getByPlaceholderText("Search..."));
      await user.type(screen.getByPlaceholderText("Search..."), "zzz");
      expect(screen.getByTestId("sidebar-recent-empty")).toBeTruthy();
      // The tabs stay so the way back to the tree does too.
      expect(screen.getByTestId("sidebar-tabs")).toBeTruthy();
    });

    it("shows only the tree in select mode", async () => {
      localStorage.setItem("loop-sidebar-tab", JSON.stringify("recent"));
      const { user } = setup({ channels: withRecent() });
      await user.click(screen.getByTitle("Select channels to delete"));
      expect(screen.queryByTestId("sidebar-recent")).toBeNull();
      expect(screen.queryByTestId("sidebar-tabs")).toBeNull();
      expect(channelRowNames()).toContain("alpha");
    });
  });

  describe("task threads", () => {
    const task = channel({ id: "tk", name: "⏱ task #1 nightly", parent_id: "a", task_id: 1, ...recent() });
    const runningTask = channel({ id: "tr", name: "⏱ task #2 busy", parent_id: "a", task_id: 2, agent_running: true, ...recent() });
    const litTask = channel({ id: "tl", name: "⏱ task #3 asks", parent_id: "a", task_id: 3, ...recent() });
    const channels = [...all, task, runningTask, litTask];
    const pr = () => pillsRef([["ask", ["tl"]]]);

    it("hides the idle task threads in the tree, keeping running and waiting ones", () => {
      setup({ channels, pillsRef: pr() });
      expect(threadNames()).toContain("task #1 nightly");
      fireEvent.click(screen.getByTestId("sidebar-hide-tasks"));
      expect(JSON.parse(localStorage.getItem("loop-sidebar-hide-tasks")!)).toEqual({ recent: false, tree: true });
      expect(threadNames().some((n) => n?.startsWith("task #1"))).toBe(false);
      expect(threadNames().some((n) => n?.startsWith("task #2"))).toBe(true);
      expect(threadNames().some((n) => n?.startsWith("task #3"))).toBe(true);
    });

    it("reads an old all-tabs setting and hides them in Recent too", () => {
      localStorage.setItem("loop-sidebar-hide-tasks", "true");
      localStorage.setItem("loop-sidebar-tab", JSON.stringify("recent"));
      setup({ channels, pillsRef: pr() });
      const ids = screen.getAllByTestId("sidebar-session-row").map((r) => r.dataset.channelId);
      expect(ids).not.toContain("tk");
      expect(ids).toEqual(expect.arrayContaining(["tr", "tl"]));
      fireEvent.click(screen.getByTestId("sidebar-hide-tasks"));
      expect(screen.getAllByTestId("sidebar-session-row").map((r) => r.dataset.channelId)).toContain("tk");
    });
  });

  describe("needs you", () => {
    it("shows only the rows with a lit pill, and their parents", () => {
      setup({ pillsRef: pillsRef([["gate", ["s1"]]]) });
      fireEvent.click(screen.getByTestId("sidebar-needs-you"));
      expect(localStorage.getItem("loop-sidebar-needs-you")).toBe("true");
      expect(channelRowNames()).toEqual(["alpha"]);
      expect(threadNames()).toEqual(["one", "deepgate"]);
    });

    it("keeps a lit DM and says when nothing needs you", () => {
      localStorage.setItem("loop-sidebar-needs-you", "true");
      const { rerender } = setup({ pillsRef: pillsRef([["gate", ["dm"]]]) });
      expect(channelRowNames()).toEqual(["dmgate"]);
      expect(screen.queryByTestId("sidebar-needs-you-empty")).toBeNull();
      rerender({ pillsRef: pillsRef([]) });
      expect(screen.getByTestId("sidebar-needs-you-empty").textContent).toBe("Nothing needs you right now");
    });

    it("narrows Recent to the lit sessions", () => {
      localStorage.setItem("loop-sidebar-needs-you", "true");
      localStorage.setItem("loop-sidebar-tab", JSON.stringify("recent"));
      const channels = all.map((c) => (c.id === "a1" || c.id === "a3" ? { ...c, ...recent(), agent_running: c.id === "a3" } : c));
      setup({ channels, pillsRef: pillsRef([["ci", ["a1"]]]) });
      expect(screen.getAllByTestId("sidebar-session-row").map((r) => r.dataset.channelId)).toEqual(["a1"]);
      // Recent has its own empty line, so the tree's isn't shown.
      expect(screen.queryByTestId("sidebar-needs-you-empty")).toBeNull();
    });

    it("steps aside in select mode", async () => {
      localStorage.setItem("loop-sidebar-needs-you", "true");
      const { user } = setup({ pillsRef: pillsRef([]) });
      expect(channelRowNames()).toEqual([]);
      await user.click(screen.getByTitle("Select channels to delete"));
      expect(channelRowNames()).toEqual(["dm", "alpha", "beta"]);
    });

    it("turns pills on and off from the bell's menu and saves the choice", async () => {
      const { user } = setup({}, { initialHiddenPills: ["ci"] });
      fireEvent.contextMenu(screen.getByTestId("sidebar-needs-you"), { clientX: 5, clientY: 5 });
      const ci = screen.getByRole("menuitemradio", { name: /ci/ });
      expect(ci.getAttribute("aria-checked")).toBe("false");
      expect(screen.getByRole("menuitemradio", { name: /gate/ }).getAttribute("aria-checked")).toBe("true");
      await user.click(ci);
      expect(configMock).toHaveBeenCalledWith(["desktop", "hidden_pills"], []);
      fireEvent.contextMenu(screen.getByTestId("sidebar-needs-you"), { clientX: 5, clientY: 5 });
      expect(screen.getByRole("menuitemradio", { name: /ci/ }).getAttribute("aria-checked")).toBe("true");
      configMock.mockRejectedValueOnce(new Error("offline"));
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      await user.click(screen.getByRole("menuitemradio", { name: /gate/ }));
      expect(configMock).toHaveBeenLastCalledWith(["desktop", "hidden_pills"], ["gate"]);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] saving the hidden sidebar pills:", expect.any(Error)));
    });
  });

  it("closes the context menu", async () => {
    const { user } = setup();
    fireEvent.contextMenu(channelRow("alpha"));
    await act(async () => {
      await user.keyboard("{Escape}");
    });
    expect(screen.queryByTestId("context-menu")).toBeNull();
  });
});
