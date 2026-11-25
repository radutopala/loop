import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchWorkflowRun, fetchWorkflowRuns, fetchWorkflows, type WorkflowRun } from "../../api/workflows";
import { useEventStream } from "../../hooks/useEventStream";
import { makeWorkflowDef, makeWorkflowRun } from "../../test/panels-tasks-workflows-fixtures";
import type { WSEvent } from "../../types";
import { WorkflowsLayoutPanel } from "./WorkflowsLayoutPanel";

vi.mock("../../api/workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/workflows")>()),
  fetchWorkflowRun: vi.fn(),
  fetchWorkflowRuns: vi.fn(),
  fetchWorkflows: vi.fn(),
}));

// The event stream opens a WebSocket; the mock records the handler instead.
vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

// The graph needs layout jsdom lacks.
vi.mock("./WorkflowGraph", () => ({ WorkflowGraph: () => <div data-testid="graph" /> }));

const lastOnEvent = () => vi.mocked(useEventStream).mock.lastCall![0].onEvent;

beforeEach(() => {
  vi.mocked(fetchWorkflowRuns).mockResolvedValue([]);
  vi.mocked(fetchWorkflows).mockResolvedValue([]);
  vi.mocked(fetchWorkflowRun).mockResolvedValue({ run: makeWorkflowRun(), node_runs: [] } as never);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("WorkflowsLayoutPanel", () => {
  it("loads the channel's runs and shows the empty state", async () => {
    render(<WorkflowsLayoutPanel channelId="chan-1" />);
    expect(await screen.findByText("No workflow runs")).toBeTruthy();
    expect(fetchWorkflowRuns).toHaveBeenCalledWith("chan-1", 50, 0);
    expect(fetchWorkflows).toHaveBeenCalledWith("chan-1");
    expect(screen.getByText("0 runs")).toBeTruthy();
    expect(vi.mocked(useEventStream).mock.lastCall![0].channelId).toBe("chan-1");
  });

  it("counts a single run, selects it, and ends the history", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([makeWorkflowRun()]);
    render(<WorkflowsLayoutPanel channelId="chan-1" />);
    const row = await screen.findByTestId("workflow-run-row-run-1");
    expect(screen.getByText("1 run")).toBeTruthy();
    expect(screen.getByText("End of history")).toBeTruthy();
    await user.click(row);
    await waitFor(() => expect(fetchWorkflowRun).toHaveBeenCalledWith("run-1"));
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  it("filters runs to the selected workflow", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([makeWorkflowRun({ id: "a" }), makeWorkflowRun({ id: "b" })]);
    vi.mocked(fetchWorkflows).mockResolvedValue([makeWorkflowDef("deploy"), makeWorkflowDef("lint", { scope: "project" })]);
    render(<WorkflowsLayoutPanel channelId="chan-1" />);
    expect(await screen.findByText("2 runs")).toBeTruthy();
    await user.click(await screen.findByTestId("workflow-def-lint"));
    expect(screen.getByText("No runs for lint")).toBeTruthy();
    expect(screen.queryByText("End of history")).toBeNull();
  });

  it("opens the start dialog from a workflow and closes it with Cancel", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchWorkflows).mockResolvedValue([makeWorkflowDef("deploy")]);
    render(<WorkflowsLayoutPanel channelId="chan-1" />);
    await user.click(await screen.findByTestId("workflow-run-deploy"));
    const dialog = screen.getByTestId("start-workflow-dialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByTestId("start-workflow-dialog")).toBeNull();
  });

  it("loads the next page when scrolled near the bottom", async () => {
    vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce(Array.from({ length: 50 }, (_, i) => makeWorkflowRun({ id: `r${i}` })));
    let resolvePage!: (runs: WorkflowRun[]) => void;
    vi.mocked(fetchWorkflowRuns).mockImplementationOnce(() => new Promise((r) => (resolvePage = r)));
    render(<WorkflowsLayoutPanel channelId="chan-1" />);
    const list = (await screen.findByTestId("workflow-run-row-r0")).parentElement!;

    Object.defineProperty(list, "scrollHeight", { configurable: true, value: 5000 });
    fireEvent.scroll(list);
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(1);

    Object.defineProperty(list, "scrollHeight", { configurable: true, value: 100 });
    fireEvent.scroll(list);
    expect(fetchWorkflowRuns).toHaveBeenLastCalledWith("chan-1", 50, 50);
    expect(await screen.findByText("Loading more…")).toBeTruthy();
    await act(async () => resolvePage([]));
    expect(screen.queryByText("Loading more…")).toBeNull();
    expect(screen.getByText("End of history")).toBeTruthy();
  });

  it("refreshes on workflow events only, with a handler that stays stable", async () => {
    const { rerender } = render(<WorkflowsLayoutPanel channelId="chan-1" />);
    await screen.findByText("No workflow runs");
    const onEvent = lastOnEvent();
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(1);

    await act(async () => onEvent({ type: "message.created", data: {} } as unknown as WSEvent));
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(1);

    await act(async () => onEvent({ type: "workflow.run_completed", data: { run_id: "x" } } as unknown as WSEvent));
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(2);

    rerender(<WorkflowsLayoutPanel channelId="chan-1" />);
    expect(lastOnEvent()).toBe(onEvent);
  });
});
