import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { stopLiveTask } from "../../api/live";
import type { BackgroundTask } from "../../types";
import { registerTerminalInput, registerWorkspace } from "../../uiBridge/registry";
import { SIDE_PANE } from "../../uiBridge/sideText";
import type { TerminalInput, WorkspaceController } from "../../uiBridge/types";
import { BackgroundTasksBar } from "./BackgroundTasksBar";

vi.mock("../../api/live", () => ({ stopLiveTask: vi.fn() }));

const stopMock = vi.mocked(stopLiveTask);

const cleanups: (() => void)[] = [];

// The chat's Shell view, open with a started shell, recording what it's sent.
function openShell(status: () => string = () => "running") {
  const sent: string[] = [];
  const ws = { view: () => ({ side: "shell" }), openSide: vi.fn() } as unknown as WorkspaceController;
  const t = { status, lastOutputAt: () => 1, send: (d: string) => sent.push(d) } as unknown as TerminalInput;
  cleanups.push(registerWorkspace("ch-1", ws), registerTerminalInput("ch-1", SIDE_PANE, t));
  return sent;
}

const task = (over: Partial<BackgroundTask> = {}): BackgroundTask => ({ id: "t1", type: "local_bash", description: "make test", started_at: new Date().toISOString(), ...over });

describe("BackgroundTasksBar", () => {
  beforeEach(() => {
    stopMock.mockReset();
  });
  afterEach(() => {
    for (const fn of cleanups.splice(0)) fn();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("lists each task with its kind, description and elapsed time", () => {
    render(<BackgroundTasksBar channelId="ch-1" tasks={[task(), task({ id: "t2", type: "weird", description: "" })]} stoppable={false} />);
    const rows = screen.getAllByTestId("background-task-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]!.textContent).toContain("Shell");
    expect(screen.getAllByTestId("background-task-description").map((e) => e.textContent)).toEqual(["make test", "Background task"]);
    expect(rows[1]!.textContent).toContain("Background");
    expect(screen.queryByTestId("background-task-stop")).toBeNull();
    expect(screen.queryByTestId("background-task-tail")).toBeNull();
  });

  it("shows the whole command on hover, and nothing for an undescribed task", () => {
    vi.useFakeTimers();
    render(<BackgroundTasksBar channelId="ch-1" tasks={[task({ description: "" }), task({ id: "t2" })]} stoppable={false} />);
    const [bare, described] = screen.getAllByTestId("background-task-description");
    fireEvent.mouseEnter(bare!.parentElement!);
    act(() => vi.advanceTimersByTime(250));
    expect(screen.queryByRole("tooltip")).toBeNull();
    fireEvent.mouseEnter(described!.parentElement!);
    act(() => vi.advanceTimersByTime(250));
    expect(screen.getByRole("tooltip").textContent).toBe("Shellmake test");
  });

  it("keeps the elapsed times current and stops ticking on unmount", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    const { unmount } = render(<BackgroundTasksBar channelId="ch-1" tasks={[task({ started_at: "2026-01-01T00:00:00Z" })]} stoppable={false} />);
    expect(screen.getByTestId("background-task-elapsed").textContent).toBe("0s");
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByTestId("background-task-elapsed").textContent).toBe("2s");
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stops a live session's task, disabled until the stop settles", async () => {
    let finish!: () => void;
    stopMock.mockReturnValue(new Promise<void>((r) => (finish = r)));
    render(<BackgroundTasksBar channelId="ch-1" tasks={[task(), task({ id: "" })]} stoppable />);
    const stops = screen.getAllByTestId("background-task-stop") as HTMLButtonElement[];
    expect(stops).toHaveLength(1);
    await userEvent.click(stops[0]!);
    expect(stopMock).toHaveBeenCalledWith("ch-1", "t1");
    expect(stops[0]!.disabled).toBe(true);
    await act(async () => finish());
    expect(stops[0]!.disabled).toBe(false);
  });

  it("logs a stop that fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    stopMock.mockRejectedValue(new Error("gone"));
    render(<BackgroundTasksBar channelId="ch-1" tasks={[task()]} stoppable />);
    const stop = screen.getByTestId("background-task-stop") as HTMLButtonElement;
    await userEvent.click(stop);
    await waitFor(() => expect(stop.disabled).toBe(false));
    expect(warn).toHaveBeenCalledWith("[loop] stopping the background task:", expect.any(Error));
  });

  it("tails a task's output in the Shell view", async () => {
    const sent = openShell();
    render(<BackgroundTasksBar channelId="ch-1" tasks={[task({ output_path: "/tmp/t1.output" })]} stoppable={false} />);
    const tail = screen.getByTestId("background-task-tail");
    expect(tail.getAttribute("title")).toBe("Follow its output in the Shell view\n/tmp/t1.output");
    await userEvent.click(tail);
    await waitFor(() => expect(sent).toHaveLength(2));
    expect(sent[0]).toBe("\x03");
    expect(sent[1]).toContain("tail -n +1 -F '/tmp/t1.output'");
    expect(screen.queryByTestId("background-task-tail-error")).toBeNull();
  });

  it("says why a Tail failed in its row, and clears it on the next try", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    render(<BackgroundTasksBar channelId="ch-1" tasks={[task({ output_path: "/tmp/t1.output" })]} stoppable={false} />);
    await userEvent.click(screen.getByTestId("background-task-tail"));
    const err = await screen.findByTestId("background-task-tail-error");
    expect(err.textContent).toBe("Can't tail: channel ch-1 isn't open");
    expect(warn).toHaveBeenCalledWith("[loop] following the background task's output:", expect.any(Error));
    const sent = openShell();
    await userEvent.click(screen.getByTestId("background-task-tail"));
    expect(screen.queryByTestId("background-task-tail-error")).toBeNull();
    await waitFor(() => expect(sent).toHaveLength(2));
  });

  it("shows a Tail that failed with something other than an Error", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    openShell(() => {
      throw "shell vanished";
    });
    render(<BackgroundTasksBar channelId="ch-1" tasks={[task({ output_path: "/tmp/t1.output" })]} stoppable={false} />);
    await userEvent.click(screen.getByTestId("background-task-tail"));
    expect((await screen.findByTestId("background-task-tail-error")).textContent).toBe("Can't tail: shell vanished");
  });
});
