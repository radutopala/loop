import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";

export interface MenuItem {
  label: string;
  onClick: () => void;
  danger?: boolean;
  separator?: boolean;
}

interface ContextMenuProps {
  x: number;
  y: number;
  items: MenuItem[];
  onClose: () => void;
}

export function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  const { colors } = useTheme();
  const menuRef = useRef<HTMLDivElement>(null);
  const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
  // Index of the menu item currently owning roving tabindex. -1 means
  // none focused yet — keyboard nav grabs index 0 on first ArrowDown.
  const [focusIdx, setFocusIdx] = useState<number>(-1);
  // Element that opened the menu — focus is returned here on close so
  // screen-reader users don't lose context after dismissing.
  const triggerRef = useRef<HTMLElement | null>(null);
  // Render at the caller-supplied (x,y) first, then measure on mount and
  // shift left/up if the menu overflows the viewport. This handles dropdowns
  // anchored to right-edge or bottom-edge buttons without forcing callers to
  // compute clamping themselves.
  const [pos, setPos] = useState({ x, y });

  // Indices of the "real" (non-separator) menu items — arrow nav skips
  // separators so the user never lands on a non-actionable row.
  const itemIndices = items
    .map((it, i) => (it.separator ? -1 : i))
    .filter((i) => i >= 0);

  useLayoutEffect(() => {
    const el = menuRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const margin = 8;
    let nx = x;
    let ny = y;
    if (nx + r.width > window.innerWidth - margin) {
      nx = Math.max(margin, window.innerWidth - r.width - margin);
    }
    if (ny + r.height > window.innerHeight - margin) {
      ny = Math.max(margin, window.innerHeight - r.height - margin);
    }
    // Only update state when we actually shifted. The initial useState({x,y})
    // already matches the unshifted position, so setting it again would
    // allocate a fresh object literal and force a no-op re-render on every
    // menu open.
    if (nx !== x || ny !== y) setPos({ x: nx, y: ny });
  }, [x, y]);

  // Capture the trigger on mount so we can restore focus on close,
  // then move focus into the menu itself so screen readers announce
  // the menu opening and arrow keys work immediately.
  useEffect(() => {
    triggerRef.current = (document.activeElement as HTMLElement) ?? null;
    const el = menuRef.current;
    if (el) el.focus();
    return () => {
      triggerRef.current?.focus?.();
    };
  }, []);

  // Keep DOM focus in sync with the focused index after arrow nav.
  useEffect(() => {
    if (focusIdx < 0) return;
    const btn = itemRefs.current[focusIdx];
    if (btn) btn.focus();
  }, [focusIdx]);

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
        return;
      }
      const first = itemIndices[0];
      const last = itemIndices[itemIndices.length - 1];
      if (first === undefined || last === undefined) return;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const pos = itemIndices.indexOf(focusIdx);
        let nextPos: number;
        if (pos < 0) {
          // No item focused yet — first ArrowDown lands on the first item,
          // first ArrowUp wraps to the last.
          nextPos = e.key === "ArrowDown" ? 0 : itemIndices.length - 1;
        } else {
          const delta = e.key === "ArrowDown" ? 1 : -1;
          nextPos = (pos + delta + itemIndices.length) % itemIndices.length;
        }
        const next = itemIndices[nextPos];
        if (next !== undefined) setFocusIdx(next);
        return;
      }
      if (e.key === "Home") {
        e.preventDefault();
        setFocusIdx(first);
        return;
      }
      if (e.key === "End") {
        e.preventDefault();
        setFocusIdx(last);
        return;
      }
    };
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("keydown", handleKey);
    };
  }, [onClose, focusIdx, itemIndices]);

  // Render through a portal to <body> so the menu escapes any ancestor that
  // sets CSS `zoom` (the sidebar, chat, and panel containers scale themselves by
  // the user's font-size setting). `zoom` scales a fixed-positioned descendant's
  // coordinates, so a menu nested inside would land at `clientY * zoom` — visibly
  // "way down" the larger the font. At <body> the position maps to true viewport px.
  return createPortal(
    <>
      {/* Invisible backdrop: catches clicks outside the menu */}
      <div
        style={{ position: "fixed", inset: 0, zIndex: 999 }}
        onMouseDown={(e) => {
          if (e.button === 0) onClose();
        }}
        onContextMenu={(e) => {
          e.preventDefault();
          const { clientX, clientY } = e;
          onClose();
          // After the menu + backdrop unmount, re-dispatch contextmenu
          // to whatever element is underneath so it can open its own menu.
          requestAnimationFrame(() => {
            const el = document.elementFromPoint(clientX, clientY);
            if (el) {
              el.dispatchEvent(
                new MouseEvent("contextmenu", {
                  bubbles: true,
                  cancelable: true,
                  clientX,
                  clientY,
                }),
              );
            }
          });
        }}
      />
      <div
        data-testid="context-menu"
        ref={menuRef}
        role="menu"
        aria-orientation="vertical"
        tabIndex={-1}
        onContextMenu={(e) => e.preventDefault()}
        style={{
          position: "fixed",
          top: pos.y,
          left: pos.x,
          backgroundColor: colors.surface,
          border: `1px solid ${colors.border}`,
          borderRadius: 6,
          padding: 4,
          minWidth: 150,
          zIndex: 1000,
          boxShadow: `0 4px 12px ${colors.shadow}`,
          fontFamily: fonts.sans,
          outline: "none",
        }}
      >
        {items.map((item, i) => (
          <div key={item.label + i}>
            {item.separator && (
              <div
                style={{
                  height: 1,
                  backgroundColor: colors.border,
                  margin: "2px 4px",
                }}
              />
            )}
            <button
              role="menuitem"
              tabIndex={focusIdx === i ? 0 : -1}
              ref={(el) => {
                itemRefs.current[i] = el;
              }}
              onClick={() => {
                item.onClick();
                onClose();
              }}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 6,
                width: "100%",
                padding: "4px 8px",
                border: "none",
                background: "transparent",
                color: item.danger ? colors.dangerText : colors.textLight,
                fontSize: 11,
                textAlign: "left",
                cursor: "pointer",
                borderRadius: 4,
                fontFamily: fonts.sans,
                whiteSpace: "nowrap",
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = item.danger ? colors.dangerBg : "rgba(255,255,255,0.08)";
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = "transparent";
              }}
            >
              {item.label}
            </button>
          </div>
        ))}
      </div>
    </>,
    document.body,
  );
}
