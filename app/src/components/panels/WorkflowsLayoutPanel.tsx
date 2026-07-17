import { useCallback, useRef } from "react";
import { useTheme } from "../../ThemeContext";
import { useEventStream } from "../../hooks/useEventStream";
import { useWorkflowState } from "../../hooks/useWorkflowState";
import { WorkflowRunRow } from "./WorkflowRunRow";
import { WorkflowDefList } from "./WorkflowDefList";
import { WorkflowDetail } from "./WorkflowDetail";
import { WorkflowStartDialog } from "./WorkflowStartDialog";

interface WorkflowsLayoutPanelProps {
  channelId: string;
}

export function WorkflowsLayoutPanel({ channelId }: WorkflowsLayoutPanelProps) {
  const { colors, fontSizes } = useTheme();
  const wf = useWorkflowState({
    channelId,
    initialListWidth: 340,
    listWidthMin: 200,
    listWidthMax: 500,
  });

  // Stabilize the event handler via a ref so useEventStream does not
  // re-subscribe the WebSocket callback on every run selection.
  const handleEventRef = useRef(wf.handleWorkflowEvent);
  handleEventRef.current = wf.handleWorkflowEvent;

  const onEvent = useCallback(
    (evt: { type: string; data?: unknown }) => {
      if (evt.type.startsWith("workflow.")) {
        handleEventRef.current(evt as { type: string; data: unknown });
      }
    },
    [],
  );

  useEventStream({ channelId, onEvent });

  return (
    <div data-testid="workflows-split-panel" style={{ display: "flex", flex: 1, height: "100%", overflow: "hidden", zoom: fontSizes.panels / 12 }}>
      {/* Left: run list */}
      <div style={{
        width: wf.listWidth, minWidth: 200, display: "flex", flexDirection: "column",
        borderRight: `1px solid ${colors.border}`, background: colors.bg,
      }}>
        <div style={{ padding: "6px 8px", borderBottom: `1px solid ${colors.border}`, display: "flex", alignItems: "center", gap: 6 }}>
          <span style={{ fontSize: 12, color: colors.textDim, flex: 1 }}>
            {wf.runs.length} run{wf.runs.length !== 1 ? "s" : ""}
          </span>
        </div>
        <WorkflowDefList
          grouped={wf.groupedDefinitions}
          selectedName={wf.selectedWorkflowName}
          onSelect={wf.setSelectedWorkflowName}
          onRun={wf.handleRunWorkflow}
          colors={colors}
        />
        <div
          style={{ flex: 1, overflowY: "auto" }}
          onScroll={(e) => {
            const el = e.currentTarget;
            if (el.scrollHeight - el.scrollTop - el.clientHeight < 200) {
              wf.loadMore();
            }
          }}
        >
          {wf.displayedRuns.map((r) => (
            <WorkflowRunRow
              key={r.id}
              run={r}
              isSelected={r.id === wf.selectedRunId}
              colors={colors}
              onClick={() => wf.selectRun(r.id)}
              testId={`workflow-run-row-${r.id}`}
            />
          ))}
          {wf.loadingMore && (
            <div style={{ padding: 12, color: colors.textDim, fontSize: 11, textAlign: "center" }}>Loading more…</div>
          )}
          {!wf.hasMore && !wf.selectedWorkflowName && wf.runs.length > 0 && (
            <div style={{ padding: 12, color: colors.textDim, fontSize: 11, textAlign: "center", opacity: 0.6 }}>End of history</div>
          )}
          {wf.displayedRuns.length === 0 && (
            <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
              {wf.selectedWorkflowName ? `No runs for ${wf.selectedWorkflowName}` : "No workflow runs"}
            </div>
          )}
        </div>
      </div>

      {/* Resizable divider */}
      <div onMouseDown={wf.onDividerMouseDown} style={{ width: 4, cursor: "col-resize", background: "transparent", flexShrink: 0 }} />

      {/* Right: detail view */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
        <WorkflowDetail
          selectedRun={wf.selectedRun}
          selectedDef={wf.selectedDef}
          nodeRuns={wf.nodeRuns}
          expandedNodeId={wf.expandedNodeId}
          resumeResponse={wf.resumeResponse}
          confirmingDeleteId={wf.confirmingDeleteId}
          colors={colors}
          onToggleNodeExpand={wf.toggleNodeExpand}
          onCancel={wf.handleCancel}
          onDelete={wf.handleDelete}
          onRetry={wf.handleRetry}
          onResume={wf.handleResume}
          onResumeResponseChange={wf.setResumeResponse}
          onConfirmDeleteChange={wf.setConfirmingDeleteId}
        />
      </div>

      <WorkflowStartDialog
        show={wf.showStartDialog}
        startInputs={wf.startInputs}
        selectedStartDef={wf.selectedStartDef}
        colors={colors}
        onClose={() => wf.setShowStartDialog(false)}
        onInputChange={wf.setStartInputs}
        onStart={wf.handleStartRun}
        onSave={wf.handleSaveWorkflowDef}
        testId="start-workflow-dialog"
      />
    </div>
  );
}
