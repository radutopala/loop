import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Ticket } from "../../api/loopApi";
import { addTicketNote, assignTicket, createTicket, deleteTicket, fetchTickets, updateTicket, updateTicketStatus } from "../../api/loopApi";
import { useEventStream } from "../../hooks/useEventStream";
import { useTheme } from "../../ThemeContext";

interface KanbanPanelProps {
  channelId: string;
  dirPath: string;
  /** Inside a worktree chain: the checkout it was cut from. The toolbar then
   *  switches between the worktree's own board and that checkout's. */
  rootDirPath?: string;
  allowWorktree?: boolean;
  onSelectChannel?: (channelId: string) => void;
}

const STATUS_COLUMNS: { key: Ticket["status"]; label: string; color: string }[] = [
  { key: "open", label: "Open", color: "#818cf8" },
  { key: "in_progress", label: "In Progress", color: "#fbbf24" },
  { key: "closed", label: "Closed", color: "#34d399" },
];

const TYPE_COLORS: Record<string, string> = {
  bug: "#ef4444",
  feature: "#818cf8",
  task: "#94a3b8",
  epic: "#a855f7",
  chore: "#78716c",
};

const PRIORITY_LABELS: Record<number, string> = {
  0: "P0",
  1: "P1",
  2: "P2",
  3: "P3",
  4: "P4",
};

function isUrl(value: string): boolean {
  return /^https?:\/\//i.test(value);
}

function renderRefLink(value: string, title: string, linkColor: string, dimColor: string) {
  if (isUrl(value)) {
    return (
      <a
        href={value}
        target="_blank"
        rel="noreferrer"
        title={title}
        onClick={(e) => e.stopPropagation()}
        style={{
          fontFamily: "monospace",
          color: linkColor,
          textDecoration: "underline",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          maxWidth: "100%",
        }}
      >
        {value}
      </a>
    );
  }
  return (
    <span title={title} style={{ fontFamily: "monospace", color: dimColor }}>
      {value}
    </span>
  );
}

function formatNoteTime(timestamp: string): string {
  const d = new Date(timestamp);
  return Number.isNaN(d.getTime()) ? timestamp : d.toLocaleString();
}

const DRAWER_MS = 220;

interface TicketDrawerProps {
  open: boolean;
  onClose: () => void;
  testId: string;
  header: ReactNode;
  footer: ReactNode;
  children: ReactNode;
}

/** Full-height drawer on the right edge of the panel. It slides in from the
 *  right on open and back out on close, unmounting only once the slide ends;
 *  meanwhile it keeps showing what it last showed open, so the form doesn't
 *  go blank mid-slide when the caller clears its state. */
function TicketDrawer({ open, onClose, testId, header, footer, children }: TicketDrawerProps) {
  const { colors } = useTheme();
  const [mounted, setMounted] = useState(open);
  const [shown, setShown] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const lastContent = useRef({ header, footer, children });
  if (open) lastContent.current = { header, footer, children };
  if (open && !mounted) setMounted(true);

  useEffect(() => {
    if (open) {
      // Paint once off-screen, then slide in on the next frame.
      const frame = requestAnimationFrame(() => {
        setShown(true);
        // preventScroll: a plain focus would scroll the panel sideways to
        // reveal the still off-screen input.
        panelRef.current?.querySelector<HTMLElement>("[data-autofocus]")?.focus({ preventScroll: true });
      });
      return () => cancelAnimationFrame(frame);
    }
    setShown(false);
    const timer = setTimeout(() => setMounted(false), DRAWER_MS);
    return () => clearTimeout(timer);
  }, [open]);

  if (!mounted) return null;
  const content = lastContent.current;
  return (
    <div
      onClick={onClose}
      style={{
        position: "absolute",
        inset: 0,
        zIndex: 100,
        overflow: "hidden",
        background: shown ? "rgba(0,0,0,0.4)" : "rgba(0,0,0,0)",
        transition: `background ${DRAWER_MS}ms ease`,
        pointerEvents: open ? "auto" : "none",
      }}
    >
      <div
        ref={panelRef}
        data-testid={testId}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose();
        }}
        style={{
          position: "absolute",
          top: 0,
          right: 0,
          bottom: 0,
          width: "max(75%, min(760px, 100%))",
          background: colors.bg,
          borderLeft: `1px solid ${colors.border}`,
          boxShadow: "-8px 0 32px rgba(0,0,0,0.3)",
          transform: shown ? "translateX(0)" : "translateX(100%)",
          transition: `transform ${DRAWER_MS}ms cubic-bezier(0.2, 0, 0, 1)`,
          display: "flex",
          flexDirection: "column",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "14px 20px", borderBottom: `1px solid ${colors.border}`, flexShrink: 0 }}>
          {content.header}
          <button
            onClick={onClose}
            title="Close (Esc)"
            aria-label="Close"
            style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", fontSize: 16, lineHeight: 1, padding: "0 2px" }}
          >
            ×
          </button>
        </div>
        <div style={{ flex: 1, overflowY: "auto", padding: 20, display: "flex", flexDirection: "column", gap: 14 }}>{content.children}</div>
        <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "12px 20px", borderTop: `1px solid ${colors.border}`, flexShrink: 0 }}>{content.footer}</div>
      </div>
    </div>
  );
}

type BoardScope = "local" | "root";

export function KanbanPanel({ channelId, dirPath, rootDirPath, allowWorktree, onSelectChannel }: KanbanPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [tickets, setTickets] = useState<Ticket[]>([]);

  // Which board a worktree channel shows sticks per channel, so switching
  // layouts (which remounts the panel) keeps it.
  const scopeKey = `kanban-scope:${channelId}`;
  const [scope, setScope] = useState<BoardScope>(() => (localStorage.getItem(scopeKey) === "root" ? "root" : "local"));
  const canSwitchScope = !!rootDirPath && rootDirPath !== dirPath;
  const boardDir = canSwitchScope && scope === "root" && rootDirPath ? rootDirPath : dirPath;
  const pickScope = useCallback(
    (next: BoardScope) => {
      setScope(next);
      localStorage.setItem(scopeKey, next);
    },
    [scopeKey],
  );
  const [showCreate, setShowCreate] = useState(false);
  const [assigning, setAssigning] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Create form — draft persisted in localStorage until saved
  const draftKey = `kanban-draft:${channelId}`;
  const loadDraft = useCallback(() => {
    try {
      const raw = localStorage.getItem(draftKey);
      if (raw)
        return JSON.parse(raw) as {
          title?: string;
          type?: string;
          priority?: number;
          description?: string;
          assignee?: string;
          tags?: string;
          external_ref?: string;
          pr?: string;
          parent?: string;
          design?: string;
          acceptance?: string;
        };
    } catch {
      /* ignore */
    }
    return null;
  }, [draftKey]);
  const draft = useMemo(() => loadDraft(), [loadDraft]);
  const [newTitle, setNewTitle] = useState(draft?.title ?? "");
  const [newType, setNewType] = useState(draft?.type ?? "task");
  const [newPriority, setNewPriority] = useState(draft?.priority ?? 2);
  const [newDescription, setNewDescription] = useState(draft?.description ?? "");
  const [newAssignee, setNewAssignee] = useState(draft?.assignee ?? "");
  const [newTags, setNewTags] = useState(draft?.tags ?? "");
  const [newExternalRef, setNewExternalRef] = useState(draft?.external_ref ?? "");
  const [newPR, setNewPR] = useState(draft?.pr ?? "");
  const [newParent, setNewParent] = useState(draft?.parent ?? "");
  const [newDesign, setNewDesign] = useState(draft?.design ?? "");
  const [newAcceptance, setNewAcceptance] = useState(draft?.acceptance ?? "");
  const [showCreateAdvanced, setShowCreateAdvanced] = useState(false);

  // Persist draft to localStorage on change
  useEffect(() => {
    const hasContent = newTitle || newDescription || newType !== "task" || newPriority !== 2 || newAssignee || newTags || newExternalRef || newPR || newParent || newDesign || newAcceptance;
    if (hasContent) {
      localStorage.setItem(
        draftKey,
        JSON.stringify({
          title: newTitle,
          type: newType,
          priority: newPriority,
          description: newDescription,
          assignee: newAssignee,
          tags: newTags,
          external_ref: newExternalRef,
          pr: newPR,
          parent: newParent,
          design: newDesign,
          acceptance: newAcceptance,
        }),
      );
    } else {
      localStorage.removeItem(draftKey);
    }
  }, [draftKey, newTitle, newType, newPriority, newDescription, newAssignee, newTags, newExternalRef, newPR, newParent, newDesign, newAcceptance]);

  // Edit form
  const [editing, setEditing] = useState<Ticket | null>(null);
  const [editTitle, setEditTitle] = useState("");
  const [editType, setEditType] = useState("task");
  const [editPriority, setEditPriority] = useState(2);
  const [editDescription, setEditDescription] = useState("");
  const [editAssignee, setEditAssignee] = useState("");
  const [editTags, setEditTags] = useState("");
  const [editDeps, setEditDeps] = useState("");
  const [editExternalRef, setEditExternalRef] = useState("");
  const [editPR, setEditPR] = useState("");
  const [editDesign, setEditDesign] = useState("");
  const [editAcceptance, setEditAcceptance] = useState("");
  const [showEditAdvanced, setShowEditAdvanced] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [noteDraft, setNoteDraft] = useState("");
  const [noteError, setNoteError] = useState<string | null>(null);
  const [addingNote, setAddingNote] = useState(false);

  const loadTickets = useCallback(async () => {
    if (!boardDir) return;
    try {
      const data = await fetchTickets(boardDir, { sort: "priority" });
      setTickets(data);
    } catch {
      /* ignore */
    }
  }, [boardDir]);

  useEffect(() => {
    loadTickets();
  }, [loadTickets]);

  // Real-time updates via WebSocket
  const onEvent = useCallback(
    (evt: { type: string }) => {
      if (evt.type.startsWith("ticket.") || evt.type === "channel.created") {
        loadTickets();
      }
    },
    [loadTickets],
  );
  useEventStream({ channelId, onEvent });

  // Group tickets by status
  const columns = useMemo(() => {
    const grouped: Record<string, Ticket[]> = { open: [], in_progress: [], closed: [] };
    for (const t of tickets) {
      const col = grouped[t.status];
      if (col) {
        col.push(t);
      }
    }
    return grouped;
  }, [tickets]);

  const handleCreate = useCallback(async () => {
    if (!newTitle.trim() || !boardDir) return;
    try {
      const parsedTags = newTags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean);
      await createTicket({
        dir: boardDir,
        title: newTitle.trim(),
        type: newType,
        priority: newPriority,
        description: newDescription.trim() || undefined,
        assignee: newAssignee.trim() || undefined,
        tags: parsedTags.length > 0 ? parsedTags : undefined,
        external_ref: newExternalRef.trim() || undefined,
        pr: newPR.trim() || undefined,
        parent: newParent.trim() || undefined,
        design: newDesign.trim() || undefined,
        acceptance: newAcceptance.trim() || undefined,
      });
      setShowCreate(false);
      setNewTitle("");
      setNewType("task");
      setNewPriority(2);
      setNewDescription("");
      setNewAssignee("");
      setNewTags("");
      setNewExternalRef("");
      setNewPR("");
      setNewParent("");
      setNewDesign("");
      setNewAcceptance("");
      setShowCreateAdvanced(false);
      localStorage.removeItem(draftKey);
      loadTickets();
    } catch {
      /* ignore */
    }
  }, [boardDir, draftKey, newTitle, newType, newPriority, newDescription, newAssignee, newTags, newExternalRef, newPR, newParent, newDesign, newAcceptance, loadTickets]);

  const handleStatusChange = useCallback(
    async (ticketId: string, newStatus: string) => {
      if (!boardDir) return;
      try {
        await updateTicketStatus(ticketId, newStatus, boardDir);
        loadTickets();
      } catch {
        /* ignore */
      }
    },
    [boardDir, loadTickets],
  );

  const handleAssign = useCallback(
    async (ticketId: string) => {
      if (!boardDir) return;
      setAssigning(ticketId);
      setError(null);
      try {
        const result = await assignTicket(ticketId, { dir: boardDir, channel_id: channelId });
        onSelectChannel?.(result.thread_id);
        loadTickets();
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to assign worktree");
      } finally {
        setAssigning(null);
      }
    },
    [boardDir, channelId, onSelectChannel, loadTickets],
  );

  const openEdit = useCallback((ticket: Ticket) => {
    setEditing(ticket);
    setEditTitle(ticket.title);
    setEditType(ticket.type || "task");
    setEditPriority(ticket.priority);
    setEditDescription(ticket.description || "");
    setEditAssignee(ticket.assignee || "");
    setEditTags(ticket.tags.join(", "));
    setEditDeps(ticket.deps.join(", "));
    setEditExternalRef(ticket.external_ref || "");
    setEditPR(ticket.pr || "");
    setEditDesign(ticket.design || "");
    setEditAcceptance(ticket.acceptance || "");
    setShowEditAdvanced(!!(ticket.deps.length || ticket.external_ref || ticket.pr || ticket.design || ticket.acceptance));
    setNoteDraft("");
    setNoteError(null);
  }, []);

  // Read notes off the live list rather than the snapshot taken on open, so a
  // `tk add-note` from chat or a terminal shows up while the drawer is open.
  const editNotes = (editing && (tickets.find((t) => t.id === editing.id) ?? editing).notes) || [];

  const handleAddNote = useCallback(async () => {
    const content = noteDraft.trim();
    if (!editing || !content || !boardDir) return;
    setAddingNote(true);
    setNoteError(null);
    try {
      const updated = await addTicketNote(editing.id, { dir: boardDir, content });
      setTickets((prev) => prev.map((t) => (t.id === updated.id ? updated : t)));
      setNoteDraft("");
    } catch (err) {
      setNoteError(err instanceof Error ? err.message : "Failed to add note");
    } finally {
      setAddingNote(false);
    }
  }, [editing, boardDir, noteDraft]);

  const handleDelete = useCallback(
    async (ticketId: string) => {
      if (!boardDir) return;
      try {
        await deleteTicket(ticketId, boardDir);
        setConfirmDelete(null);
        setEditing(null);
        loadTickets();
      } catch {
        /* ignore */
      }
    },
    [boardDir, loadTickets],
  );

  const handleEdit = useCallback(async () => {
    if (!editing || !editTitle.trim() || !boardDir) return;
    try {
      const parsedTags = editTags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean);
      const parsedDeps = editDeps
        .split(",")
        .map((d) => d.trim())
        .filter(Boolean);
      await updateTicket(editing.id, {
        dir: boardDir,
        title: editTitle.trim(),
        type: editType,
        priority: editPriority,
        description: editDescription.trim(),
        assignee: editAssignee.trim(),
        tags: parsedTags,
        deps: parsedDeps,
        external_ref: editExternalRef.trim(),
        pr: editPR.trim(),
        design: editDesign.trim(),
        acceptance: editAcceptance.trim(),
      });
      setEditing(null);
      setShowEditAdvanced(false);
      loadTickets();
    } catch {
      /* ignore */
    }
  }, [editing, boardDir, editTitle, editType, editPriority, editDescription, editAssignee, editTags, editDeps, editExternalRef, editPR, editDesign, editAcceptance, loadTickets]);

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

  const btnStyle: React.CSSProperties = {
    padding: "3px 8px",
    background: colors.active,
    border: "none",
    borderRadius: 4,
    color: "#fff",
    fontSize: 11,
    cursor: "pointer",
  };

  const btnSecondaryStyle: React.CSSProperties = {
    ...btnStyle,
    background: "transparent",
    border: `1px solid ${colors.border}`,
    color: colors.text,
  };

  const renderCard = (ticket: Ticket) => {
    const isAssigning = assigning === ticket.id;
    return (
      <div
        key={ticket.id}
        data-testid={`kanban-card-${ticket.id}`}
        style={{
          padding: "8px 10px",
          background: colors.surface,
          borderRadius: 6,
          border: `1px solid ${colors.border}`,
          display: "flex",
          flexDirection: "column",
          gap: 5,
        }}
      >
        {/* Header: priority + ID */}
        <div style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 10 }}>
          {ticket.priority <= 1 && (
            <span
              style={{
                padding: "0 4px",
                borderRadius: 3,
                fontWeight: 700,
                color: "#fff",
                background: ticket.priority === 0 ? "#ef4444" : "#f97316",
              }}
            >
              {PRIORITY_LABELS[ticket.priority]}
            </span>
          )}
          {ticket.type && (
            <span
              style={{
                padding: "0 4px",
                borderRadius: 3,
                fontWeight: 600,
                color: "#fff",
                background: TYPE_COLORS[ticket.type] ?? colors.textDim,
                textTransform: "uppercase",
              }}
            >
              {ticket.type}
            </span>
          )}
          <div style={{ flex: 1 }} />
          <span style={{ color: colors.textDim, fontFamily: "monospace" }}>{ticket.id}</span>
        </div>

        {/* Title */}
        <div onClick={() => openEdit(ticket)} style={{ fontSize: 12, color: colors.text, fontWeight: 500, lineHeight: 1.3, cursor: "pointer" }} title="Click to edit">
          {ticket.title}
        </div>

        {/* Tags */}
        {ticket.tags.length > 0 && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 3 }}>
            {ticket.tags.map((tag) => (
              <span
                key={tag}
                style={{
                  padding: "0 4px",
                  borderRadius: 3,
                  fontSize: 10,
                  color: colors.textLight,
                  background: colors.bg,
                  border: `1px solid ${colors.border}`,
                }}
              >
                {tag}
              </span>
            ))}
          </div>
        )}

        {/* Assignee / external ref / PR */}
        {(ticket.assignee || ticket.external_ref || ticket.pr) && (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, fontSize: 10, color: colors.textDim, minWidth: 0 }}>
            {ticket.assignee && <span title="Assignee">{ticket.assignee}</span>}
            {ticket.external_ref && renderRefLink(ticket.external_ref, "External ref", colors.active, colors.textDim)}
            {ticket.pr && renderRefLink(ticket.pr, "Pull request", colors.active, colors.textDim)}
          </div>
        )}

        {/* Deps count */}
        {ticket.deps.length > 0 && (
          <div style={{ fontSize: 10, color: colors.textDim }}>
            {ticket.deps.length} dep{ticket.deps.length !== 1 ? "s" : ""}
          </div>
        )}

        {/* Actions */}
        <div style={{ display: "flex", gap: 4, flexWrap: "wrap", marginTop: 2 }}>
          {ticket.status === "open" && (
            <>
              <button onClick={() => handleStatusChange(ticket.id, "in_progress")} style={btnSecondaryStyle}>
                Start
              </button>
              {allowWorktree && (
                <button onClick={() => handleAssign(ticket.id)} disabled={isAssigning} style={{ ...btnStyle, opacity: isAssigning ? 0.5 : 1 }}>
                  {isAssigning ? "Assigning..." : "Assign Worktree"}
                </button>
              )}
            </>
          )}
          {ticket.status === "in_progress" && (
            <>
              <button onClick={() => handleStatusChange(ticket.id, "closed")} style={btnSecondaryStyle}>
                Close
              </button>
              <button onClick={() => handleStatusChange(ticket.id, "open")} style={btnSecondaryStyle}>
                Reopen
              </button>
            </>
          )}
          {ticket.status === "closed" && (
            <button onClick={() => handleStatusChange(ticket.id, "open")} style={btnSecondaryStyle}>
              Reopen
            </button>
          )}
        </div>
      </div>
    );
  };

  if (!dirPath) {
    return (
      <div
        data-testid="kanban-panel"
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          color: colors.textDim,
          fontSize: 13,
          zoom: fontSizes.panels / 12,
        }}
      >
        No directory configured for this channel
      </div>
    );
  }

  return (
    <div
      data-testid="kanban-panel"
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        position: "relative",
        zoom: fontSizes.panels / 12,
      }}
    >
      {/* Toolbar */}
      <div
        style={{
          padding: "6px 10px",
          borderBottom: `1px solid ${colors.border}`,
          display: "flex",
          alignItems: "center",
          gap: 8,
          background: colors.bg,
          flexShrink: 0,
        }}
      >
        {canSwitchScope && (
          <div style={{ display: "flex", border: `1px solid ${colors.border}`, borderRadius: 3, overflow: "hidden", flexShrink: 0 }}>
            {(
              [
                ["local", "Local", dirPath],
                ["root", "Root", rootDirPath],
              ] as const
            ).map(([value, label, path]) => (
              <button
                key={value}
                data-testid={`kanban-scope-${value}`}
                title={`${path}/.tickets`}
                onClick={() => pickScope(value)}
                style={{
                  background: scope === value ? colors.surface : "none",
                  border: "none",
                  color: scope === value ? colors.active : colors.textDim,
                  cursor: "pointer",
                  padding: "1px 8px",
                  fontSize: 11,
                  lineHeight: 1.4,
                }}
              >
                {label}
              </button>
            ))}
          </div>
        )}
        <span style={{ fontSize: 12, color: colors.textDim }}>
          {tickets.length} ticket{tickets.length !== 1 ? "s" : ""}
        </span>
        <span style={{ fontSize: 10, color: colors.textDim, opacity: 0.7 }}>
          Tip: use <code style={{ fontFamily: "monospace", fontSize: 10, background: colors.surface, padding: "0 3px", borderRadius: 2 }}>tk</code> in chat or terminal
        </span>
        <div style={{ flex: 1 }} />
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
          + New
        </button>
      </div>

      {/* Error banner */}
      {error && (
        <div
          style={{
            padding: "4px 10px",
            background: "#ef44441a",
            borderBottom: `1px solid #ef444444`,
            color: "#ef4444",
            fontSize: 11,
            display: "flex",
            alignItems: "center",
            gap: 6,
            flexShrink: 0,
          }}
        >
          <span style={{ flex: 1 }}>{error}</span>
          <button onClick={() => setError(null)} style={{ background: "none", border: "none", color: "#ef4444", cursor: "pointer", fontSize: 11, padding: 0 }}>
            dismiss
          </button>
        </div>
      )}

      {/* Create drawer */}
      <TicketDrawer
        open={showCreate}
        onClose={() => setShowCreate(false)}
        testId="kanban-create-drawer"
        header={<div style={{ fontSize: 14, fontWeight: 600, color: colors.text, flex: 1 }}>New Ticket</div>}
        footer={
          <>
            <div style={{ flex: 1 }} />
            <button onClick={() => setShowCreate(false)} style={btnSecondaryStyle}>
              Cancel
            </button>
            <button onClick={handleCreate} disabled={!newTitle.trim()} style={{ ...btnStyle, opacity: newTitle.trim() ? 1 : 0.5 }}>
              Create
            </button>
          </>
        }
      >
        <input
          type="text"
          placeholder="Title"
          value={newTitle}
          onChange={(e) => setNewTitle(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && newTitle.trim()) handleCreate();
          }}
          style={inputStyle}
          data-autofocus
        />
        <div style={{ display: "flex", gap: 6 }}>
          <select value={newType} onChange={(e) => setNewType(e.target.value)} style={{ ...inputStyle, flex: 1, cursor: "pointer" }}>
            <option value="task">Task</option>
            <option value="bug">Bug</option>
            <option value="feature">Feature</option>
            <option value="epic">Epic</option>
            <option value="chore">Chore</option>
          </select>
          <select value={newPriority} onChange={(e) => setNewPriority(Number(e.target.value))} style={{ ...inputStyle, flex: 1, cursor: "pointer" }}>
            <option value={0}>P0 - Critical</option>
            <option value={1}>P1 - High</option>
            <option value={2}>P2 - Medium</option>
            <option value={3}>P3 - Low</option>
            <option value={4}>P4 - Lowest</option>
          </select>
        </div>
        <textarea
          placeholder="Description (optional)"
          value={newDescription}
          onChange={(e) => setNewDescription(e.target.value)}
          rows={8}
          style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }}
        />
        <div style={{ display: "flex", gap: 6 }}>
          <input type="text" placeholder="Assignee" value={newAssignee} onChange={(e) => setNewAssignee(e.target.value)} style={{ ...inputStyle, flex: 1 }} />
          <input type="text" placeholder="Tags (comma-separated)" value={newTags} onChange={(e) => setNewTags(e.target.value)} style={{ ...inputStyle, flex: 1 }} />
        </div>
        <button
          type="button"
          onClick={() => setShowCreateAdvanced(!showCreateAdvanced)}
          style={{ background: "none", border: "none", color: colors.textDim, fontSize: 11, cursor: "pointer", padding: 0, textAlign: "left" }}
        >
          {showCreateAdvanced ? "▾ Less" : "▸ More fields"}
        </button>
        {showCreateAdvanced && (
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <div style={{ display: "flex", gap: 6 }}>
              <input type="text" placeholder="External ref (URL or e.g. gh-123)" value={newExternalRef} onChange={(e) => setNewExternalRef(e.target.value)} style={{ ...inputStyle, flex: 1 }} />
              <input type="text" placeholder="Parent ticket ID" value={newParent} onChange={(e) => setNewParent(e.target.value)} style={{ ...inputStyle, flex: 1 }} />
            </div>
            <input type="text" placeholder="PR URL (e.g. https://github.com/owner/repo/pull/123)" value={newPR} onChange={(e) => setNewPR(e.target.value)} style={inputStyle} />
            <textarea placeholder="Design notes" value={newDesign} onChange={(e) => setNewDesign(e.target.value)} rows={5} style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }} />
            <textarea
              placeholder="Acceptance criteria"
              value={newAcceptance}
              onChange={(e) => setNewAcceptance(e.target.value)}
              rows={5}
              style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }}
            />
          </div>
        )}
      </TicketDrawer>

      {/* Edit drawer */}
      <TicketDrawer
        open={!!editing}
        onClose={() => setEditing(null)}
        testId="kanban-edit-drawer"
        header={
          editing && (
            <>
              <div style={{ fontSize: 14, fontWeight: 600, color: colors.text, flex: 1 }}>Edit Ticket</div>
              <span style={{ fontSize: 11, color: colors.textDim, fontFamily: "monospace" }}>{editing.id}</span>
            </>
          )
        }
        footer={
          editing && (
            <>
              {confirmDelete === editing.id ? (
                <>
                  <span style={{ fontSize: 11, color: "#ef4444" }}>Delete?</span>
                  <button onClick={() => handleDelete(editing.id)} style={{ ...btnStyle, background: "#ef4444", fontSize: 10 }}>
                    Yes
                  </button>
                  <button onClick={() => setConfirmDelete(null)} style={{ ...btnSecondaryStyle, fontSize: 10 }}>
                    No
                  </button>
                </>
              ) : (
                <button onClick={() => setConfirmDelete(editing.id)} style={{ ...btnSecondaryStyle, color: "#ef4444", borderColor: "#ef444444" }}>
                  Delete
                </button>
              )}
              <div style={{ flex: 1 }} />
              <button onClick={() => setEditing(null)} style={btnSecondaryStyle}>
                Cancel
              </button>
              <button onClick={handleEdit} disabled={!editTitle.trim()} style={{ ...btnStyle, opacity: editTitle.trim() ? 1 : 0.5 }}>
                Save
              </button>
            </>
          )
        }
      >
        {editing && (
          <>
            <input
              type="text"
              placeholder="Title"
              value={editTitle}
              onChange={(e) => setEditTitle(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && editTitle.trim()) handleEdit();
              }}
              style={inputStyle}
              data-autofocus
            />
            <div style={{ display: "flex", gap: 6 }}>
              <select value={editType} onChange={(e) => setEditType(e.target.value)} style={{ ...inputStyle, flex: 1, cursor: "pointer" }}>
                <option value="task">Task</option>
                <option value="bug">Bug</option>
                <option value="feature">Feature</option>
                <option value="epic">Epic</option>
                <option value="chore">Chore</option>
              </select>
              <select value={editPriority} onChange={(e) => setEditPriority(Number(e.target.value))} style={{ ...inputStyle, flex: 1, cursor: "pointer" }}>
                <option value={0}>P0 - Critical</option>
                <option value={1}>P1 - High</option>
                <option value={2}>P2 - Medium</option>
                <option value={3}>P3 - Low</option>
                <option value={4}>P4 - Lowest</option>
              </select>
            </div>
            <textarea
              placeholder="Description (optional)"
              value={editDescription}
              onChange={(e) => setEditDescription(e.target.value)}
              rows={8}
              style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }}
            />
            <div style={{ display: "flex", gap: 6 }}>
              <input type="text" placeholder="Assignee" value={editAssignee} onChange={(e) => setEditAssignee(e.target.value)} style={{ ...inputStyle, flex: 1 }} />
              <input type="text" placeholder="Tags (comma-separated)" value={editTags} onChange={(e) => setEditTags(e.target.value)} style={{ ...inputStyle, flex: 1 }} />
            </div>
            <button
              type="button"
              onClick={() => setShowEditAdvanced(!showEditAdvanced)}
              style={{ background: "none", border: "none", color: colors.textDim, fontSize: 11, cursor: "pointer", padding: 0, textAlign: "left" }}
            >
              {showEditAdvanced ? "▾ Less" : "▸ More fields"}
            </button>
            {showEditAdvanced && (
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <input type="text" placeholder="Dependencies (comma-separated IDs)" value={editDeps} onChange={(e) => setEditDeps(e.target.value)} style={inputStyle} />
                <input type="text" placeholder="External ref (URL or e.g. gh-123)" value={editExternalRef} onChange={(e) => setEditExternalRef(e.target.value)} style={inputStyle} />
                <input type="text" placeholder="PR URL (e.g. https://github.com/owner/repo/pull/123)" value={editPR} onChange={(e) => setEditPR(e.target.value)} style={inputStyle} />
                <textarea placeholder="Design notes" value={editDesign} onChange={(e) => setEditDesign(e.target.value)} rows={5} style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }} />
                <textarea
                  placeholder="Acceptance criteria"
                  value={editAcceptance}
                  onChange={(e) => setEditAcceptance(e.target.value)}
                  rows={5}
                  style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }}
                />
              </div>
            )}

            {/* Notes — append-only, like `tk add-note`; adding one saves it straight away */}
            <div data-testid="kanban-ticket-notes" style={{ display: "flex", flexDirection: "column", gap: 8, borderTop: `1px solid ${colors.border}`, paddingTop: 12 }}>
              <div style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: colors.text }}>Notes</span>
                <span style={{ fontSize: 11, color: colors.textDim }}>{editNotes.length}</span>
              </div>
              {editNotes.length === 0 && <div style={{ fontSize: 11, color: colors.textDim }}>No notes yet</div>}
              {editNotes.map((note, i) => (
                <div
                  key={`${note.timestamp}-${i}`}
                  style={{ padding: "6px 8px", background: colors.surface, border: `1px solid ${colors.border}`, borderRadius: 4, display: "flex", flexDirection: "column", gap: 3 }}
                >
                  <span title={note.timestamp} style={{ fontSize: 10, color: colors.textDim }}>
                    {formatNoteTime(note.timestamp)}
                  </span>
                  <span style={{ fontSize: 12, color: colors.text, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{note.content}</span>
                </div>
              ))}
              <textarea
                placeholder="Add a note (⌘/Ctrl+Enter to add)"
                value={noteDraft}
                onChange={(e) => setNoteDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                    e.preventDefault();
                    handleAddNote();
                  }
                }}
                rows={3}
                style={{ ...inputStyle, resize: "vertical", fontFamily: "inherit" }}
              />
              {noteError && <div style={{ fontSize: 11, color: "#ef4444" }}>{noteError}</div>}
              <div style={{ display: "flex", justifyContent: "flex-end" }}>
                <button onClick={handleAddNote} disabled={!noteDraft.trim() || addingNote} style={{ ...btnSecondaryStyle, opacity: noteDraft.trim() && !addingNote ? 1 : 0.5 }}>
                  {addingNote ? "Adding..." : "Add note"}
                </button>
              </div>
            </div>
          </>
        )}
      </TicketDrawer>

      {/* Kanban columns */}
      <div
        style={{
          flex: 1,
          display: "flex",
          overflow: "hidden",
        }}
      >
        {STATUS_COLUMNS.map((col) => (
          <div
            key={col.key}
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              borderRight: col.key !== "closed" ? `1px solid ${colors.border}` : undefined,
              overflow: "hidden",
            }}
          >
            {/* Column header */}
            <div
              style={{
                padding: "8px 10px",
                borderBottom: `1px solid ${colors.border}`,
                display: "flex",
                alignItems: "center",
                gap: 6,
                background: colors.bg,
                flexShrink: 0,
              }}
            >
              <span
                style={{
                  width: 8,
                  height: 8,
                  borderRadius: "50%",
                  background: col.color,
                  flexShrink: 0,
                }}
              />
              <span style={{ fontSize: 12, fontWeight: 600, color: colors.text }}>{col.label}</span>
              <span style={{ fontSize: 11, color: colors.textDim }}>{columns[col.key]?.length ?? 0}</span>
            </div>

            {/* Cards */}
            <div
              style={{
                flex: 1,
                overflowY: "auto",
                padding: 6,
                display: "flex",
                flexDirection: "column",
                gap: 6,
              }}
            >
              {(columns[col.key] ?? []).map((t) => renderCard(t))}
              {(columns[col.key] ?? []).length === 0 && <div style={{ padding: 12, color: colors.textDim, fontSize: 11, textAlign: "center" }}>No tickets</div>}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
