import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AuditFileEntry } from "../../api/audit";
import { deleteAuditFile, fetchAuditFiles } from "../../api/audit";
import { AuditPanel } from "./AuditPanel";

vi.mock("../../api/audit", () => ({
  fetchAuditFiles: vi.fn(),
  deleteAuditFile: vi.fn(),
}));

// The real Terminal opens an xterm over a WebSocket; record its props instead.
const terminalProps = vi.hoisted(() => vi.fn());
vi.mock("./Terminal", () => ({
  Terminal: (props: Record<string, unknown>) => {
    terminalProps(props);
    return <div data-testid="terminal">{String(props.instanceId)}</div>;
  },
}));

const fetchMock = vi.mocked(fetchAuditFiles);
const deleteMock = vi.mocked(deleteAuditFile);

const file = (date: string, size: number): AuditFileEntry => ({ date, size, last_modified: "" });

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: Error) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function setScroll(el: HTMLElement, scrollTop: number, clientHeight: number, scrollHeight: number) {
  Object.defineProperty(el, "scrollTop", { configurable: true, value: scrollTop });
  Object.defineProperty(el, "clientHeight", { configurable: true, value: clientHeight });
  Object.defineProperty(el, "scrollHeight", { configurable: true, value: scrollHeight });
}

describe("AuditPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    deleteMock.mockResolvedValue(undefined);
  });

  it("lists the channel's audit files with formatted dates and sizes", async () => {
    fetchMock.mockResolvedValue({ files: [file("2026-01-02", 512), file("2026-01-01", 2048), file("bad-date", 3 * 1024 * 1024)], total: 3 });
    render(<AuditPanel channelId="ch-1" />);
    expect(await screen.findByText("2026-01-02")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledWith("ch-1", 0, 50);
    expect(screen.getByText("Audit logs (3)")).toBeTruthy();
    expect(screen.getByText("512 B")).toBeTruthy();
    expect(screen.getByText("2.0 KB")).toBeTruthy();
    expect(screen.getByText("3.0 MB")).toBeTruthy();
    expect(screen.getByText(new Date("2026-01-02T00:00:00").toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }))).toBeTruthy();
    // An unparseable date is shown as-is in both places.
    expect(screen.getAllByText("bad-date")).toHaveLength(2);
    expect(screen.getByText("Select an audit log to view")).toBeTruthy();
  });

  it("shows the empty state and a loading row, and ignores a failed fetch", async () => {
    const d = deferred<never>();
    fetchMock.mockReturnValue(d.promise);
    render(<AuditPanel channelId="ch-1" />);
    expect(screen.getByText("Loading...")).toBeTruthy();
    expect(screen.queryByText("No audit logs yet")).toBeNull();
    expect(screen.getByText("Audit logs")).toBeTruthy();
    await act(async () => d.reject(new Error("down")));
    expect(await screen.findByText("No audit logs yet")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("tails the selected file in the agent container", async () => {
    fetchMock.mockResolvedValue({ files: [file("2026-01-02", 10)], total: 1 });
    const user = userEvent.setup();
    render(<AuditPanel channelId="ch-1" />);
    await user.click(await screen.findByText("2026-01-02"));
    expect(screen.getByText("agentgate-2026-01-02.jsonl")).toBeTruthy();
    expect(screen.getByTestId("terminal").textContent).toBe("audit-tail-2026-01-02");
    expect(terminalProps).toHaveBeenLastCalledWith(
      expect.objectContaining({
        channelId: "ch-1",
        target: "agent",
        hideActions: true,
        cmd: ["tail", "-f", "-n", "100", "/var/log/loop-gate/agentgate-2026-01-02.jsonl"],
      }),
    );
  });

  it("refreshes the list from the first page", async () => {
    fetchMock.mockResolvedValueOnce({ files: [file("2026-01-02", 10)], total: 1 });
    const user = userEvent.setup();
    render(<AuditPanel channelId="ch-1" />);
    await screen.findByText("2026-01-02");
    fetchMock.mockResolvedValueOnce({ files: [file("2026-02-02", 10)], total: 1 });
    const refresh = screen.getByTitle("Refresh");
    fireEvent.mouseEnter(refresh);
    fireEvent.mouseLeave(refresh);
    await user.click(refresh);
    expect(await screen.findByText("2026-02-02")).toBeTruthy();
    expect(screen.queryByText("2026-01-02")).toBeNull();
    expect(fetchMock).toHaveBeenLastCalledWith("ch-1", 0, 50);
  });

  it("doesn't start a second fetch while one is loading", async () => {
    const d = deferred<{ files: AuditFileEntry[]; total: number }>();
    fetchMock.mockReturnValue(d.promise);
    render(<AuditPanel channelId="ch-1" />);
    fireEvent.click(screen.getByTitle("Refresh"));
    const list = screen.getByText("Loading...").parentElement!;
    setScroll(list, 1000, 100, 1000);
    fireEvent.scroll(list);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await act(async () => d.resolve({ files: [], total: 0 }));
  });

  it("loads the next page when scrolled near the bottom, until all are loaded", async () => {
    const first = Array.from({ length: 50 }, (_, i) => file(`2026-01-${String(i).padStart(2, "0")}`, 1));
    fetchMock.mockResolvedValueOnce({ files: first, total: 51 });
    render(<AuditPanel channelId="ch-1" />);
    const [firstRow] = await screen.findAllByText("2026-01-00");
    const list = firstRow!.closest("[style*='overflow-y']") as HTMLElement;

    setScroll(list, 0, 100, 1000);
    fireEvent.scroll(list);
    expect(fetchMock).toHaveBeenCalledTimes(1);

    fetchMock.mockResolvedValueOnce({ files: [file("2026-03-01", 1)], total: 51 });
    setScroll(list, 830, 100, 1000);
    fireEvent.scroll(list);
    expect(fetchMock).toHaveBeenLastCalledWith("ch-1", 50, 50);
    expect(await screen.findByText("2026-03-01")).toBeTruthy();
    expect(screen.getAllByText("2026-01-00").length).toBeGreaterThan(0);

    fireEvent.scroll(list);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("resets and reloads when the channel changes", async () => {
    fetchMock.mockResolvedValueOnce({ files: [file("2026-01-02", 10)], total: 1 });
    const user = userEvent.setup();
    const { rerender } = render(<AuditPanel channelId="ch-1" />);
    await user.click(await screen.findByText("2026-01-02"));
    fetchMock.mockResolvedValueOnce({ files: [], total: 0 });
    rerender(<AuditPanel channelId="ch-2" />);
    expect(await screen.findByText("No audit logs yet")).toBeTruthy();
    expect(fetchMock).toHaveBeenLastCalledWith("ch-2", 0, 50);
    expect(screen.getByText("Select an audit log to view")).toBeTruthy();
  });

  describe("delete", () => {
    async function setup() {
      fetchMock.mockResolvedValue({ files: [file("2026-01-02", 10), file("2026-01-03", 10)], total: 2 });
      const user = userEvent.setup();
      render(<AuditPanel channelId="ch-1" />);
      const row = (await screen.findByText("2026-01-02")).closest("[style*='position: relative']") as HTMLElement;
      return { user, row };
    }

    it("shows Delete on hover only", async () => {
      const { row } = await setup();
      expect(screen.queryByTitle("Delete audit log")).toBeNull();
      fireEvent.mouseEnter(row);
      const del = screen.getByTitle("Delete audit log");
      fireEvent.mouseEnter(del);
      expect(del.style.borderColor).not.toBe("");
      fireEvent.mouseLeave(del, { relatedTarget: row });
      fireEvent.mouseLeave(screen.getByText("2026-01-03").closest("[style*='position: relative']")!);
      expect(screen.getByTitle("Delete audit log")).toBeTruthy();
      fireEvent.mouseLeave(row);
      expect(screen.queryByTitle("Delete audit log")).toBeNull();
    });

    it("deletes the selected file after confirming and clears the selection", async () => {
      const { user, row } = await setup();
      await user.click(screen.getByText("2026-01-02"));
      expect(screen.getByTestId("terminal")).toBeTruthy();
      fireEvent.mouseEnter(row);
      fireEvent.click(screen.getByTitle("Delete audit log"));
      expect(screen.getByText("Delete?")).toBeTruthy();
      fireEvent.mouseLeave(row);
      // The confirm keeps the button visible after the pointer leaves.
      expect(screen.getByTitle("Delete audit log")).toBeTruthy();
      const yes = screen.getByRole("button", { name: "Yes" });
      fireEvent.mouseEnter(yes);
      fireEvent.mouseLeave(yes, { relatedTarget: row });
      fireEvent.mouseDown(yes);
      fireEvent.click(yes);
      expect(deleteMock).toHaveBeenCalledWith("ch-1", "2026-01-02");
      await waitFor(() => expect(screen.queryByText("2026-01-02")).toBeNull());
      expect(screen.getByText("Audit logs (1)")).toBeTruthy();
      expect(screen.getByText("Select an audit log to view")).toBeTruthy();
    });

    it("keeps another file selected when deleting a different one", async () => {
      const { user, row } = await setup();
      await user.click(screen.getByText("2026-01-03"));
      fireEvent.mouseEnter(row);
      await user.click(screen.getByTitle("Delete audit log"));
      await user.click(screen.getByRole("button", { name: "Yes" }));
      await waitFor(() => expect(screen.queryByText("2026-01-02")).toBeNull());
      expect(screen.getByTestId("terminal").textContent).toBe("audit-tail-2026-01-03");
    });

    it("keeps the row when the delete fails", async () => {
      deleteMock.mockRejectedValue(new Error("nope"));
      const { user, row } = await setup();
      fireEvent.mouseEnter(row);
      await user.click(screen.getByTitle("Delete audit log"));
      await user.click(screen.getByRole("button", { name: "Yes" }));
      expect(deleteMock).toHaveBeenCalled();
      expect(screen.getByText("2026-01-02")).toBeTruthy();
      expect(screen.getByText("Audit logs (2)")).toBeTruthy();
    });

    it("cancels on No and on a click outside the popover, but not inside it", async () => {
      const { user, row } = await setup();
      fireEvent.mouseEnter(row);
      await user.click(screen.getByTitle("Delete audit log"));
      const no = screen.getByRole("button", { name: "No" });
      fireEvent.mouseEnter(no);
      fireEvent.mouseLeave(no, { relatedTarget: row });
      await user.click(no);
      expect(screen.queryByText("Delete?")).toBeNull();

      await user.click(screen.getByTitle("Delete audit log"));
      fireEvent.mouseDown(screen.getByText("Delete?"));
      // A click inside the popover doesn't select the row under it.
      fireEvent.click(screen.getByText("Delete?"));
      expect(screen.getByText("Delete?")).toBeTruthy();
      fireEvent.mouseDown(document.body);
      expect(screen.queryByText("Delete?")).toBeNull();
      expect(deleteMock).not.toHaveBeenCalled();
      expect(screen.getByText("Select an audit log to view")).toBeTruthy();
    });
  });

  it("resizes the list by dragging the divider within bounds", async () => {
    fetchMock.mockResolvedValue({ files: [], total: 0 });
    render(<AuditPanel channelId="ch-1" />);
    await screen.findByText("No audit logs yet");
    const panel = screen.getByTestId("audit-panel");
    const list = panel.firstElementChild as HTMLElement;
    const divider = list.nextElementSibling as HTMLElement;
    fireEvent.mouseDown(divider);
    fireEvent.mouseMove(window, { movementX: 50 });
    expect(list.style.width).toBe("350px");
    fireEvent.mouseMove(window, { movementX: 1000 });
    expect(list.style.width).toBe("500px");
    fireEvent.mouseMove(window, { movementX: -1000 });
    expect(list.style.width).toBe("180px");
    fireEvent.mouseUp(window);
    fireEvent.mouseMove(window, { movementX: 50 });
    expect(list.style.width).toBe("180px");
  });
});
