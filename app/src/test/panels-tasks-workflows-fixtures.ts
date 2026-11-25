import type { WorkflowDef, WorkflowRun } from "../api/workflows";

/** A finished workflow run with every field filled; override what a test needs. */
export function makeWorkflowRun(over: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    id: "run-1",
    workflow_name: "deploy",
    channel_id: "",
    dir_path: "",
    worktree_path: "",
    status: "completed",
    inputs: "",
    paused_node_id: "",
    error_text: "",
    started_at: new Date(Date.now() - 60_000).toISOString(),
    finished_at: new Date().toISOString(),
    ...over,
  };
}

/** A workflow definition with no nodes. */
export function makeWorkflowDef(name: string, over: Partial<WorkflowDef> = {}): WorkflowDef {
  return { name, description: "", nodes: [], ...over };
}
