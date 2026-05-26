import { useEffect, useLayoutEffect, useRef, useState } from "react";
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
  // Render at the caller-supplied (x,y) first, then measure on mount and
  // shift left/up if the menu overflows the viewport. This handles dropdowns
  // anchored to right-edge or bottom-edge buttons without forcing callers to
  // compute clamping themselves.
  const [pos, setPos] = useState({ x, y });

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

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("keydown", handleKey);
    };
  }, [onClose]);

  return (
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
    </>
  );
}
