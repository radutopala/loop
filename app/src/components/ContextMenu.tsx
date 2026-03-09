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
        backgroundColor: "#2b2d31",
        border: `1px solid ${colors.border}`,
        borderRadius: 8,
        padding: "6px",
        minWidth: 180,
        zIndex: 1000,
        boxShadow: "0 8px 24px rgba(0,0,0,0.6)",
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
                margin: "4px 8px",
              }}
            />
          )}
          <button
            onClick={() => {
              item.onClick();
              onClose();
            }}
            style={{
              display: "block",
              width: "100%",
              padding: "8px 10px",
              border: "none",
              background: "transparent",
              color: item.danger ? "#f47067" : colors.textLight,
              fontSize: 14,
              fontWeight: 500,
              textAlign: "left",
              cursor: "pointer",
              borderRadius: 4,
              fontFamily: fonts.sans,
            }}
            onMouseEnter={(e) => {
              const btn = e.currentTarget;
              btn.style.backgroundColor = item.danger ? "#da373c" : colors.active;
              btn.style.color = "#fff";
            }}
            onMouseLeave={(e) => {
              const btn = e.currentTarget;
              btn.style.backgroundColor = "transparent";
              btn.style.color = item.danger ? "#f47067" : colors.textLight;
            }}
          >
            {item.label}
          </button>
        </div>
      ))}
    </div>
  );
}
