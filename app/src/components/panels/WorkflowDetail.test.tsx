import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkflowDef, WorkflowNodeDef, WorkflowNodeRun, WorkflowRun } from "../../api/workflows";
import { darkColors } from "../../theme";
import { WorkflowDetail } from "./WorkflowDetail";

// The graph lays nodes out with measurements jsdom lacks; the stub lists the
// node ids it was given and forwards a click on each to onNodeClick.
vi.mock("./WorkflowGraph", () => ({
  WorkflowGraph: ({ defs, expandedNodeId, onNodeClick }: { defs: WorkflowNodeDef[]; expandedNodeId: string | null; onNodeClick: (id: string) => void }) => (
    <div data-testid="graph" data-expanded={expandedNodeId ?? ""}>
      {defs.map((d) => (
        <button key={d.id} onClick={() => onNodeClick(d.id)}>
          node {d.id}
        </button>
      ))}
    </div>
  ),
}));

function makeRun(over: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    id: "run-1",
    workflow_name: "deploy",
    channel_id: "",
    dir_path: "",
    worktree_path: "",
    status: "running",
    inputs: "",
    paused_node_id: "",
    error_text: "",
    started_at: new Date(Date.now() - 3000).toISOString(),
    finished_at: null,
    ...over,
  };
}

function nodeRun(over: Partial<WorkflowNodeRun>): WorkflowNodeRun {
  return {
    id: 1,
    run_id: "run-1",
    node_id: "n",
    iteration: 0,
    status: "success",
    input: "",
    session_id: "",
    output: "",
    error_text: "",
    attempt: 1,
    started_at: null,
    finished_at: null,
    last_heartbeat_at: null,
    ...over,
  };
}

type Props = Parameters<typeof WorkflowDetail>[0];

function setup(over: Partial<Props> = {}) {
  const props: Props = {
    selectedRun: makeRun(),
    selectedDef: null,
    nodeRuns: [],
    expandedNodeId: null,
    resumeResponse: "",
    confirmingDeleteId: null,
    colors: darkColors,
    onToggleNodeExpand: vi.fn(),
    onCancel: vi.fn(),
    onDelete: vi.fn(),
    onRetry: vi.fn(),
    onResume: vi.fn(),
    onResumeResponseChange: vi.fn(),
    onConfirmDeleteChange: vi.fn(),
    ...over,
  };
  const view = render(<WorkflowDetail {...props} />);
  return { props, ...view };
}

describe("WorkflowDetail", () => {
  it("prompts to pick a run when none is selected", () => {
    setup({ selectedRun: null });
    expect(screen.getByText("Select a workflow run to view details")).toBeTruthy();
    expect(screen.queryByTestId("graph")).toBeNull();
  });

  it("shows a running run with Cancel but no Retry, and an empty graph without a def", async () => {
    const user = userEvent.setup();
    const { props } = setup({ expandedNodeId: "x" });
    expect(screen.getByText("deploy")).toBeTruthy();
    expect(screen.getByText("RUNNING")).toBeTruthy();
    expect(screen.getByText("run-1")).toBeTruthy();
    expect(screen.getByText(/^Started \d+s ago$/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(props.onCancel).toHaveBeenCalledWith("run-1");
    const graph = screen.getByTestId("graph");
    expect(graph.dataset.expanded).toBe("x");
    expect(graph.children).toHaveLength(0);
  });

  it.each(["failed", "completed", "cancelled"] as const)("offers Retry for a %s run", async (status) => {
    const user = userEvent.setup();
    const { props } = setup({ selectedRun: makeRun({ status, finished_at: new Date().toISOString(), dir_path: "/w/p", error_text: "boom" }) });
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(props.onRetry).toHaveBeenCalledWith("run-1");
    expect(screen.getByText(/Took \d+s/)).toBeTruthy();
    expect(screen.getByText("/w/p")).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
  });

  it("renders an unknown status in the dim colour", () => {
    setup({ selectedRun: makeRun({ status: "mystery" as WorkflowRun["status"] }) });
    expect(screen.getByText("MYSTERY")).toBeTruthy();
  });

  it("forwards graph node clicks", async () => {
    const user = userEvent.setup();
    const def: WorkflowDef = { name: "deploy", description: "", nodes: [{ id: "build", type: "bash" }] };
    const { props } = setup({ selectedDef: def });
    await user.click(screen.getByRole("button", { name: "node build" }));
    expect(props.onToggleNodeExpand).toHaveBeenCalledWith("build");
  });

  describe("delete confirmation", () => {
    it("asks to confirm", async () => {
      const user = userEvent.setup();
      const { props } = setup();
      expect(screen.queryByText("Delete?")).toBeNull();
      await user.click(screen.getByRole("button", { name: "Delete" }));
      expect(props.onConfirmDeleteChange).toHaveBeenCalledWith("run-1");
    });

    it("only shows the popover for this run", () => {
      setup({ confirmingDeleteId: "other" });
      expect(screen.queryByText("Delete?")).toBeNull();
    });

    it("deletes on Yes and dismisses on No, with hover styling", async () => {
      const user = userEvent.setup();
      const { props } = setup({ confirmingDeleteId: "run-1" });
      const yes = screen.getByRole("button", { name: "Yes" });
      const no = screen.getByRole("button", { name: "No" });

      fireEvent.mouseEnter(yes);
      const hovered = yes.style.background;
      fireEvent.mouseLeave(yes);
      expect(yes.style.background).not.toBe(hovered);

      fireEvent.mouseEnter(no);
      const noHover = no.style.color;
      fireEvent.mouseLeave(no);
      expect(no.style.color).not.toBe(noHover);

      // A mousedown inside the popover stops before it reaches document-level
      // outside-click listeners.
      const popover = yes.parentElement!;
      const outside = vi.fn();
      document.addEventListener("mousedown", outside);
      fireEvent.mouseDown(popover);
      expect(outside).not.toHaveBeenCalled();
      fireEvent.mouseDown(screen.getByTestId("graph"));
      expect(outside).toHaveBeenCalledOnce();
      document.removeEventListener("mousedown", outside);

      await user.click(no);
      expect(props.onConfirmDeleteChange).toHaveBeenLastCalledWith(null);
      expect(props.onDelete).not.toHaveBeenCalled();

      await user.click(yes);
      expect(props.onConfirmDeleteChange).toHaveBeenLastCalledWith(null);
      expect(props.onDelete).toHaveBeenCalledWith("run-1");
    });
  });

  describe("approval", () => {
    const paused = makeRun({ status: "paused", paused_node_id: "gate" });

    it("is hidden for a paused run without a node", () => {
      setup({ selectedRun: makeRun({ status: "paused" }) });
      expect(screen.queryByTestId("approval-response-input")).toBeNull();
      expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
    });

    it("prefers the definition's message", () => {
      const def: WorkflowDef = { name: "deploy", description: "", nodes: [{ id: "gate", type: "approval", message: "Ship to prod?" }] };
      setup({ selectedRun: paused, selectedDef: def, nodeRuns: [nodeRun({ node_id: "gate", status: "paused", output: "from run" })] });
      expect(screen.getByText("Approval Required — gate")).toBeTruthy();
      expect(screen.getByText("Ship to prod?")).toBeTruthy();
      expect(screen.queryByText("from run")).toBeNull();
    });

    it("falls back to the paused node's output", () => {
      const def: WorkflowDef = { name: "deploy", description: "", nodes: [{ id: "other", type: "bash" }] };
      setup({
        selectedRun: paused,
        selectedDef: def,
        nodeRuns: [nodeRun({ node_id: "gate", status: "success", output: "stale" }), nodeRun({ node_id: "gate", status: "paused", output: "from run" })],
      });
      expect(screen.getByText("from run")).toBeTruthy();
      expect(screen.queryByText("stale")).toBeNull();
    });

    it("shows no message when neither source has one", () => {
      setup({ selectedRun: paused, nodeRuns: [nodeRun({ node_id: "gate", status: "paused" })] });
      // The approval box holds just its heading and the response row.
      const box = screen.getByTestId("approval-response-input").parentElement!.parentElement!;
      expect(box.children).toHaveLength(2);
      expect(box.firstElementChild!.textContent).toBe("Approval Required — gate");
    });

    it("resumes with the typed response, approved, or rejected", async () => {
      const user = userEvent.setup();
      const { props, rerender } = setup({ selectedRun: paused });
      const input = screen.getByTestId("approval-response-input");
      fireEvent.change(input, { target: { value: "ok" } });
      expect(props.onResumeResponseChange).toHaveBeenCalledWith("ok");

      fireEvent.keyDown(input, { key: "a" });
      expect(props.onResume).not.toHaveBeenCalled();
      fireEvent.keyDown(input, { key: "Enter" });
      expect(props.onResume).toHaveBeenLastCalledWith("run-1", "");

      await user.click(screen.getByTestId("approval-approve-btn"));
      expect(props.onResume).toHaveBeenLastCalledWith("run-1", "approved");

      await user.click(screen.getByTestId("approval-reject-btn"));
      expect(props.onResume).toHaveBeenLastCalledWith("run-1", "rejected");

      rerender(<WorkflowDetail {...props} resumeResponse="lgtm" />);
      await user.click(screen.getByTestId("approval-approve-btn"));
      expect(props.onResume).toHaveBeenLastCalledWith("run-1", "lgtm");
    });
  });
});
