// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkflowDef, WorkflowRun } from "../api/loopApi";
import { cancelWorkflowRun, deleteWorkflowRun, fetchWorkflowRun, fetchWorkflowRuns, fetchWorkflows, resumeWorkflowRun, retryWorkflowRun, saveWorkflowDef, startWorkflowRun } from "../api/loopApi";
import { type UseWorkflowStateOptions, useWorkflowState } from "./useWorkflowState";

vi.mock("../api/loopApi", () => ({
  cancelWorkflowRun: vi.fn(),
  deleteWorkflowRun: vi.fn(),
  fetchWorkflowRun: vi.fn(),
  fetchWorkflowRuns: vi.fn(),
  fetchWorkflows: vi.fn(),
  resumeWorkflowRun: vi.fn(),
  retryWorkflowRun: vi.fn(),
  saveWorkflowDef: vi.fn(),
  startWorkflowRun: vi.fn(),
}));

function run(id: string, over: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    id,
    workflow_name: "wf",
    channel_id: "c1",
    dir_path: "",
    worktree_path: "",
    status: "completed",
    inputs: "",
    paused_node_id: "",
    error_text: "",
    started_at: "2026-01-01T00:00:00Z",
    finished_at: null,
    ...over,
  };
}

function def(name: string, over: Partial<WorkflowDef> = {}): WorkflowDef {
  return { name, description: "", nodes: [], ...over };
}

const flush = () => act(async () => {});

const baseOpts: UseWorkflowStateOptions = { channelId: "c1", initialListWidth: 300, listWidthMin: 200, listWidthMax: 500 };

async function setup(opts: Partial<UseWorkflowStateOptions> = {}) {
  const hook = renderHook(() => useWorkflowState({ ...baseOpts, ...opts }));
  await flush();
  return hook;
}

describe("useWorkflowState", () => {
  beforeEach(() => {
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([]);
    vi.mocked(fetchWorkflows).mockResolvedValue([]);
    vi.mocked(fetchWorkflowRun).mockResolvedValue({ node_runs: [] } as never);
    vi.mocked(cancelWorkflowRun).mockResolvedValue();
    vi.mocked(deleteWorkflowRun).mockResolvedValue();
    vi.mocked(resumeWorkflowRun).mockResolvedValue();
    vi.mocked(saveWorkflowDef).mockResolvedValue();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    vi.resetAllMocks();
  });

  it("loads runs and definitions on mount", async () => {
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([run("r1")]);
    vi.mocked(fetchWorkflows).mockResolvedValue([def("wf")]);
    const { result } = await setup({ listLimit: 1 });
    expect(fetchWorkflowRuns).toHaveBeenCalledWith("c1", 1, 0);
    expect(fetchWorkflows).toHaveBeenCalledWith("c1");
    expect(result.current.runs.map((r) => r.id)).toEqual(["r1"]);
    expect(result.current.definitions).toEqual([def("wf")]);
    expect(result.current.definitionsLoaded).toBe(true);
    // A full page means there may be more.
    expect(result.current.hasMore).toBe(true);
    expect(result.current.listWidth).toBe(300);
  });

  it("ignores load failures and leaves definitions unloaded", async () => {
    vi.mocked(fetchWorkflowRuns).mockRejectedValue(new Error("down"));
    vi.mocked(fetchWorkflows).mockRejectedValue(new Error("down"));
    vi.mocked(fetchWorkflowRun).mockRejectedValue(new Error("down"));
    const { result } = await setup();
    expect(result.current.runs).toEqual([]);
    expect(result.current.definitionsLoaded).toBe(false);
    await act(() => result.current.loadRunDetail("r1"));
    expect(result.current.nodeRuns).toEqual([]);
  });

  it("loads a selected run's node runs, defaulting to none", async () => {
    vi.mocked(fetchWorkflowRun).mockResolvedValueOnce({ node_runs: [{ node_id: "n1" }] } as never);
    const { result } = await setup();
    act(() => result.current.selectRun("r1"));
    await flush();
    expect(fetchWorkflowRun).toHaveBeenCalledWith("r1");
    expect(result.current.nodeRuns).toEqual([{ node_id: "n1" }]);
    expect(result.current.selectedRunId).toBe("r1");

    vi.mocked(fetchWorkflowRun).mockResolvedValueOnce({} as never);
    await act(() => result.current.loadRunDetail("r1"));
    expect(result.current.nodeRuns).toEqual([]);
  });

  describe("loadMore", () => {
    it("appends the next page without duplicates", async () => {
      vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce([run("a"), run("b")]);
      const { result } = await setup({ listLimit: 2 });
      vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce([run("b"), run("c")]);
      await act(() => result.current.loadMore());
      expect(fetchWorkflowRuns).toHaveBeenLastCalledWith("c1", 2, 2);
      expect(result.current.runs.map((r) => r.id)).toEqual(["a", "b", "c"]);
      expect(result.current.hasMore).toBe(true);
      expect(result.current.loadingMore).toBe(false);

      // A short page ends the list.
      vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce([run("d")]);
      await act(() => result.current.loadMore());
      expect(result.current.hasMore).toBe(false);
      // With nothing more, it does not fetch.
      const calls = vi.mocked(fetchWorkflowRuns).mock.calls.length;
      await act(() => result.current.loadMore());
      expect(fetchWorkflowRuns).toHaveBeenCalledTimes(calls);
    });

    it("stops at an empty page and ignores errors", async () => {
      vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce([run("a")]);
      const { result } = await setup({ listLimit: 1 });
      vi.mocked(fetchWorkflowRuns).mockRejectedValueOnce(new Error("x"));
      await act(() => result.current.loadMore());
      expect(result.current.hasMore).toBe(true);
      expect(result.current.loadingMore).toBe(false);
      vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce([]);
      await act(() => result.current.loadMore());
      expect(result.current.hasMore).toBe(false);
      expect(result.current.runs.map((r) => r.id)).toEqual(["a"]);
    });

    it("skips while a page is already loading", async () => {
      vi.mocked(fetchWorkflowRuns).mockResolvedValueOnce([run("a")]);
      const { result } = await setup({ listLimit: 1 });
      let release: (v: WorkflowRun[]) => void = () => {};
      vi.mocked(fetchWorkflowRuns).mockReturnValueOnce(new Promise((r) => (release = r)));
      let first: Promise<void> = Promise.resolve();
      act(() => {
        first = result.current.loadMore();
      });
      expect(result.current.loadingMore).toBe(true);
      await act(() => result.current.loadMore());
      expect(fetchWorkflowRuns).toHaveBeenCalledTimes(2);
      await act(async () => {
        release([run("b")]);
        await first;
      });
      expect(result.current.runs.map((r) => r.id)).toEqual(["a", "b"]);
    });
  });

  it("polls every 2s while a run is active, refreshing the selected run", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([run("r1", { status: "running" })]);
    const { result, unmount } = await setup();
    const before = vi.mocked(fetchWorkflowRuns).mock.calls.length;
    await act(async () => vi.advanceTimersByTime(2000));
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(before + 1);
    expect(fetchWorkflowRun).not.toHaveBeenCalled();

    act(() => result.current.selectRun("r1"));
    await flush();
    vi.mocked(fetchWorkflowRun).mockClear();
    await act(async () => vi.advanceTimersByTime(2000));
    expect(fetchWorkflowRun).toHaveBeenCalledWith("r1");
    unmount();
  });

  it("does not poll when no run is active", async () => {
    vi.useFakeTimers();
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([run("r1", { status: "failed" })]);
    await setup();
    const before = vi.mocked(fetchWorkflowRuns).mock.calls.length;
    await act(async () => vi.advanceTimersByTime(6000));
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(before);
  });

  it("reacts to workflow WebSocket events", async () => {
    const { result } = await setup();
    act(() => result.current.selectRun("sel"));
    await flush();
    vi.mocked(fetchWorkflowRuns).mockClear();
    vi.mocked(fetchWorkflowRun).mockClear();

    act(() => result.current.handleWorkflowEvent({ type: "workflow.run_started" }));
    expect(fetchWorkflowRuns).not.toHaveBeenCalled();

    for (const type of ["workflow.run_started", "workflow.run_completed", "workflow.run_paused"]) {
      act(() => result.current.handleWorkflowEvent({ type, data: { run_id: "other" } }));
    }
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(3);
    expect(fetchWorkflowRun).not.toHaveBeenCalled();
    act(() => result.current.handleWorkflowEvent({ type: "workflow.run_completed", data: {} }));
    act(() => result.current.handleWorkflowEvent({ type: "workflow.run_completed", data: { run_id: "sel" } }));
    expect(fetchWorkflowRun).toHaveBeenCalledTimes(1);

    act(() => result.current.handleWorkflowEvent({ type: "workflow.node_started", data: { run_id: "other" } }));
    act(() => result.current.handleWorkflowEvent({ type: "workflow.node_started", data: {} }));
    act(() => result.current.handleWorkflowEvent({ type: "workflow.node_completed", data: { run_id: "sel" } }));
    act(() => result.current.handleWorkflowEvent({ type: "something.else", data: { run_id: "sel" } }));
    expect(fetchWorkflowRuns).toHaveBeenCalledTimes(5);
    expect(fetchWorkflowRun).toHaveBeenCalledTimes(2);
    await flush();
  });

  describe("run actions", () => {
    it("cancels, refreshing the run's detail only when it is selected", async () => {
      const { result } = await setup();
      await act(() => result.current.handleCancel("r1"));
      expect(cancelWorkflowRun).toHaveBeenCalledWith("r1");
      expect(fetchWorkflowRun).not.toHaveBeenCalled();
      act(() => result.current.selectRun("r1"));
      await flush();
      vi.mocked(fetchWorkflowRun).mockClear();
      await act(() => result.current.handleCancel("r1"));
      expect(fetchWorkflowRun).toHaveBeenCalledWith("r1");
    });

    it("deletes, clearing the selection when the selected run goes", async () => {
      const { result } = await setup();
      act(() => result.current.selectRun("r1"));
      await act(() => result.current.handleDelete("r2"));
      expect(deleteWorkflowRun).toHaveBeenCalledWith("r2");
      expect(result.current.selectedRunId).toBe("r1");
      await act(() => result.current.handleDelete("r1"));
      expect(result.current.selectedRunId).toBeNull();
    });

    it("resumes with the response or 'approved'", async () => {
      const { result } = await setup();
      act(() => result.current.setResumeResponse("draft"));
      await act(() => result.current.handleResume("r1", ""));
      expect(resumeWorkflowRun).toHaveBeenCalledWith("r1", "approved");
      expect(result.current.resumeResponse).toBe("");
      expect(fetchWorkflowRun).not.toHaveBeenCalled();
      act(() => result.current.selectRun("r1"));
      await flush();
      vi.mocked(fetchWorkflowRun).mockClear();
      await act(() => result.current.handleResume("r1", "go"));
      expect(resumeWorkflowRun).toHaveBeenLastCalledWith("r1", "go");
      expect(fetchWorkflowRun).toHaveBeenCalledWith("r1");
    });

    it("retries, selecting the new run and re-fetching it twice more", async () => {
      vi.useFakeTimers();
      vi.mocked(retryWorkflowRun).mockResolvedValueOnce({ run_id: "r2" });
      const { result } = await setup();
      await act(() => result.current.handleRetry("r1"));
      expect(retryWorkflowRun).toHaveBeenCalledWith("r1");
      expect(result.current.selectedRunId).toBe("r2");
      vi.mocked(fetchWorkflowRun).mockClear();
      vi.mocked(fetchWorkflowRuns).mockClear();
      await act(async () => vi.advanceTimersByTime(500));
      expect(fetchWorkflowRun).toHaveBeenCalledTimes(1);
      await act(async () => vi.advanceTimersByTime(1500));
      expect(fetchWorkflowRun).toHaveBeenCalledTimes(2);
      expect(fetchWorkflowRuns).toHaveBeenCalledTimes(2);

      // An empty run id skips the follow-up detail fetches.
      vi.mocked(retryWorkflowRun).mockResolvedValueOnce({ run_id: "" });
      await act(() => result.current.handleRetry("r1"));
      vi.mocked(fetchWorkflowRun).mockClear();
      await act(async () => vi.advanceTimersByTime(2000));
      expect(fetchWorkflowRun).not.toHaveBeenCalled();
    });

    it("logs failed actions", async () => {
      const err = vi.spyOn(console, "error").mockImplementation(() => {});
      const boom = new Error("boom");
      vi.mocked(cancelWorkflowRun).mockRejectedValue(boom);
      vi.mocked(deleteWorkflowRun).mockRejectedValue(boom);
      vi.mocked(retryWorkflowRun).mockRejectedValue(boom);
      vi.mocked(resumeWorkflowRun).mockRejectedValue(boom);
      vi.mocked(startWorkflowRun).mockRejectedValue(boom);
      const { result } = await setup();
      await act(() => result.current.handleCancel("r"));
      await act(() => result.current.handleDelete("r"));
      await act(() => result.current.handleRetry("r"));
      await act(() => result.current.handleResume("r", "x"));
      act(() => result.current.handleSelectWorkflow("wf"));
      await act(() => result.current.handleStartRun());
      expect(err.mock.calls).toEqual([
        ["workflow cancel failed:", boom],
        ["workflow delete failed:", boom],
        ["workflow retry failed:", boom],
        ["workflow resume failed:", boom],
        ["workflow start failed:", boom],
      ]);
    });
  });

  describe("starting a workflow", () => {
    it("does nothing without a chosen workflow", async () => {
      const { result } = await setup();
      await act(() => result.current.handleStartRun());
      expect(startWorkflowRun).not.toHaveBeenCalled();
    });

    it("prefills inputs from the definition's defaults and opens the dialog", async () => {
      vi.mocked(fetchWorkflows).mockResolvedValue([def("withInputs", { inputs: { a: { default: "1" }, b: {} } }), def("plain")]);
      const { result } = await setup();
      act(() => result.current.handleRunWorkflow("withInputs"));
      expect(result.current.showStartDialog).toBe(true);
      expect(result.current.startWorkflowName).toBe("withInputs");
      expect(result.current.startInputs).toEqual({ a: "1", b: "" });
      expect(result.current.selectedStartDef?.name).toBe("withInputs");

      act(() => result.current.handleSelectWorkflow("plain"));
      expect(result.current.startInputs).toEqual({});
      act(() => result.current.handleSelectWorkflow("missing"));
      expect(result.current.startInputs).toEqual({});
      expect(result.current.selectedStartDef).toBeUndefined();
    });

    it("starts with inputs, resets the dialog and follows the run", async () => {
      vi.useFakeTimers();
      vi.mocked(fetchWorkflows).mockResolvedValue([def("wf", { inputs: { a: { default: "1" } } })]);
      vi.mocked(startWorkflowRun).mockResolvedValue({ run_id: "new" });
      const { result } = await setup();
      act(() => result.current.handleRunWorkflow("wf"));
      await act(() => result.current.handleStartRun());
      expect(startWorkflowRun).toHaveBeenCalledWith({ workflow_name: "wf", channel_id: "c1", inputs: { a: "1" } });
      expect(result.current.showStartDialog).toBe(false);
      expect(result.current.startWorkflowName).toBe("");
      expect(result.current.startInputs).toEqual({});
      expect(result.current.selectedRunId).toBe("new");
      vi.mocked(fetchWorkflowRun).mockClear();
      await act(async () => vi.advanceTimersByTime(2000));
      expect(vi.mocked(fetchWorkflowRun).mock.calls).toEqual([["new"], ["new"]]);
    });

    it("sends no inputs when there are none and skips detail fetches without a run id", async () => {
      vi.useFakeTimers();
      vi.mocked(startWorkflowRun).mockResolvedValue({ run_id: "" });
      const { result } = await setup({ channelId: undefined });
      act(() => result.current.handleSelectWorkflow("wf"));
      await act(() => result.current.handleStartRun());
      expect(startWorkflowRun).toHaveBeenCalledWith({ workflow_name: "wf", channel_id: undefined, inputs: undefined });
      await act(async () => vi.advanceTimersByTime(2000));
      expect(fetchWorkflowRun).not.toHaveBeenCalled();
    });
  });

  describe("saving an edited definition", () => {
    it("rejects bad JSON and a missing name", async () => {
      const { result } = await setup();
      expect(await result.current.handleSaveWorkflowDef("{")).toMatch(/^Invalid JSON: /);
      for (const text of ["null", "{}", '{"name": 3}', '{"name": ""}']) {
        expect(await result.current.handleSaveWorkflowDef(text)).toBe('Definition must have a non-empty "name".');
      }
      expect(saveWorkflowDef).not.toHaveBeenCalled();
    });

    it("saves into the definition's scope without the scope tag, then reloads", async () => {
      vi.mocked(fetchWorkflows).mockResolvedValue([def("p", { scope: "project" })]);
      const { result } = await setup();
      vi.mocked(fetchWorkflows).mockClear();
      let out: string | null = "unset";
      await act(async () => {
        out = await result.current.handleSaveWorkflowDef(JSON.stringify({ name: "p", scope: "project", description: "d" }));
      });
      expect(out).toBeNull();
      expect(saveWorkflowDef).toHaveBeenCalledWith({ action: "update", scope: "project", channel_id: "c1", workflow: { name: "p", description: "d" } });
      expect(fetchWorkflows).toHaveBeenCalledTimes(1);

      await act(async () => {
        out = await result.current.handleSaveWorkflowDef('{"name": "new"}');
      });
      expect(saveWorkflowDef).toHaveBeenLastCalledWith({ action: "update", scope: "global", channel_id: "c1", workflow: { name: "new" } });
    });

    it("returns the save error's message", async () => {
      const { result } = await setup();
      vi.mocked(saveWorkflowDef).mockRejectedValueOnce(new Error("nope"));
      expect(await result.current.handleSaveWorkflowDef('{"name": "x"}')).toBe("nope");
      vi.mocked(saveWorkflowDef).mockRejectedValueOnce("plain");
      expect(await result.current.handleSaveWorkflowDef('{"name": "x"}')).toBe("plain");
    });
  });

  it("expands a node, refetching the selected run, and collapses it again", async () => {
    const { result } = await setup();
    act(() => result.current.toggleNodeExpand("n1"));
    expect(result.current.expandedNodeId).toBe("n1");
    expect(fetchWorkflowRun).not.toHaveBeenCalled();

    act(() => result.current.selectRun("r1"));
    expect(result.current.expandedNodeId).toBeNull();
    await flush();
    vi.mocked(fetchWorkflowRun).mockClear();
    act(() => result.current.toggleNodeExpand("n2"));
    expect(result.current.expandedNodeId).toBe("n2");
    expect(fetchWorkflowRun).toHaveBeenCalledWith("r1");
    act(() => result.current.toggleNodeExpand("n2"));
    expect(result.current.expandedNodeId).toBeNull();
    await flush();
  });

  it("resizes the list while dragging the divider, within bounds", async () => {
    const { result } = await setup();
    const move = (movementX: number) => act(() => window.dispatchEvent(new MouseEvent("mousemove", { movementX })));
    act(() => result.current.onDividerMouseDown());
    move(50);
    expect(result.current.listWidth).toBe(350);
    move(1000);
    expect(result.current.listWidth).toBe(500);
    move(-1000);
    expect(result.current.listWidth).toBe(200);
    act(() => window.dispatchEvent(new MouseEvent("mouseup")));
    move(100);
    expect(result.current.listWidth).toBe(200);
  });

  it("closes the delete confirmation on a click outside it", async () => {
    const { result } = await setup();
    const inside = document.createElement("div");
    inside.setAttribute("data-confirm-popover", "");
    const child = document.createElement("span");
    inside.appendChild(child);
    document.body.appendChild(inside);

    // With nothing to confirm, clicks are not watched.
    act(() => document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    act(() => result.current.setConfirmingDeleteId("r1"));
    act(() => child.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    expect(result.current.confirmingDeleteId).toBe("r1");
    act(() => document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    expect(result.current.confirmingDeleteId).toBeNull();
    inside.remove();
  });

  it("sorts active runs first then newest, filters by workflow and groups definitions", async () => {
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([
      run("old", { started_at: "2026-01-01T00:00:00Z", workflow_name: "a" }),
      run("new", { started_at: "2026-02-01T00:00:00Z", workflow_name: "b" }),
      run("paused", { status: "paused", started_at: "2025-01-01T00:00:00Z", workflow_name: "a" }),
      run("running", { status: "running", started_at: "2025-06-01T00:00:00Z", workflow_name: "b" }),
    ]);
    vi.mocked(fetchWorkflows).mockResolvedValue([def("g1"), def("g2", { scope: "global" }), def("p1", { scope: "project" })]);
    const { result } = await setup();
    expect(result.current.sortedRuns.map((r) => r.id)).toEqual(["running", "paused", "new", "old"]);
    expect(result.current.displayedRuns).toBe(result.current.sortedRuns);
    act(() => result.current.setSelectedWorkflowName("a"));
    expect(result.current.displayedRuns.map((r) => r.id)).toEqual(["paused", "old"]);
    expect(result.current.groupedDefinitions.global.map((d) => d.name)).toEqual(["g1", "g2"]);
    expect(result.current.groupedDefinitions.project.map((d) => d.name)).toEqual(["p1"]);
  });

  it("resolves the selected run's definition from the list or its snapshot", async () => {
    const snapshot = def("gone", { description: "snap" });
    vi.mocked(fetchWorkflowRuns).mockResolvedValue([
      run("listed", { workflow_name: "known" }),
      run("snap", { workflow_name: "gone", workflow_def: JSON.stringify(snapshot) }),
      run("broken", { workflow_name: "gone", workflow_def: "{" }),
      run("bare", { workflow_name: "gone" }),
    ]);
    vi.mocked(fetchWorkflows).mockResolvedValue([def("known")]);
    const { result } = await setup();
    expect(result.current.selectedRun).toBeNull();
    expect(result.current.selectedDef).toBeNull();

    act(() => result.current.selectRun("listed"));
    expect(result.current.selectedRun?.id).toBe("listed");
    expect(result.current.selectedDef).toEqual(def("known"));
    act(() => result.current.selectRun("snap"));
    expect(result.current.selectedDef).toEqual(snapshot);
    act(() => result.current.selectRun("broken"));
    expect(result.current.selectedDef).toBeNull();
    act(() => result.current.selectRun("bare"));
    expect(result.current.selectedDef).toBeNull();
    await flush();
  });
});
