import { useCallback, useEffect, useRef, useState } from "react";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import {
  fetchAllTasks,
  updateTask,
  deleteTask,
  fetchTaskRuns,
  runTaskNow,
} from "../../api/loopApi";
import type { ScheduledTask, TaskRunLog } from "../../api/loopApi";

function buildHeaderBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: 4,
    lineHeight: 1,
    borderRadius: 4,
    display: "flex",
    alignItems: "center",
  };
}

function nextRunLabel(dateStr: string): string {
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "-";
  const diff = d.getTime() - Date.now();
  if (diff < 0) return "overdue";
  const mins = Math.floor(diff / 60000);
  if (mins < 60) return `in ${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `in ${hours}h`;
  const days = Math.floor(hours / 24);
  return `in ${days}d`;
}

function timeAgo(dateStr: string): string {
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "-";
  const diff = Date.now() - d.getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 0) return `in ${-mins}m`;
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

const TYPE_COLORS: Record<string, string> = {
  cron: "#818cf8",
  interval: "#34d399",
  once: "#fbbf24",
};

interface GlobalTasksPanelProps {
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onClose: () => void;
  onSelectChannel?: (channelId: string) => void;
}

export function GlobalTasksPanel({
  sidebarOpen,
  onToggleSidebar,
  onOpenPalette,
  onClose,
  onSelectChannel,
}: GlobalTasksPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [tasks, setTasks] = useState<ScheduledTask[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [runs, setRuns] = useState<TaskRunLog[]>([]);
  const [listWidth, setListWidth] = useState(380);
  const [editing, setEditing] = useState(false);
  const draggingRef = useRef(false);

  const headerBtnStyle = buildHeaderBtnStyle(colors);
  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

  // Edit form state
  const [editSchedule, setEditSchedule] = useState("");
  const [editType, setEditType] = useState<"cron" | "interval" | "once">("cron");
  const [editPrompt, setEditPrompt] = useState("");
  const [editWorktree, setEditWorktree] = useState(false);
  const [editOriginBranch, setEditOriginBranch] = useState("");
  const [editUpdateBeforeRun, setEditUpdateBeforeRun] = useState(false);
  const [editAutoDelete, setEditAutoDelete] = useState(0);
  const [runningNow, setRunningNow] = useState(false);

  const loadTasks = useCallback(async () => {
    try {
      const data = await fetchAllTasks();
      setTasks(data);
    } catch {
      /* ignore */
    }
  }, []);

  const loadRuns = useCallback(async (taskId: number) => {
    try {
      const data = await fetchTaskRuns(taskId);
      setRuns(data);
    } catch {
      /* ignore */
    }
  }, []);

  // Initial load + polling
  useEffect(() => {
    loadTasks();
    const interval = setInterval(loadTasks, 10_000);
    return () => clearInterval(interval);
  }, [loadTasks]);

  useEffect(() => {
    if (selectedId != null) loadRuns(selectedId);
  }, [selectedId, loadRuns]);

  // Escape to close
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") { e.preventDefault(); onClose(); }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  // Sort: enabled tasks first, then by next_run_at (already sorted by backend)
  const sortedTasks = [...tasks].sort((a, b) => {
    if (a.enabled !== b.enabled) return a.enabled ? -1 : 1;
    return 0;
  });

  const selectedTask = tasks.find((t) => t.id === selectedId) ?? null;

  const handleToggle = useCallback(
    async (task: ScheduledTask) => {
      try {
        await updateTask(task.id, { enabled: !task.enabled });
        loadTasks();
      } catch {
        /* ignore */
      }
    },
    [loadTasks],
  );

  const handleDelete = useCallback(
    async (taskId: number) => {
      try {
        await deleteTask(taskId);
        if (selectedId === taskId) setSelectedId(null);
        loadTasks();
      } catch {
        /* ignore */
      }
    },
    [selectedId, loadTasks],
  );

  const handleRunNow = useCallback(
    async (taskId: number) => {
      setRunningNow(true);
      try {
        await runTaskNow(taskId);
        setTasks((prev) => prev.map((t) => (t.id === taskId ? { ...t, running: true } : t)));
        loadRuns(taskId);
      } catch {
        /* error will appear in run history */
      } finally {
        setRunningNow(false);
      }
    },
    [loadRuns],
  );

  const startEdit = useCallback((task: ScheduledTask) => {
    setEditSchedule(task.schedule);
    setEditType(task.type);
    setEditPrompt(task.prompt);
    setEditWorktree(task.worktree);
    setEditOriginBranch(task.origin_branch || "");
    setEditUpdateBeforeRun(task.update_before_run);
    setEditAutoDelete(task.auto_delete_sec);
    setEditing(true);
  }, []);

  const handleSaveEdit = useCallback(async () => {
    if (selectedId == null) return;
    try {
      await updateTask(selectedId, {
        schedule: editSchedule,
        type: editType,
        prompt: editPrompt,
        worktree: editWorktree,
        origin_branch: editOriginBranch || undefined,
        update_before_run: editUpdateBeforeRun,
        auto_delete_sec: editAutoDelete,
      });
      setEditing(false);
      loadTasks();
    } catch {
      /* ignore */
    }
  }, [selectedId, editSchedule, editType, editPrompt, editWorktree, editOriginBranch, editUpdateBeforeRun, editAutoDelete, loadTasks]);

  // Resizable divider
  const onMouseDown = useCallback(() => {
    draggingRef.current = true;
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current) return;
      setListWidth((prev) => Math.max(240, Math.min(600, prev + e.movementX)));
    };
    const onUp = () => {
      draggingRef.current = false;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }, []);

  const inputStyle: React.CSSProperties = {
    width: "100%",
    padding: "4px 8px",
    background: colors.surface,
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.text,
    fontSize: 12,
    outline: "none",
    boxSizing: "border-box",
  };

  const selectStyle: React.CSSProperties = {
    ...inputStyle,
    cursor: "pointer",
  };

  const btnStyle: React.CSSProperties = {
    padding: "4px 10px",
    background: colors.active,
    border: "none",
    borderRadius: 4,
    color: "#fff",
    fontSize: 12,
    cursor: "pointer",
  };

  const btnSecondaryStyle: React.CSSProperties = {
    ...btnStyle,
    background: "transparent",
    border: `1px solid ${colors.border}`,
    color: colors.text,
  };

  const renderTaskRow = (task: ScheduledTask) => {
    const isSelected = task.id === selectedId;
    return (
      <div
        key={task.id}
        onClick={() => { setSelectedId(task.id); setEditing(false); }}
        style={{
          padding: "6px 8px",
          cursor: "pointer",
          display: "flex",
          flexDirection: "column",
          gap: 3,
          background: isSelected ? colors.surface : "transparent",
          borderLeft: isSelected ? `2px solid ${colors.active}` : "2px solid transparent",
          fontSize: 12,
          opacity: task.enabled ? 1 : 0.5,
        }}
        onMouseEnter={(e) => { if (!isSelected) e.currentTarget.style.background = "rgba(255,255,255,0.04)"; }}
        onMouseLeave={(e) => { if (!isSelected) e.currentTarget.style.background = "transparent"; }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <span
            style={{
              padding: "1px 5px",
              borderRadius: 3,
              fontSize: 10,
              fontWeight: 600,
              color: "#fff",
              background: TYPE_COLORS[task.type] ?? colors.textDim,
              textTransform: "uppercase",
              flexShrink: 0,
            }}
          >
            {task.type}
          </span>
          <span style={{ color: colors.textLight, fontFamily: "monospace", fontSize: 11, flexShrink: 0 }}>
            {task.schedule}
          </span>
          {task.worktree && (
            <span title="Runs in worktree" style={{ fontSize: 11, flexShrink: 0 }}>wt</span>
          )}
          <div style={{ flex: 1 }} />
          <span style={{ color: colors.textLight, fontSize: 10, flexShrink: 0 }}>#{task.id}</span>
          <button
            onClick={(e) => { e.stopPropagation(); handleToggle(task); }}
            title={task.enabled ? "Disable" : "Enable"}
            style={{
              background: "none",
              border: "none",
              cursor: "pointer",
              fontSize: 14,
              lineHeight: 1,
              padding: 0,
              color: task.enabled ? colors.active : colors.textDim,
            }}
          >
            {task.enabled ? "\u25CF" : "\u25CB"}
          </button>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <span
            onClick={(e) => {
              e.stopPropagation();
              onSelectChannel?.(task.channel_id);
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
            #{task.channel_name ?? task.channel_id.slice(0, 8)}
          </span>
          {task.channel_worktree && (
            <span title="Channel is a worktree" style={{ fontSize: 10, color: colors.textDim, border: `1px solid ${colors.border}`, borderRadius: 3, padding: "0 3px", flexShrink: 0 }}>wt</span>
          )}
          <span
            style={{
              color: colors.textDim,
              fontSize: 11,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              flex: 1,
            }}
          >
            {task.prompt}
          </span>
        </div>
        {task.dir_path && (
          <div style={{ color: colors.textDim, fontSize: 10, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {task.dir_path}
          </div>
        )}
        {task.enabled && (
          <div style={{ color: colors.textDim, fontSize: 10 }}>
            Next: {nextRunLabel(task.next_run_at)}
          </div>
        )}
      </div>
    );
  };

  const renderDetail = () => {
    if (!selectedTask) {
      return (
        <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", color: colors.textDim, fontSize: 13 }}>
          Select a task to view details
        </div>
      );
    }

    if (editing) {
      return (
        <div style={{ flex: 1, overflowY: "auto", padding: 12, display: "flex", flexDirection: "column", gap: 8 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: colors.text }}>Edit Task #{selectedTask.id}</div>
          <div style={{ display: "flex", gap: 6 }}>
            <select value={editType} onChange={(e) => setEditType(e.target.value as "cron" | "interval" | "once")} style={{ ...selectStyle, flex: 1 }}>
              <option value="cron">Cron</option>
              <option value="interval">Interval</option>
              <option value="once">Once</option>
            </select>
            <input value={editSchedule} onChange={(e) => setEditSchedule(e.target.value)} style={{ ...inputStyle, flex: 2 }} />
          </div>
          <textarea
            value={editPrompt}
            onChange={(e) => setEditPrompt(e.target.value)}
            rows={5}
            style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }}
          />
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            {!selectedTask.channel_worktree && (
            <label style={{ display: "flex", alignItems: "center", gap: 4, color: colors.textDim, fontSize: 11, cursor: "pointer" }}>
              <input type="checkbox" checked={editWorktree} onChange={(e) => setEditWorktree(e.target.checked)} />
              Worktree
            </label>
            )}
            {editWorktree && (
              <>
                <input
                  type="text"
                  placeholder="Origin branch (auto-detect)"
                  value={editOriginBranch}
                  onChange={(e) => setEditOriginBranch(e.target.value)}
                  style={{ ...inputStyle, width: 150 }}
                />
                <label style={{ display: "flex", alignItems: "center", gap: 4, color: colors.textDim, fontSize: 11, cursor: "pointer" }}>
                  <input type="checkbox" checked={editUpdateBeforeRun} onChange={(e) => setEditUpdateBeforeRun(e.target.checked)} />
                  Update before run
                </label>
              </>
            )}
            <label style={{ display: "flex", alignItems: "center", gap: 4, color: colors.textDim, fontSize: 11 }}>
              Auto-delete (s):
              <input type="number" value={editAutoDelete} onChange={(e) => setEditAutoDelete(Number(e.target.value))} style={{ ...inputStyle, width: 60 }} min={0} />
            </label>
          </div>
          <div style={{ display: "flex", gap: 6 }}>
            <button onClick={() => setEditing(false)} style={btnSecondaryStyle}>Cancel</button>
            <button onClick={handleSaveEdit} style={btnStyle}>Save</button>
          </div>
        </div>
      );
    }

    return (
      <div style={{ flex: 1, overflowY: "auto", display: "flex", flexDirection: "column" }}>
        {/* Task header */}
        <div style={{ padding: 12, borderBottom: `1px solid ${colors.border}`, display: "flex", flexDirection: "column", gap: 6 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ fontSize: 13, fontWeight: 600, color: colors.text }}>Task #{selectedTask.id}</span>
            <span
              style={{
                padding: "1px 5px",
                borderRadius: 3,
                fontSize: 10,
                fontWeight: 600,
                color: "#fff",
                background: TYPE_COLORS[selectedTask.type] ?? colors.textDim,
                textTransform: "uppercase",
              }}
            >
              {selectedTask.type}
            </span>
            {selectedTask.worktree && (
              <span style={{ fontSize: 11, color: colors.textDim, border: `1px solid ${colors.border}`, borderRadius: 3, padding: "0 4px" }}>worktree</span>
            )}
            <span
              onClick={() => onSelectChannel?.(selectedTask.channel_id)}
              style={{
                fontSize: 11,
                color: colors.active,
                cursor: onSelectChannel ? "pointer" : "default",
                padding: "0 4px",
                borderRadius: 3,
                background: `${colors.active}18`,
              }}
            >
              #{selectedTask.channel_name ?? selectedTask.channel_id.slice(0, 8)}
            </span>
            {selectedTask.channel_worktree && (
              <span style={{ fontSize: 11, color: colors.textDim, border: `1px solid ${colors.border}`, borderRadius: 3, padding: "0 4px" }}>channel worktree</span>
            )}
            <div style={{ flex: 1 }} />
            {selectedTask.running && selectedTask.thread_id ? (
              <button
                onClick={() => onSelectChannel?.(selectedTask.thread_id!)}
                style={{ ...btnStyle, background: colors.warning ?? "#eab308" }}
              >
                Running...
              </button>
            ) : (
              <button
                onClick={() => handleRunNow(selectedTask.id)}
                disabled={runningNow || selectedTask.running}
                style={{ ...btnStyle, opacity: runningNow || selectedTask.running ? 0.5 : 1 }}
              >
                {selectedTask.running ? "Running..." : runningNow ? "Starting..." : "\u25B6 Run Now"}
              </button>
            )}
            <button onClick={() => handleToggle(selectedTask)} style={{ ...btnSecondaryStyle, color: selectedTask.enabled ? (colors.warning ?? "#eab308") : colors.active }}>
              {selectedTask.enabled ? "Disable" : "Enable"}
            </button>
            <button onClick={() => startEdit(selectedTask)} style={btnSecondaryStyle}>Edit</button>
            <button
              onClick={() => handleDelete(selectedTask.id)}
              style={{ ...btnSecondaryStyle, color: colors.error ?? "#ef4444", borderColor: colors.error ?? "#ef4444" }}
            >
              Delete
            </button>
          </div>
          <div style={{ color: colors.textLight, fontSize: 12, fontFamily: "monospace" }}>
            {selectedTask.schedule}
          </div>
          {selectedTask.dir_path && (
            <div style={{ color: colors.textDim, fontSize: 11, fontFamily: "monospace" }}>
              {selectedTask.dir_path}
            </div>
          )}
          <div style={{ color: colors.text, fontSize: 12, whiteSpace: "pre-wrap" }}>
            {selectedTask.prompt}
          </div>
          {selectedTask.enabled && (
            <div style={{ color: colors.textDim, fontSize: 11 }}>
              Next run: {nextRunLabel(selectedTask.next_run_at)}
            </div>
          )}
          {selectedTask.worktree && selectedTask.origin_branch && (
            <div style={{ color: colors.textDim, fontSize: 11 }}>
              Branch: {selectedTask.origin_branch}{selectedTask.update_before_run ? " (updates before run)" : ""}
            </div>
          )}
          {selectedTask.auto_delete_sec > 0 && (
            <div style={{ color: colors.textDim, fontSize: 11 }}>
              Auto-delete after {selectedTask.auto_delete_sec}s
            </div>
          )}
        </div>

        {/* Run history */}
        <div style={{ padding: "8px 12px", borderBottom: `1px solid ${colors.border}` }}>
          <div style={{ fontSize: 11, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>
            Run History
          </div>
        </div>
        <div style={{ flex: 1, overflowY: "auto" }}>
          {runs.length === 0 ? (
            <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
              No runs yet
            </div>
          ) : (
            runs.map((run) => (
              <div
                key={run.id}
                style={{
                  padding: "6px 12px",
                  borderBottom: `1px solid ${colors.border}`,
                  fontSize: 12,
                  display: "flex",
                  flexDirection: "column",
                  gap: 2,
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span
                    style={{
                      width: 8,
                      height: 8,
                      borderRadius: "50%",
                      background: run.status === "success" ? colors.active : run.status === "failed" ? (colors.error ?? "#ef4444") : (colors.warning ?? "#eab308"),
                      flexShrink: 0,
                    }}
                  />
                  <span style={{ color: colors.text }}>{run.status}</span>
                  <span style={{ color: colors.textDim, fontSize: 11, marginLeft: "auto" }}>
                    {timeAgo(run.started_at)}
                  </span>
                </div>
                {run.error_text && (
                  <div style={{ color: colors.error ?? "#ef4444", fontSize: 11, paddingLeft: 14 }}>
                    {run.error_text}
                  </div>
                )}
                {run.response_text && (
                  <div style={{
                    color: colors.textDim,
                    fontSize: 11,
                    paddingLeft: 14,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                    maxWidth: "100%",
                  }}>
                    {run.response_text.slice(0, 200)}
                  </div>
                )}
              </div>
            ))
          )}
        </div>
      </div>
    );
  };

  return (
    <div
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
        {onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
        )}
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
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          Tasks ({tasks.length})
        </span>
        <button
          onClick={onClose}
          title="Close panel"
          style={headerBtnStyle}
          onMouseEnter={hoverIn}
          onMouseLeave={hoverOut}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>

      {/* Two-pane content */}
      <div style={{ display: "flex", flex: 1, overflow: "hidden" }}>
        {/* Left: task list */}
        <div
          style={{
            width: listWidth,
            minWidth: 240,
            display: "flex",
            flexDirection: "column",
            borderRight: `1px solid ${colors.border}`,
            background: colors.bg,
          }}
        >
          <div style={{ flex: 1, overflowY: "auto" }}>
            {sortedTasks.map((t) => renderTaskRow(t))}
            {tasks.length === 0 && (
              <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
                No scheduled tasks
              </div>
            )}
          </div>
        </div>

        {/* Resizable divider */}
        <div
          onMouseDown={onMouseDown}
          style={{
            width: 4,
            cursor: "col-resize",
            background: "transparent",
            flexShrink: 0,
          }}
        />

        {/* Right: detail view */}
        <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
          {renderDetail()}
        </div>
      </div>
    </div>
  );
}
