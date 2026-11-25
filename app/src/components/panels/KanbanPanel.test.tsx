import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { addTicketNote, assignTicket, createTicket, deleteTicket, fetchTickets, type Ticket, updateTicket, updateTicketStatus } from "../../api/loopApi";
import { useEventStream } from "../../hooks/useEventStream";
import { KanbanPanel } from "./KanbanPanel";

vi.mock("../../api/loopApi", () => ({
  addTicketNote: vi.fn(),
  assignTicket: vi.fn(),
  createTicket: vi.fn(),
  deleteTicket: vi.fn(),
  fetchTickets: vi.fn(),
  updateTicket: vi.fn(),
  updateTicketStatus: vi.fn(),
}));

// The event stream opens a WebSocket; the stub records the handler the panel
// registers so a test can push events through it.
vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

const fetchMock = vi.mocked(fetchTickets);
const createMock = vi.mocked(createTicket);
const statusMock = vi.mocked(updateTicketStatus);
const assignMock = vi.mocked(assignTicket);
const updateMock = vi.mocked(updateTicket);
const deleteMock = vi.mocked(deleteTicket);
const noteMock = vi.mocked(addTicketNote);
const streamMock = vi.mocked(useEventStream);

function ticket(id: string, extra: Partial<Ticket> = {}): Ticket {
  return { id, title: `Ticket ${id}`, status: "open", type: "task", priority: 2, tags: [], deps: [], links: [], created: "2026-01-01T00:00:00Z", ...extra };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function lastOnEvent() {
  return streamMock.mock.calls.at(-1)![0].onEvent;
}

function renderPanel(props: Partial<Parameters<typeof KanbanPanel>[0]> = {}) {
  const user = userEvent.setup();
  const view = render(<KanbanPanel channelId="ch-1" dirPath="/repo" {...props} />);
  return { user, ...view };
}

const createDrawer = () => screen.getByTestId("kanban-create-drawer");
const editDrawer = () => screen.getByTestId("kanban-edit-drawer");
const card = (id: string) => screen.getByTestId(`kanban-card-${id}`);

async function openCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "+ New" }));
  return createDrawer();
}

async function openEditFor(user: ReturnType<typeof userEvent.setup>, t: Ticket) {
  await user.click(await screen.findByText(t.title));
  return editDrawer();
}

describe("KanbanPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    fetchMock.mockResolvedValue([]);
    createMock.mockResolvedValue(ticket("new"));
    statusMock.mockResolvedValue(ticket("x"));
    updateMock.mockResolvedValue(ticket("x"));
    deleteMock.mockResolvedValue(undefined as never);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("board", () => {
    it("asks for a directory when the channel has none and fetches nothing", () => {
      renderPanel({ dirPath: "" });
      expect(screen.getByTestId("kanban-panel").textContent).toBe("No directory configured for this channel");
      expect(fetchMock).not.toHaveBeenCalled();
    });

    it("loads the board sorted by priority and groups tickets into their columns", async () => {
      fetchMock.mockResolvedValue([
        ticket("a", { title: "Open one" }),
        ticket("b", { title: "Doing one", status: "in_progress" }),
        ticket("c", { title: "Done one", status: "closed" }),
        ticket("d", { title: "Lost one", status: "archived" as Ticket["status"] }),
      ]);
      renderPanel();
      await screen.findByText("Open one");
      expect(fetchMock).toHaveBeenCalledWith("/repo", { sort: "priority" });
      expect(screen.getByText("4 tickets")).toBeTruthy();
      // A ticket with an unknown status lands in no column.
      expect(screen.queryByText("Lost one")).toBeNull();
      expect(within(card("a")).getByRole("button", { name: "Start" })).toBeTruthy();
      expect(within(card("b")).getByRole("button", { name: "Close" })).toBeTruthy();
      expect(within(card("b")).getByRole("button", { name: "Reopen" })).toBeTruthy();
      expect(within(card("c")).getByRole("button", { name: "Reopen" })).toBeTruthy();
      expect(within(card("c")).queryByRole("button", { name: "Close" })).toBeNull();
    });

    it("shows a singular count and a placeholder in each empty column", async () => {
      fetchMock.mockResolvedValue([ticket("a")]);
      renderPanel();
      await screen.findByText("1 ticket");
      expect(screen.getAllByText("No tickets")).toHaveLength(2);
    });

    it("keeps an empty board when the fetch fails", async () => {
      fetchMock.mockRejectedValue(new Error("down"));
      renderPanel();
      await waitFor(() => expect(fetchMock).toHaveBeenCalled());
      expect(screen.getByText("0 tickets")).toBeTruthy();
      expect(screen.getAllByText("No tickets")).toHaveLength(3);
    });

    it("drops a load that a later load overtook", async () => {
      const first = deferred<Ticket[]>();
      const second = deferred<Ticket[]>();
      fetchMock.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
      renderPanel();
      act(() => lastOnEvent()({ type: "ticket.updated" } as never));
      await act(async () => second.resolve([ticket("new", { title: "Fresh" })]));
      await act(async () => first.resolve([ticket("old", { title: "Stale" })]));
      expect(screen.getByText("Fresh")).toBeTruthy();
      expect(screen.queryByText("Stale")).toBeNull();
    });

    it("reloads on ticket and channel-created events only", async () => {
      renderPanel();
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
      expect(streamMock).toHaveBeenCalledWith(expect.objectContaining({ channelId: "ch-1" }));
      act(() => lastOnEvent()({ type: "ticket.created" } as never));
      act(() => lastOnEvent()({ type: "channel.created" } as never));
      act(() => lastOnEvent()({ type: "message.created" } as never));
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    });
  });

  describe("cards", () => {
    it("shows priority, type, tags, people, refs and dependency counts", async () => {
      fetchMock.mockResolvedValue([
        ticket("p0", { priority: 0, type: "bug", tags: ["ui", "fast"], assignee: "sam", external_ref: "https://tracker.example/1", pr: "pr-7", deps: ["x"] }),
        ticket("p1", { priority: 1, type: "weird", external_ref: "gh-12", pr: "https://git.example/pull/3", deps: ["x", "y"] }),
        ticket("p3", { priority: 3, type: undefined }),
      ]);
      renderPanel();
      await screen.findByText("Ticket p0");

      const p0 = within(card("p0"));
      expect(p0.getByText("P0")).toBeTruthy();
      expect(p0.getByText("bug")).toBeTruthy();
      expect(p0.getByText("ui")).toBeTruthy();
      expect(p0.getByText("fast")).toBeTruthy();
      expect(p0.getByTitle("Assignee").textContent).toBe("sam");
      const ext = p0.getByTitle("External ref");
      expect(ext.tagName).toBe("A");
      expect(ext.getAttribute("href")).toBe("https://tracker.example/1");
      expect(ext.getAttribute("target")).toBe("_blank");
      expect(p0.getByTitle("Pull request").tagName).toBe("SPAN");
      expect(p0.getByText("1 dep")).toBeTruthy();

      const p1 = within(card("p1"));
      expect(p1.getByText("P1")).toBeTruthy();
      expect(p1.getByText("weird")).toBeTruthy();
      expect(p1.getByTitle("External ref").tagName).toBe("SPAN");
      expect(p1.getByTitle("Pull request").getAttribute("href")).toBe("https://git.example/pull/3");
      expect(p1.getByText("2 deps")).toBeTruthy();

      const p3 = within(card("p3"));
      expect(p3.queryByText(/^P\d$/)).toBeNull();
      expect(p3.queryByTitle("Assignee")).toBeNull();
      expect(p3.queryByText(/dep/)).toBeNull();
    });

    it("follows a ref link without opening the edit drawer", async () => {
      fetchMock.mockResolvedValue([ticket("a", { pr: "https://git.example/pull/3" })]);
      renderPanel();
      const link = await screen.findByTitle("Pull request");
      const stop = vi.spyOn(Event.prototype, "stopPropagation");
      fireEvent.click(link);
      expect(stop).toHaveBeenCalled();
      expect(screen.queryByTestId("kanban-edit-drawer")).toBeNull();
    });

    it.each([
      ["open", "Start", "in_progress"],
      ["in_progress", "Close", "closed"],
      ["in_progress", "Reopen", "open"],
      ["closed", "Reopen", "open"],
    ] as const)("a %s ticket's %s button moves it to %s and reloads", async (status, label, next) => {
      fetchMock.mockResolvedValue([ticket("a", { status })]);
      const { user } = renderPanel();
      await screen.findByText("Ticket a");
      await user.click(within(card("a")).getByRole("button", { name: label }));
      expect(statusMock).toHaveBeenCalledWith("a", next, "/repo");
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    });

    it("doesn't reload when a status change fails", async () => {
      fetchMock.mockResolvedValue([ticket("a")]);
      statusMock.mockRejectedValue(new Error("nope"));
      const { user } = renderPanel();
      await screen.findByText("Ticket a");
      await user.click(screen.getByRole("button", { name: "Start" }));
      expect(statusMock).toHaveBeenCalled();
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it("only offers Assign Worktree when worktrees are allowed", async () => {
      fetchMock.mockResolvedValue([ticket("a")]);
      renderPanel();
      await screen.findByText("Ticket a");
      expect(screen.queryByRole("button", { name: "Assign Worktree" })).toBeNull();
    });

    it("assigns a worktree, shows progress, selects the new thread and reloads", async () => {
      fetchMock.mockResolvedValue([ticket("a")]);
      const pending = deferred<{ thread_id: string }>();
      assignMock.mockReturnValue(pending.promise as never);
      const onSelectChannel = vi.fn();
      const { user } = renderPanel({ allowWorktree: true, onSelectChannel });
      await screen.findByText("Ticket a");
      await user.click(screen.getByRole("button", { name: "Assign Worktree" }));
      const busy = screen.getByRole("button", { name: "Assigning..." }) as HTMLButtonElement;
      expect(busy.disabled).toBe(true);
      expect(assignMock).toHaveBeenCalledWith("a", { dir: "/repo", channel_id: "ch-1" });
      await act(async () => pending.resolve({ thread_id: "th-9" }));
      expect(onSelectChannel).toHaveBeenCalledWith("th-9");
      expect(screen.getByRole("button", { name: "Assign Worktree" })).toBeTruthy();
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    it("assigns without a channel selector", async () => {
      fetchMock.mockResolvedValue([ticket("a")]);
      assignMock.mockResolvedValue({ thread_id: "th-9" } as never);
      const { user } = renderPanel({ allowWorktree: true });
      await screen.findByText("Ticket a");
      await user.click(screen.getByRole("button", { name: "Assign Worktree" }));
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    });

    it.each([
      [new Error("worktree busy"), "worktree busy"],
      ["boom", "Failed to assign worktree"],
    ])("shows an assign failure (%s) until dismissed", async (failure, message) => {
      fetchMock.mockResolvedValue([ticket("a")]);
      assignMock.mockRejectedValue(failure);
      const { user } = renderPanel({ allowWorktree: true });
      await screen.findByText("Ticket a");
      await user.click(screen.getByRole("button", { name: "Assign Worktree" }));
      expect(await screen.findByText(message)).toBeTruthy();
      await user.click(screen.getByRole("button", { name: "dismiss" }));
      expect(screen.queryByText(message)).toBeNull();
    });
  });

  describe("board scope", () => {
    it("has no scope switch outside a worktree chain", async () => {
      renderPanel({ rootDirPath: "/repo" });
      await waitFor(() => expect(fetchMock).toHaveBeenCalled());
      expect(screen.queryByTestId("kanban-scope-local")).toBeNull();
    });

    it("switches between the local and root boards and remembers the choice per channel", async () => {
      const { user, unmount } = renderPanel({ dirPath: "/wt", rootDirPath: "/repo" });
      await waitFor(() => expect(fetchMock).toHaveBeenLastCalledWith("/wt", { sort: "priority" }));
      expect(screen.getByTestId("kanban-scope-local").getAttribute("title")).toBe("/wt/.tickets");
      expect(screen.getByTestId("kanban-scope-root").getAttribute("title")).toBe("/repo/.tickets");
      await user.click(screen.getByTestId("kanban-scope-root"));
      await waitFor(() => expect(fetchMock).toHaveBeenLastCalledWith("/repo", { sort: "priority" }));
      expect(localStorage.getItem("kanban-scope:ch-1")).toBe("root");
      unmount();

      fetchMock.mockClear();
      renderPanel({ dirPath: "/wt", rootDirPath: "/repo" });
      await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/repo", { sort: "priority" }));
      await userEvent.click(screen.getByTestId("kanban-scope-local"));
      await waitFor(() => expect(fetchMock).toHaveBeenLastCalledWith("/wt", { sort: "priority" }));
      expect(localStorage.getItem("kanban-scope:ch-1")).toBe("local");
    });

    it("ignores a remembered root scope once the channel has no root", async () => {
      localStorage.setItem("kanban-scope:ch-1", "root");
      renderPanel();
      await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/repo", { sort: "priority" }));
    });
  });

  describe("create drawer", () => {
    it("toggles from the toolbar, focuses the title and closes on Cancel, Escape, × and the backdrop", async () => {
      const { user } = renderPanel();
      let drawer = await openCreate(user);
      expect(within(drawer).getByText("New Ticket")).toBeTruthy();
      await waitFor(() => expect(document.activeElement).toBe(within(drawer).getByLabelText("Title")));

      // A click inside the drawer stays inside it.
      await user.click(within(drawer).getByText("New Ticket"));
      expect(screen.getByTestId("kanban-create-drawer")).toBeTruthy();

      // Other keys don't close it.
      fireEvent.keyDown(drawer, { key: "a" });
      fireEvent.keyDown(drawer, { key: "Escape" });
      await waitFor(() => expect(screen.queryByTestId("kanban-create-drawer")).toBeNull());

      drawer = await openCreate(user);
      await user.click(within(drawer).getByRole("button", { name: "Cancel" }));
      await waitFor(() => expect(screen.queryByTestId("kanban-create-drawer")).toBeNull());

      drawer = await openCreate(user);
      await user.click(within(drawer).getByRole("button", { name: "Close" }));
      await waitFor(() => expect(screen.queryByTestId("kanban-create-drawer")).toBeNull());

      drawer = await openCreate(user);
      await user.click(drawer.parentElement!);
      await waitFor(() => expect(screen.queryByTestId("kanban-create-drawer")).toBeNull());

      // The toolbar button toggles it closed too.
      await openCreate(user);
      await user.click(screen.getByRole("button", { name: "+ New" }));
      await waitFor(() => expect(screen.queryByTestId("kanban-create-drawer")).toBeNull());
    });

    it("leaves focus where it is when something in the drawer already has it", async () => {
      const raf = vi.spyOn(window, "requestAnimationFrame").mockImplementation(() => 1);
      const { user } = renderPanel();
      const drawer = await openCreate(user);
      const frame = raf.mock.calls.at(-1)![0];
      const desc = within(drawer).getByLabelText("Description");
      desc.focus();
      act(() => frame(0));
      expect(document.activeElement).toBe(desc);
    });

    it("cancels the pending slide-in frame when it closes before the frame runs", async () => {
      vi.spyOn(window, "requestAnimationFrame").mockImplementation(() => 42);
      const cancel = vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
      const { user } = renderPanel();
      await openCreate(user);
      await user.click(screen.getByRole("button", { name: "+ New" }));
      expect(cancel).toHaveBeenCalledWith(42);
    });

    it("keeps the form showing while it slides out, then unmounts", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const { user } = renderPanel();
      const drawer = await openCreate(user);
      await user.click(within(drawer).getByRole("button", { name: "Cancel" }));
      expect(screen.getByTestId("kanban-create-drawer")).toBeTruthy();
      expect(within(screen.getByTestId("kanban-create-drawer")).getByText("New Ticket")).toBeTruthy();
      act(() => vi.advanceTimersByTime(220));
      expect(screen.queryByTestId("kanban-create-drawer")).toBeNull();
    });

    it("creates a ticket from every field, then resets the form and drops the draft", async () => {
      const { user } = renderPanel();
      const drawer = await openCreate(user);
      const d = within(drawer);
      const create = d.getByRole("button", { name: "Create" }) as HTMLButtonElement;
      expect(create.disabled).toBe(true);

      await user.type(d.getByLabelText("Title"), "  Fix login  ");
      await user.selectOptions(d.getByLabelText("Type"), "bug");
      await user.selectOptions(d.getByLabelText("Priority"), "0");
      await user.type(d.getByLabelText("Description"), " Broken ");
      await user.type(d.getByLabelText("Assignee"), " sam ");
      await user.type(d.getByLabelText("Tags"), "ui, , auth ");
      expect(d.queryByLabelText("Parent")).toBeNull();
      await user.click(d.getByRole("button", { name: "▸ More fields" }));
      await user.type(d.getByLabelText("External ref"), " gh-1 ");
      await user.type(d.getByLabelText("Parent"), " t-0 ");
      await user.type(d.getByLabelText("Pull request"), " https://git.example/pull/1 ");
      await user.type(d.getByLabelText("Design"), " plan ");
      await user.type(d.getByLabelText("Acceptance criteria"), " works ");
      expect(JSON.parse(localStorage.getItem("kanban-draft:ch-1")!)).toMatchObject({ title: "  Fix login  ", type: "bug", priority: 0, acceptance: " works " });
      await user.click(d.getByRole("button", { name: "▾ Less" }));
      expect(d.queryByLabelText("Parent")).toBeNull();

      await user.click(create);
      expect(createMock).toHaveBeenCalledWith({
        dir: "/repo",
        title: "Fix login",
        type: "bug",
        priority: 0,
        description: "Broken",
        assignee: "sam",
        tags: ["ui", "auth"],
        external_ref: "gh-1",
        pr: "https://git.example/pull/1",
        parent: "t-0",
        design: "plan",
        acceptance: "works",
      });
      await waitFor(() => expect(screen.queryByTestId("kanban-create-drawer")).toBeNull());
      expect(localStorage.getItem("kanban-draft:ch-1")).toBeNull();
      expect(fetchMock).toHaveBeenCalledTimes(2);

      const again = within(await openCreate(user));
      expect((again.getByLabelText("Title") as HTMLInputElement).value).toBe("");
      expect((again.getByLabelText("Type") as HTMLSelectElement).value).toBe("task");
      expect((again.getByLabelText("Priority") as HTMLSelectElement).value).toBe("2");
      expect(again.getByRole("button", { name: "▸ More fields" })).toBeTruthy();
    });

    it("creates on Enter with only a title, leaving the optional fields out", async () => {
      const { user } = renderPanel();
      const title = within(await openCreate(user)).getByLabelText("Title");
      await user.type(title, "{Enter}");
      expect(createMock).not.toHaveBeenCalled();
      await user.type(title, "Quick{Enter}");
      expect(createMock).toHaveBeenCalledWith({
        dir: "/repo",
        title: "Quick",
        type: "task",
        priority: 2,
        description: undefined,
        assignee: undefined,
        tags: undefined,
        external_ref: undefined,
        pr: undefined,
        parent: undefined,
        design: undefined,
        acceptance: undefined,
      });
    });

    it("keeps the drawer and draft open when creating fails", async () => {
      createMock.mockRejectedValue(new Error("nope"));
      const { user } = renderPanel();
      const d = within(await openCreate(user));
      await user.type(d.getByLabelText("Title"), "Keep me");
      await user.click(d.getByRole("button", { name: "Create" }));
      expect(createMock).toHaveBeenCalled();
      expect(screen.getByTestId("kanban-create-drawer")).toBeTruthy();
      expect(JSON.parse(localStorage.getItem("kanban-draft:ch-1")!).title).toBe("Keep me");
    });

    it("restores a saved draft and clears it once the fields are emptied", async () => {
      localStorage.setItem(
        "kanban-draft:ch-1",
        JSON.stringify({ title: "Draft", type: "epic", priority: 4, description: "d", assignee: "a", tags: "t", external_ref: "e", pr: "p", parent: "pa", design: "de", acceptance: "ac" }),
      );
      const { user } = renderPanel();
      const d = within(await openCreate(user));
      await user.click(d.getByRole("button", { name: "▸ More fields" }));
      expect((d.getByLabelText("Title") as HTMLInputElement).value).toBe("Draft");
      expect((d.getByLabelText("Type") as HTMLSelectElement).value).toBe("epic");
      expect((d.getByLabelText("Priority") as HTMLSelectElement).value).toBe("4");
      expect((d.getByLabelText("Parent") as HTMLInputElement).value).toBe("pa");
      expect((d.getByLabelText("Acceptance criteria") as HTMLTextAreaElement).value).toBe("ac");

      for (const label of ["Title", "Description", "Assignee", "Tags", "External ref", "Parent", "Pull request", "Design", "Acceptance criteria"]) {
        await user.clear(d.getByLabelText(label));
      }
      await user.selectOptions(d.getByLabelText("Type"), "task");
      expect(localStorage.getItem("kanban-draft:ch-1")).not.toBeNull();
      await user.selectOptions(d.getByLabelText("Priority"), "2");
      expect(localStorage.getItem("kanban-draft:ch-1")).toBeNull();
    });

    it("starts blank when the saved draft is corrupt or empty", async () => {
      localStorage.setItem("kanban-draft:ch-1", "{not json");
      const { user, unmount } = renderPanel();
      expect((within(await openCreate(user)).getByLabelText("Title") as HTMLInputElement).value).toBe("");
      unmount();

      localStorage.setItem("kanban-draft:ch-1", "{}");
      renderPanel();
      const d = within(await openCreate(userEvent.setup()));
      expect((d.getByLabelText("Title") as HTMLInputElement).value).toBe("");
      expect((d.getByLabelText("Type") as HTMLSelectElement).value).toBe("task");
      expect((d.getByLabelText("Priority") as HTMLSelectElement).value).toBe("2");
    });
  });

  describe("edit drawer", () => {
    const full = ticket("t1", {
      title: "Full",
      type: "feature",
      priority: 1,
      description: "desc",
      assignee: "kim",
      tags: ["a", "b"],
      deps: ["t0"],
      external_ref: "gh-5",
      pr: "https://git.example/pull/5",
      design: "design",
      acceptance: "accept",
      notes: [
        { timestamp: "2026-02-03T04:05:06Z", content: "first note" },
        { timestamp: "yesterday", content: "second note" },
      ],
    });

    it("fills the form from the ticket and opens the extra fields when it uses them", async () => {
      fetchMock.mockResolvedValue([full]);
      const { user } = renderPanel();
      const d = within(await openEditFor(user, full));
      expect(d.getByText("Edit Ticket")).toBeTruthy();
      expect(d.getByText("t1")).toBeTruthy();
      expect((d.getByLabelText("Title") as HTMLInputElement).value).toBe("Full");
      expect((d.getByLabelText("Type") as HTMLSelectElement).value).toBe("feature");
      expect((d.getByLabelText("Priority") as HTMLSelectElement).value).toBe("1");
      expect((d.getByLabelText("Description") as HTMLTextAreaElement).value).toBe("desc");
      expect((d.getByLabelText("Assignee") as HTMLInputElement).value).toBe("kim");
      expect((d.getByLabelText("Tags") as HTMLInputElement).value).toBe("a, b");
      expect((d.getByLabelText("Dependencies") as HTMLInputElement).value).toBe("t0");
      expect((d.getByLabelText("External ref") as HTMLInputElement).value).toBe("gh-5");
      expect((d.getByLabelText("Pull request") as HTMLInputElement).value).toBe("https://git.example/pull/5");
      expect((d.getByLabelText("Design") as HTMLTextAreaElement).value).toBe("design");
      expect((d.getByLabelText("Acceptance criteria") as HTMLTextAreaElement).value).toBe("accept");

      const notes = within(screen.getByTestId("kanban-ticket-notes"));
      expect(notes.getByText("2")).toBeTruthy();
      expect(notes.getByText("first note")).toBeTruthy();
      expect(notes.getByTitle("2026-02-03T04:05:06Z").textContent).toBe(new Date("2026-02-03T04:05:06Z").toLocaleString());
      // A timestamp that doesn't parse is shown as written.
      expect(notes.getByTitle("yesterday").textContent).toBe("yesterday");

      await user.click(d.getByRole("button", { name: "▾ Less" }));
      expect(d.queryByLabelText("Dependencies")).toBeNull();
    });

    it("defaults missing fields and keeps the extra fields folded for a bare ticket", async () => {
      const bare = ticket("t2", { type: undefined, notes: undefined });
      fetchMock.mockResolvedValue([bare]);
      const { user } = renderPanel();
      const d = within(await openEditFor(user, bare));
      expect((d.getByLabelText("Type") as HTMLSelectElement).value).toBe("task");
      expect((d.getByLabelText("Description") as HTMLTextAreaElement).value).toBe("");
      expect((d.getByLabelText("Assignee") as HTMLInputElement).value).toBe("");
      expect(d.queryByLabelText("Dependencies")).toBeNull();
      expect(within(screen.getByTestId("kanban-ticket-notes")).getByText("No notes yet")).toBeTruthy();
      await user.click(d.getByRole("button", { name: "▸ More fields" }));
      expect((d.getByLabelText("External ref") as HTMLInputElement).value).toBe("");
      expect((d.getByLabelText("Pull request") as HTMLInputElement).value).toBe("");
      expect((d.getByLabelText("Design") as HTMLTextAreaElement).value).toBe("");
      expect((d.getByLabelText("Acceptance criteria") as HTMLTextAreaElement).value).toBe("");
    });

    it.each([
      ["external ref", { external_ref: "x" }],
      ["PR", { pr: "x" }],
      ["design", { design: "x" }],
      ["acceptance", { acceptance: "x" }],
    ])("opens the extra fields for a ticket with only a %s", async (_name, extra) => {
      const t = ticket("t3", extra);
      fetchMock.mockResolvedValue([t]);
      const { user } = renderPanel();
      const d = within(await openEditFor(user, t));
      expect(d.getByLabelText("Dependencies")).toBeTruthy();
    });

    it("saves every edited field trimmed and reloads", async () => {
      fetchMock.mockResolvedValue([full]);
      const { user } = renderPanel();
      const d = within(await openEditFor(user, full));
      await user.clear(d.getByLabelText("Title"));
      expect((d.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
      await user.type(d.getByLabelText("Title"), " Renamed ");
      await user.selectOptions(d.getByLabelText("Type"), "chore");
      await user.selectOptions(d.getByLabelText("Priority"), "3");
      await user.type(d.getByLabelText("Description"), " ");
      await user.type(d.getByLabelText("Assignee"), " ");
      await user.type(d.getByLabelText("Tags"), ", c");
      await user.type(d.getByLabelText("Dependencies"), ", , t9");
      await user.type(d.getByLabelText("External ref"), "!");
      await user.type(d.getByLabelText("Pull request"), "/");
      await user.type(d.getByLabelText("Design"), "2");
      await user.type(d.getByLabelText("Acceptance criteria"), "3");
      await user.click(d.getByRole("button", { name: "Save" }));
      expect(updateMock).toHaveBeenCalledWith("t1", {
        dir: "/repo",
        title: "Renamed",
        type: "chore",
        priority: 3,
        description: "desc",
        assignee: "kim",
        tags: ["a", "b", "c"],
        deps: ["t0", "t9"],
        external_ref: "gh-5!",
        pr: "https://git.example/pull/5/",
        design: "design2",
        acceptance: "accept3",
      });
      await waitFor(() => expect(screen.queryByTestId("kanban-edit-drawer")).toBeNull());
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    it("saves on Enter in the title only when it has text", async () => {
      fetchMock.mockResolvedValue([full]);
      const { user } = renderPanel();
      const title = within(await openEditFor(user, full)).getByLabelText("Title");
      await user.clear(title);
      await user.type(title, "{Enter}");
      expect(updateMock).not.toHaveBeenCalled();
      await user.type(title, "New{Enter}");
      expect(updateMock).toHaveBeenCalledWith("t1", expect.objectContaining({ title: "New" }));
    });

    it("stays open when saving fails", async () => {
      fetchMock.mockResolvedValue([full]);
      updateMock.mockRejectedValue(new Error("nope"));
      const { user } = renderPanel();
      const d = within(await openEditFor(user, full));
      await user.click(d.getByRole("button", { name: "Save" }));
      expect(updateMock).toHaveBeenCalled();
      expect(screen.getByTestId("kanban-edit-drawer")).toBeTruthy();
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it("closes on Cancel", async () => {
      fetchMock.mockResolvedValue([full]);
      const { user } = renderPanel();
      const d = within(await openEditFor(user, full));
      await user.click(d.getByRole("button", { name: "Cancel" }));
      await waitFor(() => expect(screen.queryByTestId("kanban-edit-drawer")).toBeNull());
    });

    it("closes on Escape", async () => {
      fetchMock.mockResolvedValue([full]);
      const { user } = renderPanel();
      const title = within(await openEditFor(user, full)).getByLabelText("Title");
      fireEvent.keyDown(title, { key: "Escape" });
      await waitFor(() => expect(screen.queryByTestId("kanban-edit-drawer")).toBeNull());
    });

    it("asks before deleting, can back out, then deletes and closes", async () => {
      fetchMock.mockResolvedValue([full]);
      const { user } = renderPanel();
      const d = within(await openEditFor(user, full));
      await user.click(d.getByRole("button", { name: "Delete" }));
      expect(d.getByText("Delete?")).toBeTruthy();
      await user.click(d.getByRole("button", { name: "No" }));
      expect(d.queryByText("Delete?")).toBeNull();
      await user.click(d.getByRole("button", { name: "Delete" }));
      await user.click(d.getByRole("button", { name: "Yes" }));
      expect(deleteMock).toHaveBeenCalledWith("t1", "/repo");
      await waitFor(() => expect(screen.queryByTestId("kanban-edit-drawer")).toBeNull());
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });

    it("keeps the confirmation when deleting fails", async () => {
      fetchMock.mockResolvedValue([full]);
      deleteMock.mockRejectedValue(new Error("nope"));
      const { user } = renderPanel();
      const d = within(await openEditFor(user, full));
      await user.click(d.getByRole("button", { name: "Delete" }));
      await user.click(d.getByRole("button", { name: "Yes" }));
      expect(deleteMock).toHaveBeenCalled();
      expect(d.getByText("Delete?")).toBeTruthy();
    });

    it("adds a note, shows it from the returned ticket and clears the draft", async () => {
      fetchMock.mockResolvedValue([full, ticket("other")]);
      const pending = deferred<Ticket>();
      noteMock.mockReturnValue(pending.promise);
      const { user } = renderPanel();
      await openEditFor(user, full);
      const notes = within(screen.getByTestId("kanban-ticket-notes"));
      const add = notes.getByRole("button", { name: "Add note" }) as HTMLButtonElement;
      expect(add.disabled).toBe(true);
      const box = notes.getByPlaceholderText(/Add a note/);
      await user.type(box, "  third  ");
      await user.click(add);
      expect(noteMock).toHaveBeenCalledWith("t1", { dir: "/repo", content: "third" });
      expect((notes.getByRole("button", { name: "Adding..." }) as HTMLButtonElement).disabled).toBe(true);
      await act(async () => pending.resolve({ ...full, notes: [...full.notes!, { timestamp: "now", content: "third" }] }));
      expect(notes.getByText("third")).toBeTruthy();
      expect(notes.getByText("3")).toBeTruthy();
      expect((box as HTMLTextAreaElement).value).toBe("");
      expect(screen.getByText("Ticket other")).toBeTruthy();
    });

    it("adds a note with Ctrl/Cmd+Enter but not plain Enter or an empty draft", async () => {
      fetchMock.mockResolvedValue([full]);
      noteMock.mockResolvedValue(full);
      const { user } = renderPanel();
      await openEditFor(user, full);
      const box = within(screen.getByTestId("kanban-ticket-notes")).getByPlaceholderText(/Add a note/);
      fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
      expect(noteMock).not.toHaveBeenCalled();
      await user.type(box, "hi");
      fireEvent.keyDown(box, { key: "Enter" });
      fireEvent.keyDown(box, { key: "x", metaKey: true });
      expect(noteMock).not.toHaveBeenCalled();
      fireEvent.keyDown(box, { key: "Enter", metaKey: true });
      expect(noteMock).toHaveBeenCalledTimes(1);
      await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe(""));
      await user.type(box, "again");
      fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
      expect(noteMock).toHaveBeenCalledTimes(2);
    });

    it.each([
      [new Error("disk full"), "disk full"],
      ["boom", "Failed to add note"],
    ])("shows a note failure (%s) and keeps the draft", async (failure, message) => {
      fetchMock.mockResolvedValue([full]);
      noteMock.mockRejectedValue(failure);
      const { user } = renderPanel();
      await openEditFor(user, full);
      const notes = within(screen.getByTestId("kanban-ticket-notes"));
      const box = notes.getByPlaceholderText(/Add a note/) as HTMLTextAreaElement;
      await user.type(box, "keep");
      await user.click(notes.getByRole("button", { name: "Add note" }));
      expect(await notes.findByText(message)).toBeTruthy();
      expect(box.value).toBe("keep");
    });

    it("reads notes off the live list and falls back to the opened ticket once it's gone", async () => {
      fetchMock.mockResolvedValue([full]);
      const { user } = renderPanel();
      await openEditFor(user, full);
      fetchMock.mockResolvedValue([{ ...full, notes: [{ timestamp: "later", content: "from chat" }] }]);
      act(() => lastOnEvent()({ type: "ticket.updated" } as never));
      const notes = within(screen.getByTestId("kanban-ticket-notes"));
      expect(await notes.findByText("from chat")).toBeTruthy();

      fetchMock.mockResolvedValue([]);
      act(() => lastOnEvent()({ type: "ticket.deleted" } as never));
      expect(await notes.findByText("first note")).toBeTruthy();
    });
  });
});
