import {
  forwardRef,
  useEffect,
  useImperativeHandle,
} from "react";
import type { Channel, WSEvent } from "../../types";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { ChannelHeaderInfo } from "../layout/ChannelHeaderInfo";
import { useWorkflowState } from "../../hooks/useWorkflowState";
import { buildHeaderBtnStyle, hoverIn, hoverOut } from "../../utils/workflowHelpers";
import { WorkflowRunRow } from "./WorkflowRunRow";
import { WorkflowDefList } from "./WorkflowDefList";
import { WorkflowDetail } from "./WorkflowDetail";
import { WorkflowStartDialog } from "./WorkflowStartDialog";

// --- types ---

export interface WorkflowsGlobalPanelHandle {
  handleWorkflowEvent: (event: WSEvent) => void;
}

interface WorkflowsGlobalPanelProps {
  channel?: Channel;
  sidebarOpen?: boolean;
  onOpenPalette?: () => void;
  onClose: () => void;
  onSelectChannel?: (channelId: string) => void;
}

// --- component ---

export const WorkflowsGlobalPanel = forwardRef<WorkflowsGlobalPanelHandle, WorkflowsGlobalPanelProps>(
  function WorkflowsGlobalPanel(
    { channel, sidebarOpen, onOpenPalette, onClose, onSelectChannel },
    ref,
  ) {
    const { colors, fontSizes } = useTheme();
    const wf = useWorkflowState({
      channelId: undefined,
      initialListWidth: 380,
      listWidthMin: 240,
      listWidthMax: 600,
    });

    const headerBtnStyle = buildHeaderBtnStyle(colors);

    // Escape to close dialog first, then panel.
    useEffect(() => {
      const onKeyDown = (e: KeyboardEvent) => {
        if (e.key === "Escape") {
          e.preventDefault();
          if (wf.showStartDialog) { wf.setShowStartDialog(false); } else { onClose(); }
        }
      };
      window.addEventListener("keydown", onKeyDown);
      return () => window.removeEventListener("keydown", onKeyDown);
    }, [onClose, wf.showStartDialog, wf.setShowStartDialog]);

    useImperativeHandle(ref, () => ({ handleWorkflowEvent: wf.handleWorkflowEvent }), [wf.handleWorkflowEvent]);

    return (
      <div
        data-testid="workflows-panel"
        style={{
          flex: 1,
          backgroundColor: colors.sidebar,
          zoom: fontSizes.panels / 12,
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          borderRadius: colors.islandRadius,
          boxShadow: colors.islandShadow,
          border: colors.islandBorder,
        }}
      >
        {/* Drag region */}
        <div
          style={{
            height: 38,
            flexShrink: 0,
            display: "flex",
            alignItems: "center",
            paddingLeft: sidebarOpen === false ? 76 : 4,
            WebkitAppRegion: "drag",
          }}
        >
          {onOpenPalette && (
            <button
              onClick={onOpenPalette}
              title="Search messages (Cmd+K)"
              style={{
                background: "none",
                border: `1px solid ${colors.border}`,
                color: colors.textDim,
                cursor: "pointer",
                padding: "2px 8px",
                lineHeight: 1,
                borderRadius: 4,
                display: "flex",
                alignItems: "center",
                gap: 4,
                fontSize: 11,
                fontFamily: fonts.mono,
                marginLeft: 6,
                WebkitAppRegion: "no-drag",
              }}
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="11" cy="11" r="8" />
                <line x1="21" y1="21" x2="16.65" y2="16.65" />
              </svg>
              <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
            </button>
          )}
          {channel && <ChannelHeaderInfo channel={channel} colors={colors} />}
          <div style={{ flex: 1 }} />
        </div>

        {/* Header */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "3px 12px",
            borderBottom: `1px solid ${colors.border}`,
            flexShrink: 0,
            boxSizing: "border-box",
            height: 35,
          }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span
              style={{
                fontSize: 10,
                fontWeight: 700,
                color: colors.textDim,
                textTransform: "uppercase",
                letterSpacing: 1,
              }}
            >
              Workflows ({wf.runs.length})
            </span>
          </div>
          <button
            onClick={onClose}
            title="Close panel"
            style={headerBtnStyle}
            onMouseEnter={(e) => hoverIn(e, colors)}
            onMouseLeave={(e) => hoverOut(e, colors)}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>

        {/* Two-pane content */}
        <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
          {/* Left: run list */}
          <div
            style={{
              width: wf.listWidth,
              minWidth: 240,
              display: "flex",
              flexDirection: "column",
              borderRight: `1px solid ${colors.border}`,
              background: colors.bg,
            }}
          >
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
                  onSelectChannel={onSelectChannel}
                  testId={`workflow-run-row-${r.id}`}
                />
              ))}
              {wf.loadingMore && (
                <div style={{ padding: 12, color: colors.textDim, fontSize: 11, textAlign: "center" }}>
                  Loading more…
                </div>
              )}
              {!wf.hasMore && !wf.selectedWorkflowName && wf.runs.length > 0 && (
                <div style={{ padding: 12, color: colors.textDim, fontSize: 11, textAlign: "center", opacity: 0.6 }}>
                  End of history
                </div>
              )}
              {wf.displayedRuns.length === 0 && (
                <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
                  {wf.selectedWorkflowName ? `No runs for ${wf.selectedWorkflowName}` : "No workflow runs"}
                </div>
              )}
            </div>
          </div>

          {/* Resizable divider */}
          <div
            onMouseDown={wf.onDividerMouseDown}
            style={{
              width: 4,
              cursor: "col-resize",
              background: "transparent",
              flexShrink: 0,
            }}
          />

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
  },
);
