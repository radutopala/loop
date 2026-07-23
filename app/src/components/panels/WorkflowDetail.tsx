import type { WorkflowDef, WorkflowNodeRun, WorkflowRun } from "../../api/loopApi";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import { buildBtnSecondaryStyle, buildBtnStyle, buildInputStyle, elapsed, STATUS_COLORS, timeAgo } from "../../utils/workflowHelpers";
import { WorkflowGraph } from "./WorkflowGraph";

interface WorkflowDetailProps {
  selectedRun: WorkflowRun | null;
  selectedDef: WorkflowDef | null;
  nodeRuns: WorkflowNodeRun[];
  expandedNodeId: string | null;
  resumeResponse: string;
  confirmingDeleteId: string | null;
  colors: ColorPalette;
  onToggleNodeExpand: (nodeId: string) => void;
  onCancel: (runId: string) => void;
  onDelete: (runId: string) => void;
  onRetry: (runId: string) => void;
  onResume: (runId: string, response: string) => void;
  onResumeResponseChange: (value: string) => void;
  onConfirmDeleteChange: (id: string | null) => void;
}

export function WorkflowDetail({
  selectedRun,
  selectedDef,
  nodeRuns,
  expandedNodeId,
  resumeResponse,
  confirmingDeleteId,
  colors,
  onToggleNodeExpand,
  onCancel,
  onDelete,
  onRetry,
  onResume,
  onResumeResponseChange,
  onConfirmDeleteChange,
}: WorkflowDetailProps) {
  if (!selectedRun) {
    return <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: 13 }}>Select a workflow run to view details</div>;
  }

  const btnSecondaryStyle = buildBtnSecondaryStyle(colors);
  const btnStyle = buildBtnStyle(colors);
  const inputStyle = buildInputStyle(colors);

  return (
    <div style={{ flex: 1, overflowY: "auto", display: "flex", flexDirection: "column" }}>
      {/* Run header */}
      <div style={{ padding: 12, borderBottom: `1px solid ${colors.border}`, display: "flex", flexDirection: "column", gap: 6 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: colors.text }}>{selectedRun.workflow_name}</span>
          <span
            style={{
              padding: "1px 5px",
              borderRadius: 3,
              fontSize: 10,
              fontWeight: 600,
              color: "#fff",
              background: STATUS_COLORS[selectedRun.status] ?? colors.textDim,
            }}
          >
            {selectedRun.status.toUpperCase()}
          </span>
          <span style={{ color: colors.textDim, fontSize: 11, fontFamily: fonts.mono }}>{selectedRun.id}</span>
          <div style={{ flex: 1 }} />
          {(selectedRun.status === "failed" || selectedRun.status === "completed" || selectedRun.status === "cancelled") && (
            <button onClick={() => onRetry(selectedRun.id)} style={btnSecondaryStyle}>
              Retry
            </button>
          )}
          {(selectedRun.status === "running" || selectedRun.status === "paused") && (
            <button onClick={() => onCancel(selectedRun.id)} style={{ ...btnSecondaryStyle, color: colors.error, borderColor: colors.error }}>
              Cancel
            </button>
          )}
          <div style={{ position: "relative" }}>
            <button onClick={() => onConfirmDeleteChange(selectedRun.id)} style={{ ...btnSecondaryStyle, color: colors.error, borderColor: colors.error }}>
              Delete
            </button>
            {confirmingDeleteId === selectedRun.id && (
              <div
                data-confirm-popover
                onMouseDown={(e) => e.stopPropagation()}
                style={{
                  position: "absolute",
                  top: "100%",
                  right: 0,
                  marginTop: 2,
                  backgroundColor: colors.surface,
                  border: `1px solid ${colors.textLight}`,
                  borderRadius: 6,
                  padding: "0 8px",
                  height: 22,
                  boxSizing: "border-box",
                  zIndex: 1000,
                  boxShadow: `0 4px 12px ${colors.shadow}`,
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                  whiteSpace: "nowrap",
                  fontFamily: fonts.sans,
                  fontSize: 9,
                }}
              >
                <svg width="16" height="9" viewBox="0 0 16 9" style={{ position: "absolute", top: -8, right: 8, filter: "drop-shadow(0 -2px 4px rgba(0,0,0,0.3))" }}>
                  <path d="M1 9 L7 2.5 Q8 1.5 9 2.5 L15 9 Z" fill={colors.surface} stroke={colors.textLight} strokeWidth="0.75" />
                  <rect x="0" y="8" width="16" height="2" fill={colors.surface} />
                </svg>
                <span style={{ color: colors.textLight }}>Delete?</span>
                <button
                  onClick={() => {
                    onConfirmDeleteChange(null);
                    onDelete(selectedRun.id);
                  }}
                  style={{
                    background: colors.dangerBg,
                    border: `1px solid ${colors.dangerText}`,
                    color: colors.dangerText,
                    cursor: "pointer",
                    padding: "1px 6px",
                    fontSize: 9,
                    fontFamily: fonts.sans,
                    borderRadius: 4,
                    lineHeight: 1.4,
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.background = colors.dangerHoverBg;
                    e.currentTarget.style.color = colors.white;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.background = colors.dangerBg;
                    e.currentTarget.style.color = colors.dangerText;
                  }}
                >
                  Yes
                </button>
                <button
                  onClick={() => onConfirmDeleteChange(null)}
                  style={{
                    background: "none",
                    border: `1px solid ${colors.border}`,
                    color: colors.textDim,
                    cursor: "pointer",
                    padding: "1px 6px",
                    fontSize: 9,
                    fontFamily: fonts.sans,
                    borderRadius: 4,
                    lineHeight: 1.4,
                  }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.color = colors.textLight;
                    e.currentTarget.style.borderColor = colors.textDim;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.color = colors.textDim;
                    e.currentTarget.style.borderColor = colors.border;
                  }}
                >
                  No
                </button>
              </div>
            )}
          </div>
        </div>
        <div style={{ color: colors.textDim, fontSize: 11 }}>
          Started {timeAgo(selectedRun.started_at)}
          {selectedRun.finished_at && <> &middot; Took {elapsed(selectedRun.started_at, selectedRun.finished_at)}</>}
        </div>
        {selectedRun.dir_path && <div style={{ color: colors.textDim, fontSize: 11, fontFamily: fonts.mono }}>{selectedRun.dir_path}</div>}
        {selectedRun.error_text && <div style={{ color: colors.error, fontSize: 11, padding: "4px 6px", borderRadius: 4, background: `${colors.error}18` }}>{selectedRun.error_text}</div>}
      </div>

      {/* Approval widget */}
      {selectedRun.status === "paused" && selectedRun.paused_node_id && (
        <div style={{ padding: 12, borderBottom: `1px solid ${colors.border}`, display: "flex", flexDirection: "column", gap: 8, background: `${STATUS_COLORS.paused}08` }}>
          <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Approval Required &mdash; {selectedRun.paused_node_id}</div>
          {(() => {
            const pausedNode = nodeRuns.find((n) => n.node_id === selectedRun.paused_node_id && n.status === "paused");
            const approvalDef = selectedDef?.nodes.find((n) => n.id === selectedRun.paused_node_id);
            const message = approvalDef?.message || pausedNode?.output;
            return message ? <div style={{ fontSize: 12, color: colors.text, whiteSpace: "pre-wrap", padding: "6px 8px", borderRadius: 4, background: colors.surface }}>{message}</div> : null;
          })()}
          <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
            <input
              data-testid="approval-response-input"
              type="text"
              placeholder="Response (optional)"
              value={resumeResponse}
              onChange={(e) => onResumeResponseChange(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") onResume(selectedRun.id, resumeResponse);
              }}
              style={{ ...inputStyle, flex: 1 }}
            />
            <button data-testid="approval-approve-btn" onClick={() => onResume(selectedRun.id, resumeResponse || "approved")} style={btnStyle}>
              Approve
            </button>
            <button data-testid="approval-reject-btn" onClick={() => onResume(selectedRun.id, "rejected")} style={{ ...btnSecondaryStyle, color: colors.error, borderColor: colors.error }}>
              Reject
            </button>
          </div>
        </div>
      )}

      {/* Node graph */}
      <WorkflowGraph defs={selectedDef?.nodes ?? []} nodeRuns={nodeRuns} colors={colors} onNodeClick={onToggleNodeExpand} expandedNodeId={expandedNodeId} />
    </div>
  );
}
