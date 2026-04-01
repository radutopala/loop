import { useCallback, useEffect, useRef, useState } from "react";
import { useTheme } from "../ThemeContext";
import { Terminal } from "./Terminal";
import { fetchSessions, createThread } from "../api/loopApi";
import type { SessionEntry } from "../api/loopApi";

interface SessionsPanelProps {
  channelId: string;
  onStatusChange?: () => void;
}

function timeAgo(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  return `${days}d`;
}

export function SessionsPanel({ channelId, onStatusChange }: SessionsPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [sessions, setSessions] = useState<SessionEntry[]>([]);
  const [currentSessionId, setCurrentSessionId] = useState("");
  const [importedIds, setImportedIds] = useState<Set<string>>(new Set());
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [listWidth, setListWidth] = useState(380);
  const draggingRef = useRef(false);

  const loadSessions = useCallback(async () => {
    try {
      const resp = await fetchSessions(channelId);
      setSessions(resp.sessions);
      setCurrentSessionId(resp.current_session_id);
      setImportedIds(new Set(resp.imported_session_ids ?? []));
    } catch {
      /* ignore */
    }
  }, [channelId]);

  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  const handleImport = useCallback(
    async (e: React.MouseEvent, sid: string) => {
      e.stopPropagation();
      const shortId = sid.slice(0, 8);
      await createThread(channelId, `session ${shortId}`, sid);
      setImportedIds((prev) => new Set([...prev, sid]));
      onStatusChange?.();
    },
    [channelId, onStatusChange],
  );

  const filtered = filter
    ? sessions.filter((s) => s.session_id.includes(filter) || (s.last_message && s.last_message.toLowerCase().includes(filter.toLowerCase())))
    : sessions;

  const importedSessions = filtered.filter((s) => importedIds.has(s.session_id));
  const availableSessions = filtered.filter((s) => !importedIds.has(s.session_id));

  // Resizable divider
  const onMouseDown = useCallback(() => {
    draggingRef.current = true;
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current) return;
      setListWidth((prev) => Math.max(180, Math.min(500, prev + e.movementX)));
    };
    const onUp = () => {
      draggingRef.current = false;
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }, []);

  const renderSessionRow = (s: SessionEntry, showImport: boolean) => {
    const isCurrent = s.session_id === currentSessionId;
    const isSelected = s.session_id === selectedId;
    return (
      <div
        key={s.session_id}
        onClick={() => setSelectedId(s.session_id)}
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
        onMouseEnter={(e) => {
          if (!isSelected) e.currentTarget.style.background = "rgba(255,255,255,0.04)";
        }}
        onMouseLeave={(e) => {
          if (!isSelected) e.currentTarget.style.background = "transparent";
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          {isCurrent && (
            <span style={{ color: colors.active, fontWeight: 600 }}>*</span>
          )}
          <span
            style={{
              flex: 1,
              color: colors.text,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              fontFamily: "monospace",
            }}
            title={s.session_id}
          >
            {s.session_id.slice(0, 12)}
          </span>
          <span style={{ color: colors.textDim, fontSize: 11, flexShrink: 0 }}>
            {timeAgo(s.last_modified)}
          </span>
          {showImport && (
            <button
              onClick={(e) => handleImport(e, s.session_id)}
              title="Import as thread"
              style={{
                background: "none",
                border: `1px solid ${colors.border}`,
                borderRadius: 3,
                color: colors.textDim,
                cursor: "pointer",
                padding: "1px 4px",
                fontSize: 11,
                flexShrink: 0,
                lineHeight: 1,
              }}
              onMouseEnter={(e) => (e.currentTarget.style.color = colors.text)}
              onMouseLeave={(e) => (e.currentTarget.style.color = colors.textDim)}
            >
              + import as thread
            </button>
          )}
        </div>
        {s.last_message && (
          <div style={{
            color: colors.textDim,
            fontSize: 11,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            paddingLeft: isCurrent ? 14 : 0,
          }}>
            {s.last_message}
          </div>
        )}
      </div>
    );
  };

  return (
    <div style={{ display: "flex", flex: 1, height: "100%", overflow: "hidden", zoom: fontSizes.panels / 12 }}>
      {/* Left: session list */}
      <div
        style={{
          width: listWidth,
          minWidth: 180,
          display: "flex",
          flexDirection: "column",
          borderRight: `1px solid ${colors.border}`,
          background: colors.bg,
        }}
      >
        <div style={{ padding: "6px 8px", borderBottom: `1px solid ${colors.border}` }}>
          <input
            type="text"
            placeholder="Filter sessions..."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            style={{
              width: "100%",
              padding: "4px 8px",
              background: colors.surface,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              color: colors.text,
              fontSize: 12,
              outline: "none",
              boxSizing: "border-box",
            }}
          />
        </div>
        <div style={{ flex: 1, overflowY: "auto" }}>
          {availableSessions.length > 0 && (
            <>
              {importedSessions.length > 0 && (
                <div style={{ padding: "6px 8px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1, borderBottom: `1px solid ${colors.border}` }}>
                  Available
                </div>
              )}
              {availableSessions.map((s) => renderSessionRow(s, true))}
            </>
          )}
          {importedSessions.length > 0 && (
            <>
              {availableSessions.length > 0 && (
                <div style={{ padding: "6px 8px", fontSize: 10, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1, borderBottom: `1px solid ${colors.border}`, borderTop: `1px solid ${colors.border}` }}>
                  Imported
                </div>
              )}
              {importedSessions.map((s) => renderSessionRow(s, false))}
            </>
          )}
          {filtered.length === 0 && (
            <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
              {sessions.length === 0 ? "No sessions found" : "No matches"}
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

      {/* Right: terminal preview */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
        {selectedId ? (
          <Terminal
            key={`session-${selectedId}`}
            channelId={channelId}
            target="agent"
            instanceId={`session-${selectedId.slice(0, 8)}`}
            claudeSessionId={selectedId}
            onStatusChange={onStatusChange}
          />
        ) : (
          <div
            style={{
              flex: 1,
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              color: colors.textDim,
              fontSize: 13,
            }}
          >
            Select a session to resume
          </div>
        )}
      </div>
    </div>
  );
}
