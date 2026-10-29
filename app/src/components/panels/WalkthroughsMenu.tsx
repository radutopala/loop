import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { deleteWalkthrough, fetchWalkthrough, listWalkthroughs, type Walkthrough } from "../../api/walkthroughs";
import { closeWalkthrough, type OpenWalkthrough, showWalkthrough } from "../../hooks/walkthroughStore";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { openChannelFile } from "../../utils/openChannelFile";
import { timeAgo } from "../../utils/taskUtils";
import { usePaneHeaderSlot } from "../chat/LearnBadge";

interface WalkthroughsBadgeProps {
  /** Pane leaf id — the portal target is `pane-header-slot-${leafId}`. */
  leafId: string;
  channelId: string;
  /** The walkthrough the channel's editor shows, if it shows one. */
  open: OpenWalkthrough | null;
}

/**
 * The editor pane's walkthroughs label, at the right of its header, once the
 * channel has walkthroughs: it lists them, newest first, to open one again
 * (the one open, at its step) or delete it.
 */
export function WalkthroughsBadge({ leafId, channelId, open }: WalkthroughsBadgeProps) {
  const { colors } = useTheme();
  const slot = usePaneHeaderSlot(leafId);
  const [list, setList] = useState<Walkthrough[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const refresh = useCallback(() => {
    listWalkthroughs(channelId)
      .then((l) => {
        setList(l);
        setError(null);
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
  }, [channelId]);

  // A walkthrough the agent opened may be new.
  const openId = open?.id;
  useEffect(() => {
    void openId;
    refresh();
  }, [refresh, openId]);

  useEffect(() => {
    if (!menu) return;
    const onDown = (e: MouseEvent) => {
      if (!menuRef.current?.contains(e.target as Node)) setMenu(null);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenu(null);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [menu]);

  const reopen = async (w: Walkthrough) => {
    setMenu(null);
    try {
      if (open?.id === w.id) {
        await showWalkthrough(channelId, open, open.step, openChannelFile);
        return;
      }
      const full = await fetchWalkthrough(channelId, w.id);
      if (!full?.steps?.length) throw new Error(`walkthrough "${w.title}" has no steps`);
      await showWalkthrough(channelId, { id: full.id, title: full.title, steps: full.steps }, 0, openChannelFile);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const remove = async (w: Walkthrough) => {
    try {
      await deleteWalkthrough(channelId, w.id);
      if (open?.id === w.id) closeWalkthrough(channelId);
      setList((l) => l?.filter((x) => x.id !== w.id) ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  if (!slot || (!open && !list?.length)) return null;
  const label = open ? `walkthrough ${open.step + 1}/${open.steps.length}` : "walkthroughs";
  return createPortal(
    <>
      <button
        data-testid="walkthroughs-badge"
        onClick={(e) => {
          if (menu) {
            setMenu(null);
            return;
          }
          const r = e.currentTarget.getBoundingClientRect();
          setMenu({ x: r.right, y: r.bottom + 4 });
          refresh();
        }}
        title={open ? `${open.title}: step ${open.step + 1} of ${open.steps.length}` : "Walkthroughs of this channel"}
        aria-expanded={!!menu}
        style={{
          display: "flex",
          alignItems: "center",
          flexShrink: 0,
          height: 16,
          boxSizing: "border-box",
          whiteSpace: "nowrap",
          background: menu || open ? colors.hoverBg : "transparent",
          border: `1px solid ${colors.border}`,
          color: colors.textDim,
          cursor: "pointer",
          padding: "0 6px",
          fontSize: 10,
          fontFamily: fonts.mono,
          lineHeight: 1,
          borderRadius: 8,
        }}
      >
        {label}
      </button>
      {menu &&
        createPortal(
          <div
            ref={menuRef}
            data-testid="walkthroughs-menu"
            style={{
              position: "fixed",
              top: menu.y,
              left: Math.max(8, menu.x - 300),
              width: 300,
              maxHeight: 360,
              overflowY: "auto",
              zIndex: 1000,
              backgroundColor: colors.surface,
              border: `1px solid ${colors.border}`,
              borderRadius: 6,
              boxShadow: `0 4px 16px ${colors.shadow}`,
              padding: 4,
              fontFamily: fonts.sans,
              fontSize: 12,
            }}
          >
            {error && <div style={{ padding: "4px 8px", color: colors.error, fontSize: 11 }}>{error}</div>}
            {list?.length === 0 && <div style={{ padding: "4px 8px", color: colors.textDim }}>No walkthroughs</div>}
            {list?.map((w) => (
              <div
                key={w.id}
                data-testid="walkthroughs-menu-item"
                style={{ display: "flex", alignItems: "center", gap: 4, borderRadius: 4, backgroundColor: open?.id === w.id ? colors.selectedBg : undefined }}
                onMouseEnter={(e) => {
                  if (open?.id !== w.id) e.currentTarget.style.backgroundColor = colors.hoverBg;
                }}
                onMouseLeave={(e) => {
                  if (open?.id !== w.id) e.currentTarget.style.backgroundColor = "";
                }}
              >
                <button
                  onClick={() => void reopen(w)}
                  title={open?.id === w.id ? "Back to the step it shows" : "Open from its first step"}
                  style={{
                    flex: 1,
                    minWidth: 0,
                    display: "flex",
                    flexDirection: "column",
                    alignItems: "flex-start",
                    gap: 2,
                    padding: "5px 8px",
                    background: "none",
                    border: "none",
                    cursor: "pointer",
                    textAlign: "left",
                    color: colors.text,
                    fontFamily: fonts.sans,
                  }}
                >
                  <span style={{ maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 12 }}>{w.title}</span>
                  <span style={{ color: colors.textDim, fontSize: 10 }}>
                    {open?.id === w.id ? `step ${open.step + 1} of ${open.steps.length}` : `${w.step_count} step${w.step_count === 1 ? "" : "s"}`} · {timeAgo(w.created_at)}
                  </span>
                </button>
                <button
                  onClick={() => void remove(w)}
                  title="Delete this walkthrough"
                  aria-label={`Delete ${w.title}`}
                  style={{ flexShrink: 0, background: "none", border: "none", cursor: "pointer", color: colors.textDim, fontSize: 14, lineHeight: 1, padding: "2px 8px" }}
                  onMouseEnter={(e) => {
                    e.currentTarget.style.color = colors.dangerText;
                  }}
                  onMouseLeave={(e) => {
                    e.currentTarget.style.color = colors.textDim;
                  }}
                >
                  &times;
                </button>
              </div>
            ))}
          </div>,
          document.body,
        )}
    </>,
    slot,
  );
}
