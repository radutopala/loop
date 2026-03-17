import { useEffect, useRef } from "react";
import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";

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
        ref={menuRef}
        onContextMenu={(e) => e.preventDefault()}
        style={{
          position: "fixed",
          top: y,
          left: x,
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
