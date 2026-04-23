import "@fontsource/jetbrains-mono/400.css";
import type { MouseEvent as ReactMouseEvent } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";
import { fetchAuditFiles, deleteAuditFile } from "../../api/audit";
import type { AuditFileEntry } from "../../api/audit";
import { Terminal } from "./Terminal";

const PAGE_SIZE = 50;

// Container-side path the runner bind-mounts the audit host dir at (see
// internal/container/runner.go — `auditHostPath+":/var/log/loop-gate:rw"`).
const CONTAINER_AUDIT_DIR = "/var/log/loop-gate";

interface AuditPanelProps {
  channelId: string;
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatDate(d: string): string {
  const dt = new Date(d + "T00:00:00");
  if (isNaN(dt.getTime())) return d;
  return dt.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

export function AuditPanel({ channelId }: AuditPanelProps) {
  const { colors, fontSizes } = useTheme();
  const [files, setFiles] = useState<AuditFileEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [selectedDate, setSelectedDate] = useState<string | null>(null);
  const [listWidth, setListWidth] = useState(300);
  const [hoveredDate, setHoveredDate] = useState<string | null>(null);
  const [confirmingDate, setConfirmingDate] = useState<string | null>(null);
  const draggingRef = useRef(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  const loadPage = useCallback(
    async (offset: number, append: boolean) => {
      if (loading) return;
      setLoading(true);
      try {
        const resp = await fetchAuditFiles(channelId, offset, PAGE_SIZE);
        setTotal(resp.total);
        setFiles((prev) => (append ? [...prev, ...resp.files] : resp.files));
      } catch {
        /* ignore */
      } finally {
        setLoading(false);
      }
    },
    [channelId, loading],
  );

  useEffect(() => {
    setFiles([]);
    setTotal(0);
    setSelectedDate(null);
    loadPage(0, false);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelId]);

  const onListScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el || loading) return;
    if (files.length >= total) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 80) {
      loadPage(files.length, true);
    }
  }, [files.length, total, loading, loadPage]);

  useEffect(() => {
    if (!confirmingDate) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest("[data-confirm-popover]")) {
        setConfirmingDate(null);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [confirmingDate]);

  const onDelete = useCallback(
    async (date: string) => {
      try {
        await deleteAuditFile(channelId, date);
        setFiles((prev) => prev.filter((f) => f.date !== date));
        setTotal((prev) => Math.max(0, prev - 1));
        if (selectedDate === date) setSelectedDate(null);
      } catch {
        /* ignore */
      }
    },
    [channelId, selectedDate],
  );

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

  return (
    <div data-testid="audit-panel" style={{ display: "flex", flex: 1, height: "100%", overflow: "hidden", zoom: fontSizes.panels / 12 }}>
      {/* Left: audit file list */}
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
        <div style={{ padding: "6px 8px", borderBottom: `1px solid ${colors.border}`, display: "flex", gap: 6, alignItems: "center" }}>
          <span style={{ flex: 1, color: colors.textDim, fontSize: 11, fontFamily: fonts.sans }}>
            Audit logs {total > 0 ? `(${total})` : ""}
          </span>
          <button
            onClick={() => { setFiles([]); setTotal(0); loadPage(0, false); }}
            title="Refresh"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              borderRadius: 3,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 6px",
              fontSize: 11,
              lineHeight: 1,
            }}
            onMouseEnter={(e) => (e.currentTarget.style.color = colors.text)}
            onMouseLeave={(e) => (e.currentTarget.style.color = colors.textDim)}
          >
            &#x21bb;
          </button>
        </div>
        <div
          ref={scrollRef}
          onScroll={onListScroll}
          style={{ flex: 1, overflowY: "auto" }}
        >
          {files.length === 0 && !loading && (
            <div style={{ padding: 16, color: colors.textDim, fontSize: 12, textAlign: "center" }}>
              No audit logs yet
            </div>
          )}
          {files.map((f) => {
            const isSelected = f.date === selectedDate;
            const isHovered = f.date === hoveredDate;
            const isConfirming = f.date === confirmingDate;
            return (
              <div
                key={f.date}
                onClick={() => setSelectedDate(f.date)}
                style={{
                  position: "relative",
                  padding: "6px 8px",
                  cursor: "pointer",
                  display: "flex",
                  flexDirection: "column",
                  gap: 3,
                  background: isSelected ? colors.surface : isHovered || isConfirming ? "rgba(255,255,255,0.04)" : "transparent",
                  borderLeft: isSelected ? `2px solid ${colors.active}` : "2px solid transparent",
                  fontSize: 12,
                }}
                onMouseEnter={() => setHoveredDate(f.date)}
                onMouseLeave={() => setHoveredDate((prev) => (prev === f.date ? null : prev))}
              >
                <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 6 }}>
                  <div style={{ color: colors.text, fontFamily: "monospace" }}>{f.date}</div>
                  {(isHovered || isConfirming) && (
                    <button
                      onClick={(e: ReactMouseEvent) => {
                        e.stopPropagation();
                        setConfirmingDate(f.date);
                      }}
                      title="Delete audit log"
                      style={{
                        padding: "1px 6px",
                        fontSize: 10,
                        lineHeight: 1.4,
                        border: `1px solid ${colors.border}`,
                        borderRadius: 4,
                        background: "transparent",
                        color: "#ef4444",
                        cursor: "pointer",
                        fontFamily: fonts.sans,
                      }}
                      onMouseEnter={(e) => { e.currentTarget.style.borderColor = colors.textLight; e.currentTarget.style.color = colors.dangerText; }}
                      onMouseLeave={(e) => { e.currentTarget.style.borderColor = colors.border; e.currentTarget.style.color = "#ef4444"; }}
                    >
                      Delete
                    </button>
                  )}
                </div>
                <div style={{ display: "flex", justifyContent: "space-between", color: colors.textDim, fontSize: 11 }}>
                  <span>{formatDate(f.date)}</span>
                  <span>{formatSize(f.size)}</span>
                </div>
                {isConfirming && (
                  <div
                    data-confirm-popover
                    onMouseDown={(e) => e.stopPropagation()}
                    onClick={(e) => e.stopPropagation()}
                    style={{
                      position: "absolute",
                      top: "100%",
                      right: 8,
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
                      onClick={(e) => { e.stopPropagation(); setConfirmingDate(null); void onDelete(f.date); }}
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
                      onMouseEnter={(e) => { e.currentTarget.style.background = colors.dangerHoverBg; e.currentTarget.style.color = colors.white; }}
                      onMouseLeave={(e) => { e.currentTarget.style.background = colors.dangerBg; e.currentTarget.style.color = colors.dangerText; }}
                    >
                      Yes
                    </button>
                    <button
                      onClick={(e) => { e.stopPropagation(); setConfirmingDate(null); }}
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
                      onMouseEnter={(e) => { e.currentTarget.style.color = colors.textLight; e.currentTarget.style.borderColor = colors.textDim; }}
                      onMouseLeave={(e) => { e.currentTarget.style.color = colors.textDim; e.currentTarget.style.borderColor = colors.border; }}
                    >
                      No
                    </button>
                  </div>
                )}
              </div>
            );
          })}
          {loading && (
            <div style={{ padding: 12, color: colors.textDim, fontSize: 11, textAlign: "center" }}>
              Loading...
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

      {/* Right: live tail of the selected file running in the agent container. */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar }}>
        {selectedDate ? (
          <>
            <div
              style={{
                height: 22,
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                padding: "0 10px",
                backgroundColor: colors.surface,
                borderBottom: `1px solid ${colors.border}`,
                flexShrink: 0,
                fontSize: 11,
                fontFamily: fonts.sans,
              }}
            >
              <span style={{ color: colors.textDim, opacity: 0.7 }}>
                agentgate-{selectedDate}.jsonl
              </span>
              <span style={{ color: colors.textDim, opacity: 0.7 }}>
                tail -f -n 100 (agent container)
              </span>
            </div>
            {/* Remount the Terminal on date change so each file gets a fresh
                exec + PTY; instanceId carries the date so the WS session key
                is distinct per file. */}
            <Terminal
              key={`audit-tail-${channelId}-${selectedDate}`}
              channelId={channelId}
              target="agent"
              instanceId={`audit-tail-${selectedDate}`}
              cmd={["tail", "-f", "-n", "100", `${CONTAINER_AUDIT_DIR}/agentgate-${selectedDate}.jsonl`]}
              hideActions
            />
          </>
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
            Select an audit log to view
          </div>
        )}
      </div>
    </div>
  );
}
