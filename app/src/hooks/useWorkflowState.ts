import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { WSEvent } from "../types";
import {
  fetchWorkflows,
  fetchWorkflowRuns,
  fetchWorkflowRun,
  startWorkflowRun,
  resumeWorkflowRun,
  cancelWorkflowRun,
  deleteWorkflowRun,
  retryWorkflowRun,
} from "../api/loopApi";
import type {
  WorkflowDef,
  WorkflowRun,
  WorkflowNodeRun,
} from "../api/loopApi";

export interface UseWorkflowStateOptions {
  channelId?: string;
  initialListWidth: number;
  listWidthMin: number;
  listWidthMax: number;
  listLimit?: number;
}

export function useWorkflowState({
  channelId,
  initialListWidth,
  listWidthMin,
  listWidthMax,
  listLimit = 50,
}: UseWorkflowStateOptions) {
  const [definitions, setDefinitions] = useState<WorkflowDef[]>([]);
  const [definitionsLoaded, setDefinitionsLoaded] = useState(false);
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [nodeRuns, setNodeRuns] = useState<WorkflowNodeRun[]>([]);
  const [expandedNodeId, setExpandedNodeId] = useState<string | null>(null);
  const [listWidth, setListWidth] = useState(initialListWidth);
  const [hasMore, setHasMore] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const draggingRef = useRef(false);

  // Ref for selectedRunId so polling/WS callbacks always read latest value
  // without being in useCallback dependency arrays.
  const selectedRunIdRef = useRef(selectedRunId);
  selectedRunIdRef.current = selectedRunId;

  // Ref for runs so loadMore reads current length without re-triggering on every append.
  const runsRef = useRef(runs);
  runsRef.current = runs;
  const loadingMoreRef = useRef(false);
  loadingMoreRef.current = loadingMore;

  // Start workflow dialog state.
  const [showStartDialog, setShowStartDialog] = useState(false);
  const [startWorkflowName, setStartWorkflowName] = useState("");
  const [startInputs, setStartInputs] = useState<Record<string, string>>({});

  // Resume dialog state.
  const [resumeResponse, setResumeResponse] = useState("");

  // Delete confirmation popover state.
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);

  // Auto-select the first workflow when the start dialog is open but no
  // selection exists yet — covers the race where the user (or BDD) clicks
  // Start before workflow definitions finish loading after the dialog opens.
  useEffect(() => {
    if (!showStartDialog || startWorkflowName) return;
    const first = definitions[0];
    if (!first) return;
    setStartWorkflowName(first.name);
    const initial: Record<string, string> = {};
    if (first.inputs) {
      for (const [k, v] of Object.entries(first.inputs)) {
        initial[k] = v.default ?? "";
      }
    }
    setStartInputs(initial);
  }, [showStartDialog, startWorkflowName, definitions]);

  // Dismiss delete confirmation popover on outside click.
  useEffect(() => {
    if (!confirmingDeleteId) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest("[data-confirm-popover]")) {
        setConfirmingDeleteId(null);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [confirmingDeleteId]);

  // --- data loading ---

  const loadRuns = useCallback(async () => {
    try {
      // Refresh the full currently-loaded window so already-paginated rows stay
      // visible across polling/WS updates, without re-fetching page-by-page.
      const fetchLimit = Math.max(listLimit, runsRef.current.length);
      const data = await fetchWorkflowRuns(channelId, fetchLimit, 0);
      setRuns(data);
      setHasMore(data.length >= fetchLimit);
    } catch { /* ignore */ }
  }, [channelId, listLimit]);

  const loadMore = useCallback(async () => {
    if (loadingMoreRef.current || !hasMore) return;
    setLoadingMore(true);
    try {
      const offset = runsRef.current.length;
      const data = await fetchWorkflowRuns(channelId, listLimit, offset);
      if (data.length === 0) {
        setHasMore(false);
      } else {
        setRuns((prev) => {
          const existing = new Set(prev.map((r) => r.id));
          const merged = data.filter((r) => !existing.has(r.id));
          return [...prev, ...merged];
        });
        setHasMore(data.length === listLimit);
      }
    } catch { /* ignore */
    } finally {
      setLoadingMore(false);
    }
  }, [channelId, listLimit, hasMore]);

  const loadDefinitions = useCallback(async () => {
    try {
      const data = await fetchWorkflows(channelId);
      setDefinitions(data);
      // Only flip `definitionsLoaded=true` on a successful fetch. A transient
      // initial-fetch failure would otherwise leave the panel in a
      // `loaded && empty` state where the "+ Run" button is hidden but never
      // re-attempted; keeping definitionsLoaded=false lets a subsequent
      // remount / channel change retry instead of silently failing.
      setDefinitionsLoaded(true);
    } catch { /* ignore */ }
  }, [channelId]);

  const loadRunDetail = useCallback(async (runId: string) => {
    try {
      const detail = await fetchWorkflowRun(runId);
      setNodeRuns(detail.node_runs ?? []);
    } catch { /* ignore */ }
  }, []);

  useEffect(() => {
    loadRuns();
    loadDefinitions();
  }, [loadRuns, loadDefinitions]);

  useEffect(() => {
    if (selectedRunId) loadRunDetail(selectedRunId);
  }, [selectedRunId, loadRunDetail]);

  // Poll for updates only when any run is active (running/paused).
  // Uses selectedRunIdRef to avoid restarting the interval on every selection.
  const hasActiveRuns = runs.some((r) => r.status === "running" || r.status === "paused");
  useEffect(() => {
    if (!hasActiveRuns) return;
    const id = setInterval(() => {
      loadRuns();
      const runId = selectedRunIdRef.current;
      if (runId) loadRunDetail(runId);
    }, 2_000);
    return () => clearInterval(id);
  }, [hasActiveRuns, loadRuns, loadRunDetail]);

  // --- WebSocket event handler ---

  const handleWorkflowEvent = useCallback((event: WSEvent | { type: string; data?: unknown }) => {
    const data = event.data as Record<string, string> | undefined;
    if (!data) return;

    if (event.type === "workflow.run_started" || event.type === "workflow.run_completed" || event.type === "workflow.run_paused") {
      loadRuns();
      if (data.run_id && data.run_id === selectedRunIdRef.current) {
        loadRunDetail(data.run_id);
      }
    }
    if (event.type === "workflow.node_started" || event.type === "workflow.node_completed") {
      if (data.run_id && data.run_id === selectedRunIdRef.current) {
        loadRunDetail(data.run_id);
      }
    }
  }, [loadRuns, loadRunDetail]);

  // --- actions ---

  const handleCancel = useCallback(async (runId: string) => {
    try {
      await cancelWorkflowRun(runId);
      loadRuns();
      if (runId === selectedRunIdRef.current) loadRunDetail(runId);
    } catch (err) { console.error("workflow cancel failed:", err); }
  }, [loadRuns, loadRunDetail]);

  const handleDelete = useCallback(async (runId: string) => {
    try {
      await deleteWorkflowRun(runId);
      if (runId === selectedRunIdRef.current) setSelectedRunId(null);
      loadRuns();
    } catch (err) { console.error("workflow delete failed:", err); }
  }, [loadRuns]);

  const handleRetry = useCallback(async (runId: string) => {
    try {
      const { run_id } = await retryWorkflowRun(runId);
      loadRuns();
      setSelectedRunId(run_id);
      loadRunDetail(run_id);
      // Follow-up fetches to catch fast-completing retried runs.
      setTimeout(() => { loadRuns(); if (run_id) loadRunDetail(run_id); }, 500);
      setTimeout(() => { loadRuns(); if (run_id) loadRunDetail(run_id); }, 2000);
    } catch (err) { console.error("workflow retry failed:", err); }
  }, [loadRuns, loadRunDetail]);

  const handleResume = useCallback(async (runId: string, response: string) => {
    try {
      await resumeWorkflowRun(runId, response || "approved");
      setResumeResponse("");
      loadRuns();
      if (runId === selectedRunIdRef.current) loadRunDetail(runId);
    } catch (err) { console.error("workflow resume failed:", err); }
  }, [loadRuns, loadRunDetail]);

  const handleStartRun = useCallback(async () => {
    // Fall back to the first definition when no workflow has been explicitly
    // selected — the native <select> renders the first <option> visually even
    // when React state is empty (initial mount race), so respect that visual default.
    const effectiveName = startWorkflowName || definitions[0]?.name || "";
    if (!effectiveName) return;
    // Close the dialog immediately so a failed start doesn't strand the user
    // (and BDD waits) on an open modal. Success-path side effects still fire below.
    setShowStartDialog(false);
    setStartWorkflowName("");
    setStartInputs({});
    try {
      const result = await startWorkflowRun({
        workflow_name: effectiveName,
        channel_id: channelId,
        inputs: Object.keys(startInputs).length > 0 ? startInputs : undefined,
      });
      loadRuns();
      setSelectedRunId(result.run_id);
      // Fast-completing workflows (e.g. simple bash nodes) may finish before
      // the WebSocket subscription is established or before polling kicks in.
      // Schedule follow-up fetches to catch the completed status.
      setTimeout(() => { loadRuns(); if (result.run_id) loadRunDetail(result.run_id); }, 500);
      setTimeout(() => { loadRuns(); if (result.run_id) loadRunDetail(result.run_id); }, 2000);
    } catch (err) { console.error("workflow start failed:", err); }
  }, [channelId, startWorkflowName, startInputs, definitions, loadRuns, loadRunDetail]);

  const handleSelectWorkflow = useCallback((name: string) => {
    setStartWorkflowName(name);
    const def = definitions.find((d) => d.name === name);
    if (def?.inputs) {
      const initial: Record<string, string> = {};
      for (const [k, v] of Object.entries(def.inputs)) {
        initial[k] = v.default ?? "";
      }
      setStartInputs(initial);
    } else {
      setStartInputs({});
    }
  }, [definitions]);

  const openStartDialog = useCallback(() => {
    setShowStartDialog(true);
    const first = definitions[0];
    if (first && !startWorkflowName) {
      setStartWorkflowName(first.name);
      const initial: Record<string, string> = {};
      if (first.inputs) {
        for (const [k, v] of Object.entries(first.inputs)) {
          initial[k] = v.default ?? "";
        }
      }
      setStartInputs(initial);
    }
  }, [definitions, startWorkflowName]);

  const toggleNodeExpand = useCallback((nodeId: string) => {
    setExpandedNodeId((prev) => {
      if (prev === nodeId) return null; // collapsing — no refetch needed
      const runId = selectedRunIdRef.current;
      if (runId) loadRunDetail(runId);
      return nodeId;
    });
  }, [loadRunDetail]);

  const selectRun = useCallback((runId: string) => {
    setSelectedRunId(runId);
    setExpandedNodeId(null);
  }, []);

  // Resizable divider.
  const onDividerMouseDown = useCallback(() => {
    draggingRef.current = true;
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current) return;
      setListWidth((prev) => Math.max(listWidthMin, Math.min(listWidthMax, prev + e.movementX)));
    };
    const onUp = () => {
      draggingRef.current = false;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }, [listWidthMin, listWidthMax]);

  // --- derived state (memoized) ---

  const sortedRuns = useMemo(() =>
    [...runs].sort((a, b) => {
      const aActive = a.status === "running" || a.status === "paused" ? 0 : 1;
      const bActive = b.status === "running" || b.status === "paused" ? 0 : 1;
      if (aActive !== bActive) return aActive - bActive;
      return new Date(b.started_at).getTime() - new Date(a.started_at).getTime();
    }),
  [runs]);

  const selectedRun = runs.find((r) => r.id === selectedRunId) ?? null;

  const selectedDef = useMemo(() => {
    if (!selectedRun) return null;
    return definitions.find((d) => d.name === selectedRun.workflow_name)
      ?? (selectedRun.workflow_def
        ? (() => { try { return JSON.parse(selectedRun.workflow_def) as WorkflowDef; } catch { return null; } })()
        : null);
  }, [selectedRun, definitions]);

  const selectedStartDef = definitions.find((d) => d.name === startWorkflowName);

  return {
    // State
    definitions,
    definitionsLoaded,
    runs,
    selectedRunId,
    nodeRuns,
    expandedNodeId,
    listWidth,
    showStartDialog,
    setShowStartDialog,
    startWorkflowName,
    startInputs,
    setStartInputs,
    resumeResponse,
    setResumeResponse,
    confirmingDeleteId,
    setConfirmingDeleteId,

    // Derived
    sortedRuns,
    selectedRun,
    selectedDef,
    selectedStartDef,

    // Actions
    handleCancel,
    handleDelete,
    handleRetry,
    handleResume,
    handleStartRun,
    handleSelectWorkflow,
    openStartDialog,
    toggleNodeExpand,
    selectRun,
    onDividerMouseDown,
    handleWorkflowEvent,
    loadRuns,
    loadRunDetail,
    loadMore,
    hasMore,
    loadingMore,
  };
}
