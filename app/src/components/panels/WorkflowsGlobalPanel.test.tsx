import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchWorkflowRun, fetchWorkflowRuns, fetchWorkflows, type WorkflowRun } from "../../api/workflows";
import { makeWorkflowDef, makeWorkflowRun } from "../../test/panels-tasks-workflows-fixtures";
import type { Channel } from "../../types";
import { WorkflowsGlobalPanel, type WorkflowsGlobalPanelHandle } from "./WorkflowsGlobalPanel";

vi.mock("../../api/workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/workflows")>()),
  fetchWorkflowRun: vi.fn(),
  fetchWorkflowRuns: vi.fn(),
  fetchWorkflows: vi.fn(),
}));

// The header info fetches git and PR state of its own; the stub shows which
// channel it was handed.
vi.mock("../layout/ChannelHeaderInfo", () => ({
  ChannelHeaderInfo: ({ channel }: { channel: Channel }) => <div data-testid="channel-info">{channel.id}</div>,
}));

// The graph needs layout jsdom lacks.
vi.mock("./WorkflowGraph", () => ({ WorkflowGraph: () => <div data-testid="graph" /> }));

const manyRuns = (n: number): WorkflowRun[] => Array.from({ length: n }, (_, i) => makeWorkflowRun({ id: `run-${i}`, workflow_name: i === 0 ? "lint" : "deploy" }));

function renderPanel(props: Partial<Parameters<typeof WorkflowsGlobalPanel>[0]> = {}) {
  const onClose = vi.fn();
  const ref = createRef<WorkflowsGlobalPanelHandle>();
  const view = render(<WorkflowsGlobalPanel ref={ref} onClose={onClose} {...props} />);
  return { onClose, ref, ...view };
}

beforeEach(() => {
  vi.mocked(fetchWorkflowRuns).mockResolvedValue([]);
  vi.mocked(fetchWorkflows).mockResolvedValue([]);
  vi.mocked(fetchWorkflowRun).mockResolvedValue({ run: makeWorkflowRun(), node_runs: [] } as never);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe("WorkflowsGlobalPanel", () => {
  it("loads runs across all channels and shows the empty state", async () => {
    renderPanel();
    expect(await screen.findByText("No workflow runs")).toBeTruthy();
    expect(fetchWorkflowRuns).toHaveBeenCalledWith(undefined, 50, 0);
    expect(fetchWorkflows).toHaveBeenCalledWith(undefined);
    expect(screen.getByText("Workflows (0)")).toBeTruthy();
    expect(screen.getByText("Select a workflow run to view details")).toBeTruthy();
    expect(screen.queryByTitle("Search messages (Cmd+K)")).toBeNull();
    expect(screen.queryByTestId("channel-info")).toBeNull();
    expect(screen.queryByText("End of history")).toBeNull();
  });

  it("leaves room for the window controls when the sidebar is closed", () => {
    renderPanel({ sidebarOpen: false });
    const dragRow = screen.getByTestId("workflows-panel").firstElementChild as HTMLElement;
    expect(dragRow.style.paddingLeft).toBe("76px");
  });

  it("shows the channel info and a palette button labelled for the platform", async () => {
    const user = userEvent.setup();
    const onOpenPalette = vi.fn();
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    const { unmount } = renderPanel({ onOpenPalette, channel: { id: "chan-9" } as Channel, sidebarOpen: true });
    expect(screen.getByTestId("channel-info").textContent).toBe("chan-9");
    const dragRow = screen.getByTestId("workflows-panel").firstElementChild as HTMLElement;
    expect(dragRow.style.paddingLeft).toBe("4px");
    const palette = screen.getByTitle("Search messages (Cmd+K)");
    expect(palette.textContent).toBe("⌘K");
    await user.click(palette);
    expect(onOpenPalette).toHaveBeenCalledOnce();
    unmount();

    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    renderPanel({ onOpenPalette });
    expect(screen.getByTitle("Search messages (Cmd+K)").textContent).toBe("Ctrl+K");
  });

  it("closes from the close button, which highlights on hover", async () => {
    const user = userEvent.setup();
    const { onClose } = renderPanel();
    const close = screen.getByTitle("Close panel");
    fireEvent.mouseEnter(close);
    expect(close.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(close);
    expect(close.style.backgroundColor).toBe("transparent");
    await user.click(close);
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("closes the start dialog on Escape before closing the panel", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchWorkflows).mockResolvedValue([makeWorkflowDef("deploy")]);
    const { onClose } = renderPanel();
    fireEvent.keyDown(window, { key: "Enter" });
    expect(onClose).not.toHaveBeenCalled();

    await user.click(await screen.findByTestId("workflow-run-deploy"));
    expect(screen.getByTestId("start-workflow-dialog")).toBeTruthy();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("start-workflow-dialog")).toBeNull();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("closes the start dialog from its Cancel button", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchWorkflows).mockResolvedValue([makeWorkflowDef("deploy")]);
    renderPanel();
    await user.click(await screen.findByTestId("workflow-run-deploy"));
    const dialog = screen.getByTestId("start-workflow-dialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByTestId("start-workflow-dialog")).toBeNull();
  });

  it("lists runs, selects one, and links to its channel", async () => {
    const user = userEvent.setup();
    const onSelectChannel = vi.fn();
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([makeWorkflowRun({ channel_id: "chan-1", channel_name: "ops" })]);
    renderPanel({ onSelectChannel });
    const row = await screen.findByTestId("workflow-run-row-run-1");
    expect(screen.getByText("Workflows (1)")).toBeTruthy();
    // A short first page means there is nothing more to load.
    expect(screen.getByText("End of history")).toBeTruthy();

    await user.click(row);
    await waitFor(() => expect(fetchWorkflowRun).toHaveBeenCalledWith("run-1"));
    expect(screen.queryByText("Select a workflow run to view details")).toBeNull();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();

    await user.click(within(row).getByTitle("Go to channel"));
    expect(onSelectChannel).toHaveBeenCalledWith("chan-1");
  });

  it("filters runs to the selected workflow", async () => {
    const user = userEvent.setup();
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([makeWorkflowRun()]);
    vi.mocked(fetchWorkflows).mockResolvedValue([makeWorkflowDef("deploy"), makeWorkflowDef("lint")]);
    renderPanel();
    await user.click(await screen.findByTestId("workflow-def-lint"));
    expect(screen.getByText("No runs for lint")).toBeTruthy();
    expect(screen.queryByTestId("workflow-run-row-run-1")).toBeNull();
    expect(screen.queryByText("End of history")).toBeNull();

    await user.click(screen.getByTestId("workflow-def-deploy"));
    expect(screen.getByTestId("workflow-run-row-run-1")).toBeTruthy();
  });

  it("loads the next page when scrolled near the bottom", async () => {
    vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce(manyRuns(50));
    let resolvePage!: (runs: WorkflowRun[]) => void;
    vi.mocked(fetchWorkflowRuns).mockImplementationOnce(() => new Promise((r) => (resolvePage = r)));
    renderPanel();
    const list = (await screen.findByTestId("workflow-run-row-run-0")).parentElement!;
    expect(screen.queryByText("End of history")).toBeNull();

    // Far from the bottom: nothing loads.
    Object.defineProperty(list, "scrollHeight", { configurable: true, value: 5000 });
    fireEvent.scroll(list);
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(1);

    Object.defineProperty(list, "scrollHeight", { configurable: true, value: 100 });
    fireEvent.scroll(list);
    expect(fetchWorkflowRuns).toHaveBeenLastCalledWith(undefined, 50, 50);
    expect(await screen.findByText("Loading more…")).toBeTruthy();

    await act(async () => resolvePage([]));
    expect(screen.queryByText("Loading more…")).toBeNull();
    expect(screen.getByText("End of history")).toBeTruthy();
  });

  it("exposes the workflow event handler through its ref", async () => {
    const { ref } = renderPanel();
    await screen.findByText("No workflow runs");
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(1);
    await act(async () => ref.current!.handleWorkflowEvent({ type: "workflow.run_started", data: { run_id: "x" } } as never));
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(2);
  });
});
