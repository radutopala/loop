import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { WorkflowRun } from "../../api/loopApi";
import { STATUS_COLORS, timeAgo } from "../../utils/workflowHelpers";

interface WorkflowRunRowProps {
  run: WorkflowRun;
  isSelected: boolean;
  colors: ColorPalette;
  onClick: () => void;
  onSelectChannel?: (channelId: string) => void;
  testId?: string;
}

export function WorkflowRunRow({ run, isSelected, colors, onClick, onSelectChannel, testId }: WorkflowRunRowProps) {
  const statusColor = STATUS_COLORS[run.status] ?? colors.textDim;
  return (
    <div
      data-testid={testId}
      onClick={onClick}
      style={{
        padding: "6px 8px",
        cursor: "pointer",
        display: "flex",
        flexDirection: "column",
        gap: 3,
        background: isSelected ? colors.surface : "transparent",
        borderLeft: isSelected ? `2px solid ${colors.active}` : "2px solid transparent",
        fontSize: 12,
      }}
      onMouseEnter={(e) => { if (!isSelected) e.currentTarget.style.background = "rgba(255,255,255,0.04)"; }}
      onMouseLeave={(e) => { if (!isSelected) e.currentTarget.style.background = "transparent"; }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span style={{ width: 8, height: 8, borderRadius: "50%", background: statusColor, flexShrink: 0 }} />
        <span style={{ color: colors.textLight, fontWeight: 600, flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {run.workflow_name}
        </span>
        <span style={{ color: colors.textDim, fontSize: 10, flexShrink: 0 }}>{timeAgo(run.started_at)}</span>
      </div>
      {run.channel_id && (
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <span
            onClick={(e) => {
              e.stopPropagation();
              onSelectChannel?.(run.channel_id);
            }}
            title="Go to channel"
            style={{
              fontSize: 10,
              color: colors.active,
              cursor: onSelectChannel ? "pointer" : "default",
              padding: "0 4px",
              borderRadius: 3,
              background: `${colors.active}18`,
              flexShrink: 0,
            }}
          >
            {`#${run.channel_name ?? run.channel_id.slice(0, 8)}`}
          </span>
          {run.channel_worktree && (
            <span title="Channel is a worktree" style={{ fontSize: 10, color: colors.textDim, border: `1px solid ${colors.border}`, borderRadius: 3, padding: "0 3px", flexShrink: 0 }}>wt</span>
          )}
        </div>
      )}
      {run.dir_path && (
        <div style={{ color: colors.textDim, fontSize: 10, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {run.dir_path}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <span style={{
          padding: "1px 5px", borderRadius: 3, fontSize: 10, fontWeight: 600,
          color: "#fff", background: statusColor, flexShrink: 0,
        }}>
          {run.status.toUpperCase()}
        </span>
        {run.status === "paused" && run.paused_node_id && (
          <span style={{ fontSize: 10, color: colors.textDim }}>at {run.paused_node_id}</span>
        )}
        <div style={{ flex: 1 }} />
        <span style={{ color: colors.textDim, fontSize: 10, fontFamily: fonts.mono }}>{run.id.slice(0, 12)}</span>
      </div>
    </div>
  );
}
