import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionsResponse } from "../../api/loopApi";
import { createThread, fetchSessions, setSession } from "../../api/loopApi";
import { SessionsPanel } from "./SessionsPanel";

vi.mock("../../api/loopApi", () => ({
  createThread: vi.fn(),
  fetchSessions: vi.fn(),
  setSession: vi.fn(),
}));

// The real Terminal opens an xterm over a WebSocket; record its props and
// expose its session-end callback as a button.
const terminalProps = vi.hoisted(() => vi.fn());
vi.mock("./Terminal", () => ({
  Terminal: (props: { instanceId: string; onSessionEnd?: () => void }) => {
    terminalProps(props);
    return (
      <div data-testid="terminal">
        {props.instanceId}
        {props.onSessionEnd && <button onClick={props.onSessionEnd}>end session</button>}
      </div>
    );
  },
}));

const fetchMock = vi.mocked(fetchSessions);
const createThreadMock = vi.mocked(createThread);
const setSessionMock = vi.mocked(setSession);

const NOW = new Date("2026-06-01T12:00:00Z").getTime();
const ago = (ms: number) => new Date(NOW - ms).toISOString();

const response = (over: Partial<SessionsResponse> = {}): SessionsResponse => ({
  current_session_id: "cur00000-1111-2222",
  sessions: [
    { session_id: "cur00000-1111-2222", last_modified: ago(5 * 60_000), last_message: "Fix the Build" },
    { session_id: "aaaa1111-2222-3333", last_modified: ago(3 * 3_600_000), last_message: "write docs" },
    { session_id: "bbbb2222-3333-4444", last_modified: ago(2 * 86_400_000) },
  ],
  imported_session_ids: ["bbbb2222-3333-4444"],
  ...over,
});

const rowOf = (id: string) => screen.getByTitle(id).closest("[style*='cursor: pointer']") as HTMLElement;

describe("SessionsPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(Date, "now").mockReturnValue(NOW);
    fetchMock.mockResolvedValue(response());
    createThreadMock.mockResolvedValue("thread-1");
    setSessionMock.mockResolvedValue({ deferred: false });
  });
  afterEach(() => vi.restoreAllMocks());

  it("lists available and imported sessions with ages and the current one marked", async () => {
    render(<SessionsPanel channelId="ch-1" />);
    expect(await screen.findByText("cur00000-111")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledWith("ch-1");
    expect(screen.getByText("5m")).toBeTruthy();
    expect(screen.getByText("3h")).toBeTruthy();
    expect(screen.getByText("2d")).toBeTruthy();
    expect(screen.getByText("Available")).toBeTruthy();
    expect(screen.getByText("Imported")).toBeTruthy();
    expect(screen.getByText("*")).toBeTruthy();
    // Only available sessions can be imported; the current one can't be resumed.
    expect(screen.getAllByRole("button", { name: "+ import as thread" })).toHaveLength(2);
    expect(screen.getAllByTestId("session-resume-here")).toHaveLength(2);
    expect(screen.getByText("Fix the Build")).toBeTruthy();
    expect(screen.getByText("Select a session to resume")).toBeTruthy();
  });

  it("drops the section headers when only one section has sessions", async () => {
    fetchMock.mockResolvedValue(response({ imported_session_ids: [] }));
    const { unmount } = render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    expect(screen.queryByText("Available")).toBeNull();
    expect(screen.queryByText("Imported")).toBeNull();
    unmount();

    fetchMock.mockResolvedValue(response({ imported_session_ids: response().sessions.map((s) => s.session_id) }));
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    expect(screen.queryByText("Available")).toBeNull();
    expect(screen.queryByText("Imported")).toBeNull();
    expect(screen.queryByRole("button", { name: "+ import as thread" })).toBeNull();
  });

  it("treats a missing imported list as none imported", async () => {
    fetchMock.mockResolvedValue({ ...response(), imported_session_ids: undefined as unknown as string[] });
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    expect(screen.getAllByRole("button", { name: "+ import as thread" })).toHaveLength(3);
  });

  it("shows the empty state, and keeps it when the fetch fails", async () => {
    fetchMock.mockRejectedValue(new Error("down"));
    render(<SessionsPanel channelId="ch-1" />);
    expect(await screen.findByText("No sessions found")).toBeTruthy();
  });

  it("filters by id or case-insensitive last message", async () => {
    const user = userEvent.setup();
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    const box = screen.getByPlaceholderText("Filter sessions...");
    await user.type(box, "aaaa");
    expect(screen.queryByText("cur00000-111")).toBeNull();
    expect(screen.getByText("aaaa1111-222")).toBeTruthy();
    await user.clear(box);
    await user.type(box, "fix THE");
    expect(screen.getByText("cur00000-111")).toBeTruthy();
    expect(screen.queryByText("aaaa1111-222")).toBeNull();
    await user.clear(box);
    await user.type(box, "zzz");
    expect(screen.getByText("No matches")).toBeTruthy();
  });

  it("refreshes the list", async () => {
    const user = userEvent.setup();
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    fetchMock.mockResolvedValue(response({ sessions: [{ session_id: "new00000-0000", last_modified: ago(0) }], imported_session_ids: [] }));
    const refresh = screen.getByTitle("Refresh sessions");
    fireEvent.mouseEnter(refresh);
    fireEvent.mouseLeave(refresh);
    await user.click(refresh);
    expect(await screen.findByText("new00000-000")).toBeTruthy();
    expect(screen.getByText("0m")).toBeTruthy();
  });

  it("imports a session as a thread and moves it to the imported section", async () => {
    const onStatusChange = vi.fn();
    const user = userEvent.setup();
    render(<SessionsPanel channelId="ch-1" onStatusChange={onStatusChange} />);
    await screen.findByText("cur00000-111");
    const btn = rowOf("aaaa1111-2222-3333").querySelector("button[title='Import as thread']") as HTMLElement;
    fireEvent.mouseEnter(btn);
    fireEvent.mouseLeave(btn, { relatedTarget: rowOf("aaaa1111-2222-3333") });
    await user.click(btn);
    expect(createThreadMock).toHaveBeenCalledWith("ch-1", "session aaaa1111", "aaaa1111-2222-3333");
    await waitFor(() => expect(onStatusChange).toHaveBeenCalledOnce());
    expect(rowOf("aaaa1111-2222-3333").querySelector("button[title='Import as thread']")).toBeNull();
    // Importing doesn't select the row.
    expect(screen.queryByTestId("terminal")).toBeNull();
  });

  it("imports without a status callback", async () => {
    const user = userEvent.setup();
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    await user.click(rowOf("aaaa1111-2222-3333").querySelector("button[title='Import as thread']")!);
    await waitFor(() => expect(rowOf("aaaa1111-2222-3333").querySelector("button[title='Import as thread']")).toBeNull());
  });

  describe("resume here", () => {
    const resumeBtn = () => rowOf("aaaa1111-2222-3333").querySelector("[data-testid='session-resume-here']") as HTMLElement;

    it("makes the session current", async () => {
      const onStatusChange = vi.fn();
      const user = userEvent.setup();
      render(<SessionsPanel channelId="ch-1" onStatusChange={onStatusChange} />);
      await screen.findByText("cur00000-111");
      fireEvent.mouseEnter(resumeBtn());
      fireEvent.mouseLeave(resumeBtn(), { relatedTarget: rowOf("aaaa1111-2222-3333") });
      await user.click(resumeBtn());
      expect(setSessionMock).toHaveBeenCalledWith("ch-1", "aaaa1111-2222-3333");
      await waitFor(() => expect(onStatusChange).toHaveBeenCalledOnce());
      expect(resumeBtn()).toBeNull();
      expect(screen.queryByTestId("session-resume-note")).toBeNull();
      expect(screen.queryByTestId("terminal")).toBeNull();
    });

    it("notes a switch deferred until the current run ends", async () => {
      setSessionMock.mockResolvedValue({ deferred: true });
      const onStatusChange = vi.fn();
      const user = userEvent.setup();
      render(<SessionsPanel channelId="ch-1" onStatusChange={onStatusChange} />);
      await screen.findByText("cur00000-111");
      await user.click(resumeBtn());
      expect((await screen.findByTestId("session-resume-note")).textContent).toBe("Switches to aaaa1111 when the current run ends.");
      expect(onStatusChange).not.toHaveBeenCalled();
      expect(resumeBtn()).not.toBeNull();
    });

    it.each([
      [new Error("run busy"), "run busy"],
      ["plain failure", "plain failure"],
    ])("shows a failed switch (%s)", async (err, text) => {
      setSessionMock.mockRejectedValue(err);
      const user = userEvent.setup();
      render(<SessionsPanel channelId="ch-1" />);
      await screen.findByText("cur00000-111");
      await user.click(resumeBtn());
      expect((await screen.findByTestId("session-resume-note")).textContent).toBe(text);
    });

    it("works without a status callback and clears an earlier note", async () => {
      setSessionMock.mockResolvedValueOnce({ deferred: true });
      const user = userEvent.setup();
      render(<SessionsPanel channelId="ch-1" />);
      await screen.findByText("cur00000-111");
      await user.click(resumeBtn());
      await screen.findByTestId("session-resume-note");
      await user.click(resumeBtn());
      await waitFor(() => expect(screen.queryByTestId("session-resume-note")).toBeNull());
    });
  });

  it("opens a selected session in a terminal resuming it", async () => {
    const onStatusChange = vi.fn();
    const user = userEvent.setup();
    render(<SessionsPanel channelId="ch-1" onStatusChange={onStatusChange} />);
    await screen.findByText("cur00000-111");
    const row = rowOf("aaaa1111-2222-3333");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe("rgba(255, 255, 255, 0.04)");
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe("transparent");
    await user.click(screen.getByText("aaaa1111-222"));
    expect(screen.getByTestId("terminal").textContent).toBe("session-aaaa1111");
    expect(terminalProps).toHaveBeenLastCalledWith(expect.objectContaining({ channelId: "ch-1", target: "agent", claudeSessionId: "aaaa1111-2222-3333", onStatusChange }));
    // Hovering the selected row leaves its highlight alone.
    const selected = rowOf("aaaa1111-2222-3333");
    const bg = selected.style.background;
    fireEvent.mouseEnter(selected);
    fireEvent.mouseLeave(selected);
    expect(selected.style.background).toBe(bg);
  });

  it("starts a new session and reloads the list when it ends", async () => {
    const user = userEvent.setup();
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    await user.click(screen.getByText("aaaa1111-222"));
    const newBtn = screen.getByTitle("Start a new session");
    fireEvent.mouseEnter(newBtn);
    fireEvent.mouseLeave(newBtn);
    await user.click(newBtn);
    expect(screen.getByTestId("terminal").textContent).toContain(`new-${NOW}`);
    expect(terminalProps).toHaveBeenLastCalledWith(expect.objectContaining({ newSession: true, instanceId: `new-${NOW}` }));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "end session" }));
    expect(screen.getByText("Select a session to resume")).toBeTruthy();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  });

  it("selecting a row leaves a new session", async () => {
    const user = userEvent.setup();
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    await user.click(screen.getByTitle("Start a new session"));
    await user.click(screen.getByText("aaaa1111-222"));
    expect(screen.getByTestId("terminal").textContent).toBe("session-aaaa1111");
  });

  it("resizes the list by dragging the divider within bounds", async () => {
    render(<SessionsPanel channelId="ch-1" />);
    await screen.findByText("cur00000-111");
    const list = screen.getByTestId("sessions-panel").firstElementChild as HTMLElement;
    const divider = list.nextElementSibling as HTMLElement;
    fireEvent.mouseDown(divider);
    fireEvent.mouseMove(window, { movementX: 50 });
    expect(list.style.width).toBe("430px");
    fireEvent.mouseMove(window, { movementX: 1000 });
    expect(list.style.width).toBe("500px");
    fireEvent.mouseMove(window, { movementX: -1000 });
    expect(list.style.width).toBe("180px");
    fireEvent.mouseUp(window);
    fireEvent.mouseMove(window, { movementX: 50 });
    expect(list.style.width).toBe("180px");
  });
});
