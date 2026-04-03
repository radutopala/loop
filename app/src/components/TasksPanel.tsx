import { useCallback, useEffect, useRef, useState } from "react";
import { useTheme } from "../ThemeContext";
import { useEventStream } from "../hooks/useEventStream";
import {
  fetchTasks,
  createTask,
  updateTask,
  deleteTask,
  fetchTaskRuns,
  runTaskNow,
} from "../api/loopApi";
import type { ScheduledTask, TaskRunLog } from "../api/loopApi";

interface TasksPanelProps {
  channelId: string;
  allowWorktree?: boolean;
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

const TYPE_COLORS: Record<string, string> = {
  cron: "#818cf8",
  interval: "#34d399",
  once: "#fbbf24",
};

export function TasksPanel({ channelId, allowWorktree }: TasksPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [tasks, setTasks] = useState<ScheduledTask[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [runs, setRuns] = useState<TaskRunLog[]>([]);
  const [listWidth, setListWidth] = useState(340);
  const [showCreate, setShowCreate] = useState(false);
  const [editing, setEditing] = useState(false);
  const draggingRef = useRef(false);

  // Create form state
  const [newSchedule, setNewSchedule] = useState("*/30 * * * *");
  const [newType, setNewType] = useState<"cron" | "interval" | "once">("cron");
  const [newPrompt, setNewPrompt] = useState("");
  const [newWorktree, setNewWorktree] = useState(false);
  const [newAutoDelete, setNewAutoDelete] = useState(0);

  // Edit form state
  const [editSchedule, setEditSchedule] = useState("");
  const [editType, setEditType] = useState<"cron" | "interval" | "once">("cron");
  const [editPrompt, setEditPrompt] = useState("");
  const [editWorktree, setEditWorktree] = useState(false);
  const [editAutoDelete, setEditAutoDelete] = useState(0);
  const [runningNow, setRunningNow] = useState(false);

  const loadTasks = useCallback(async () => {
    try {
      const data = await fetchTasks(channelId);
      setTasks(data);
    } catch {
      /* ignore */
    }
  }, [channelId]);

  const loadRuns = useCallback(async (taskId: number) => {
    try {
      const data = await fetchTaskRuns(taskId);
      setRuns(data);
    } catch {
      /* ignore */
    }
  }, []);

  useEffect(() => {
    loadTasks();
  }, [loadTasks]);

  useEffect(() => {
    if (selectedId != null) loadRuns(selectedId);
  }, [selectedId, loadRuns]);

  // Real-time updates
  const onEvent = useCallback(
    (evt: { type: string }) => {
      if (evt.type.startsWith("task.")) {
        loadTasks();
        if (selectedId != null && evt.type === "task.run_completed") {
          loadRuns(selectedId);
        }
      }
    },
    [loadTasks, loadRuns, selectedId],
  );

  useEventStream({ channelId, onEvent });

  const selectedTask = tasks.find((t) => t.id === selectedId) ?? null;

  const handleCreate = useCallback(async () => {
    try {
      await createTask({
        channel_id: channelId,
        schedule: newSchedule,
        type: newType,
        prompt: newPrompt,
        worktree: newWorktree,
        auto_delete_sec: newAutoDelete,
      });
      setShowCreate(false);
      setNewPrompt("");
      loadTasks();
    } catch {
      /* ignore */
    }
  }, [channelId, newSchedule, newType, newPrompt, newWorktree, newAutoDelete, loadTasks]);

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
        auto_delete_sec: editAutoDelete,
      });
      setEditing(false);
      loadTasks();
    } catch {
      /* ignore */
    }
  }, [selectedId, editSchedule, editType, editPrompt, editWorktree, editAutoDelete, loadTasks]);

  // Resizable divider
  const onMouseDown = useCallback(() => {
    draggingRef.current = true;
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current) return;
      setListWidth((prev) => Math.max(200, Math.min(500, prev + e.movementX)));
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

  const renderCreateForm = () => (
    <div style={{ padding: 8, borderBottom: `1px solid ${colors.border}`, display: "flex", flexDirection: "column", gap: 6 }}>
      <div style={{ display: "flex", gap: 6 }}>
        <select value={newType} onChange={(e) => setNewType(e.target.value as "cron" | "interval" | "once")} style={{ ...selectStyle, flex: 1 }}>
          <option value="cron">Cron</option>
          <option value="interval">Interval</option>
          <option value="once">Once</option>
        </select>
        <input
          type="text"
          placeholder={newType === "cron" ? "*/30 * * * *" : newType === "interval" ? "30m" : "2026-01-01T00:00:00Z"}
          value={newSchedule}
          onChange={(e) => setNewSchedule(e.target.value)}
          style={{ ...inputStyle, flex: 2 }}
        />
      </div>
      <textarea
        placeholder="Task prompt..."
        value={newPrompt}
        onChange={(e) => setNewPrompt(e.target.value)}
        rows={3}
        style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }}
      />
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        {allowWorktree && (
          <label style={{ display: "flex", alignItems: "center", gap: 4, color: colors.textDim, fontSize: 11, cursor: "pointer" }}>
            <input type="checkbox" checked={newWorktree} onChange={(e) => setNewWorktree(e.target.checked)} />
            Worktree
          </label>
        )}
        <label style={{ display: "flex", alignItems: "center", gap: 4, color: colors.textDim, fontSize: 11 }}>
          Auto-delete (s):
          <input type="number" value={newAutoDelete} onChange={(e) => setNewAutoDelete(Number(e.target.value))} style={{ ...inputStyle, width: 60 }} min={0} />
        </label>
        <div style={{ flex: 1 }} />
        <button onClick={() => setShowCreate(false)} style={btnSecondaryStyle}>Cancel</button>
        <button onClick={handleCreate} disabled={!newPrompt.trim()} style={{ ...btnStyle, opacity: newPrompt.trim() ? 1 : 0.5 }}>Create</button>
      </div>
    </div>
  );

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
        <div
          style={{
            color: colors.textDim,
            fontSize: 11,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {task.prompt}
        </div>
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
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            {allowWorktree && (
              <label style={{ display: "flex", alignItems: "center", gap: 4, color: colors.textDim, fontSize: 11, cursor: "pointer" }}>
                <input type="checkbox" checked={editWorktree} onChange={(e) => setEditWorktree(e.target.checked)} />
                Worktree
              </label>
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
            <div style={{ flex: 1 }} />
            <button
              onClick={() => handleRunNow(selectedTask.id)}
              disabled={runningNow}
              style={{ ...btnStyle, opacity: runningNow ? 0.5 : 1 }}
            >
              {runningNow ? "Running..." : "\u25B6 Run Now"}
            </button>
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
          <div style={{ color: colors.text, fontSize: 12, whiteSpace: "pre-wrap" }}>
            {selectedTask.prompt}
          </div>
          {selectedTask.enabled && (
            <div style={{ color: colors.textDim, fontSize: 11 }}>
              Next run: {nextRunLabel(selectedTask.next_run_at)}
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
    <div style={{ display: "flex", flex: 1, height: "100%", overflow: "hidden", zoom: fontSizes.panels / 12 }}>
      {/* Left: task list */}
      <div
        style={{
          width: listWidth,
          minWidth: 200,
          display: "flex",
          flexDirection: "column",
          borderRight: `1px solid ${colors.border}`,
          background: colors.bg,
        }}
      >
        <div style={{ padding: "6px 8px", borderBottom: `1px solid ${colors.border}`, display: "flex", alignItems: "center", gap: 6 }}>
          <span style={{ fontSize: 12, color: colors.textDim, flex: 1 }}>
            {tasks.length} task{tasks.length !== 1 ? "s" : ""}
          </span>
          <button
            onClick={() => setShowCreate(!showCreate)}
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              borderRadius: 3,
              color: showCreate ? colors.active : colors.textDim,
              cursor: "pointer",
              padding: "1px 6px",
              fontSize: 12,
              lineHeight: 1,
            }}
          >
            +
          </button>
        </div>

        {showCreate && renderCreateForm()}

        <div style={{ flex: 1, overflowY: "auto" }}>
          {tasks.map((t) => renderTaskRow(t))}
          {tasks.length === 0 && !showCreate && (
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
  );
}
