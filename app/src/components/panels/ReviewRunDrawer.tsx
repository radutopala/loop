import { useCallback, useEffect, useRef, useState } from "react";
import { fetchWorkflowRun, type WorkflowNodeDef, type WorkflowNodeRun } from "../../api/workflows";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { WSEvent } from "../../types";
import { WorkflowGraph } from "./WorkflowGraph";

interface ReviewRunDrawerProps {
  /** The review-loop workflow run whose canvas to render. */
  runId: string;
  colors: ColorPalette;
  /** Same chat/WS event subscription the ReviewPanel uses. Node/run events
   *  for `runId` trigger a detail refetch so the canvas stays live. */
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
  collapsed: boolean;
  onToggleCollapsed: () => void;
}

// Height of the canvas area when the drawer is expanded. Tall enough to show
// the review loop's body iterations without swallowing the diff above it.
const DRAWER_BODY_HEIGHT = 300;

// parseDefs pulls the node list out of a run's stored workflow_def JSON. Older
// runs may not carry one — WorkflowGraph then falls back to synthesizing nodes
// from the run rows, so an empty list is a safe default.
function parseDefs(workflowDef: string | undefined): WorkflowNodeDef[] {
  if (!workflowDef) return [];
  try {
    const parsed = JSON.parse(workflowDef) as { nodes?: WorkflowNodeDef[] };
    return parsed.nodes ?? [];
  } catch {
    return [];
  }
}

// ReviewRunDrawer shows the live workflow canvas for a single review-loop run
// as a collapsible drawer pinned to the bottom of the Review panel, so the
// user can watch the loop's nodes progress without opening the Workflows panel.
export function ReviewRunDrawer({ runId, colors, subscribeChatEvents, collapsed, onToggleCollapsed }: ReviewRunDrawerProps) {
  const [defs, setDefs] = useState<WorkflowNodeDef[]>([]);
  const [nodeRuns, setNodeRuns] = useState<WorkflowNodeRun[]>([]);
  const [expandedNodeId, setExpandedNodeId] = useState<string | null>(null);
  const [workflowName, setWorkflowName] = useState<string>("");
  const [runActive, setRunActive] = useState(false);

  // Keep `collapsed` in a ref so the fetch effect doesn't re-subscribe / refetch
  // when the user merely toggles the drawer open/closed.
  const collapsedRef = useRef(collapsed);
  collapsedRef.current = collapsed;

  const refetch = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const detail = await fetchWorkflowRun(runId, signal ? { signal } : undefined);
        setDefs(parseDefs(detail.run?.workflow_def));
        setNodeRuns(detail.node_runs ?? []);
        setWorkflowName(detail.run?.workflow_name ?? "");
        const status = detail.run?.status;
        setRunActive(status === "running" || status === "paused");
      } catch {
        /* transient — leave the last-known canvas in place */
      }
    },
    [runId],
  );

  // Initial + on-runId-change fetch. Reset the expanded node so a new run
  // doesn't inherit the previous run's open node.
  useEffect(() => {
    setExpandedNodeId(null);
    const ctl = new AbortController();
    void refetch(ctl.signal);
    return () => ctl.abort();
  }, [runId, refetch]);

  // Refetch on workflow events for this run so the canvas mirrors node/run
  // progress in real time. Skip work while collapsed — the next expand
  // triggers a fresh fetch via the effect below.
  useEffect(() => {
    if (!subscribeChatEvents) return;
    const listener: ChatEventListener = (event: WSEvent) => {
      const d = event.data as { run_id?: string } | undefined;
      if (!d || d.run_id !== runId) return;
      switch (event.type) {
        case "workflow.run_started":
        case "workflow.run_completed":
        case "workflow.run_paused":
        case "workflow.node_started":
        case "workflow.node_completed":
          if (!collapsedRef.current) void refetch();
          break;
        default:
      }
    };
    return subscribeChatEvents(listener);
  }, [subscribeChatEvents, runId, refetch]);

  // When re-expanded, pull the latest state (events were ignored while
  // collapsed) so the canvas isn't stale.
  useEffect(() => {
    if (collapsed) return;
    void refetch();
  }, [collapsed, refetch]);

  // Poll while the run is active and the drawer is open, so the canvas keeps
  // advancing even if a WS event is dropped or subscribeChatEvents is absent.
  // Mirrors the Workflows panel's 2s cadence.
  useEffect(() => {
    if (collapsed || !runActive) return;
    const id = setInterval(() => void refetch(), 2_000);
    return () => clearInterval(id);
  }, [collapsed, runActive, refetch]);

  const onNodeClick = useCallback((nodeId: string) => {
    setExpandedNodeId((prev) => (prev === nodeId ? null : nodeId));
  }, []);

  return (
    <div
      data-testid="review-run-drawer"
      style={{
        display: "flex",
        flexDirection: "column",
        borderTop: `1px solid ${colors.border}`,
        background: colors.bg,
        flexShrink: 0,
      }}
    >
      {/* Drawer header — click to collapse/expand */}
      <button
        data-testid="review-run-drawer-toggle"
        onClick={onToggleCollapsed}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          width: "100%",
          textAlign: "left",
          background: "transparent",
          color: colors.textDim,
          border: "none",
          borderBottom: collapsed ? "none" : `1px solid ${colors.border}`,
          padding: "4px 10px",
          cursor: "pointer",
          fontFamily: fonts.sans,
          fontSize: 11,
        }}
        title={collapsed ? "Show workflow canvas" : "Hide workflow canvas"}
      >
        <span style={{ fontSize: 9, width: 8, display: "inline-block" }}>{collapsed ? "▶" : "▼"}</span>
        <span style={{ fontWeight: 600 }}>Workflow canvas</span>
        {workflowName && <span style={{ fontFamily: fonts.mono, color: colors.textDim, opacity: 0.8 }}>{workflowName}</span>}
      </button>
      {!collapsed && (
        <div style={{ height: DRAWER_BODY_HEIGHT, display: "flex", flexDirection: "column" }}>
          <WorkflowGraph defs={defs} nodeRuns={nodeRuns} colors={colors} onNodeClick={onNodeClick} expandedNodeId={expandedNodeId} />
        </div>
      )}
    </div>
  );
}
