import { useEffect, useRef } from "react";
import { colors, fonts } from "../theme";

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
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      // Only close on left-click; right-click will replace via contextmenu handler.
      if (e.button !== 0) return;
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        onClose();
      }
    };
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", handleClick);
    document.addEventListener("keydown", handleKey);
    return () => {
      document.removeEventListener("mousedown", handleClick);
      document.removeEventListener("keydown", handleKey);
    };
  }, [onClose]);

  return (
    <div
      ref={menuRef}
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
        boxShadow: "0 4px 12px rgba(0,0,0,0.3)",
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
              color: item.danger ? "#f47067" : colors.textLight,
              fontSize: 11,
              textAlign: "left",
              cursor: "pointer",
              borderRadius: 4,
              fontFamily: fonts.sans,
              whiteSpace: "nowrap",
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.backgroundColor = item.danger ? "rgba(218, 55, 60, 0.2)" : "rgba(255,255,255,0.08)";
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
  );
}
