import { useEffect, useRef, useState } from "react";
import { fonts } from "../../theme";
import { useTheme } from "../../ThemeContext";

function NewMenu({ onNewProject, onOpenDirectory, onClose }: {
  onNewProject: () => void;
  onOpenDirectory?: () => void;
  onClose: () => void;
}) {
  const { colors } = useTheme();
  const ref = useRef<HTMLDivElement>(null);

  const dropdownItemStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 6,
    width: "100%",
    padding: "4px 8px",
    border: "none",
    background: "transparent",
    color: colors.textLight,
    fontSize: 11,
    textAlign: "left",
    cursor: "pointer",
    borderRadius: 4,
    fontFamily: fonts.sans,
    whiteSpace: "nowrap",
  };

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (e.button !== 0) return;
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose();
      }
    };
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [onClose]);

  return (
    <div
      ref={ref}
      style={{
        position: "absolute",
        top: 74,
        right: 8,
        backgroundColor: colors.surface,
        border: `1px solid ${colors.border}`,
        borderRadius: 6,
        padding: 4,
        zIndex: 100,
        minWidth: 150,
        boxShadow: `0 4px 12px ${colors.shadow}`,
        fontFamily: fonts.sans,
      }}
    >
      <button
        onClick={onNewProject}
        style={dropdownItemStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
      >
        New project
      </button>
      {onOpenDirectory && (
        <button
          onClick={onOpenDirectory}
          style={dropdownItemStyle}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
        >
          Open directory...
        </button>
      )}
    </div>
  );
}

interface SidebarHeaderProps {
  searchQuery: string;
  onSearchQueryChange: (query: string) => void;
  selectMode: boolean;
  selectedCount: number;
  onBatchDelete: () => void;
  onEnterSelectMode: () => void;
  onCancelSelectMode: () => void;
  unreadCount?: number;
  onMarkAllRead?: () => void;
  onNewProject: () => void;
  onOpenDirectory?: (dirPath: string) => void;
  creatingChannel: boolean;
  newChannelName: string;
  onNewChannelNameChange: (name: string) => void;
  onCreateChannel: (name: string) => void;
  onCancelCreateChannel: () => void;
  newChannelInputRef: React.RefObject<HTMLInputElement | null>;
}

export function SidebarHeader({
  searchQuery,
  onSearchQueryChange,
  selectMode,
  selectedCount,
  onBatchDelete,
  onEnterSelectMode,
  onCancelSelectMode,
  unreadCount,
  onMarkAllRead,
  onNewProject,
  onOpenDirectory,
  creatingChannel,
  newChannelName,
  onNewChannelNameChange,
  onCreateChannel,
  onCancelCreateChannel,
  newChannelInputRef,
}: SidebarHeaderProps) {
  const { colors } = useTheme();
  const [newMenuOpen, setNewMenuOpen] = useState(false);

  const sidebarBtnStyle: React.CSSProperties = {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: "3px 8px",
    fontSize: 12,
    lineHeight: 1,
    borderRadius: 4,
    fontFamily: fonts.sans,
  };

  return (
    <>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          padding: "3px 12px",
          borderBottom: `1px solid ${colors.border}`,
          flexShrink: 0,
          boxSizing: "border-box",
          height: 35,
        }}
      >
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
          }}
        >
          Channels
        </span>
        <div style={{ display: "flex", alignItems: "center", gap: 2 }}>
          {selectMode ? (
            <>
              {selectedCount > 0 && (
                <button
                  onClick={onBatchDelete}
                  title={`Delete ${selectedCount} selected`}
                  style={sidebarBtnStyle}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.error; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
                >
                  Delete ({selectedCount})
                </button>
              )}
              <button
                onClick={onCancelSelectMode}
                title="Cancel selection"
                style={sidebarBtnStyle}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
              >
                Cancel
              </button>
            </>
          ) : (
            <>
              {(unreadCount ?? 0) > 0 && (
                <button
                  onClick={() => onMarkAllRead?.()}
                  title="Mark all as read"
                  style={sidebarBtnStyle}
                  onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                  onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
                >
                  Mark read
                </button>
              )}
              <button
                onClick={onEnterSelectMode}
                title="Select channels to delete"
                style={sidebarBtnStyle}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
              >
                Select
              </button>
              <button
                onClick={() => setNewMenuOpen((v) => !v)}
                title="New channel"
                style={sidebarBtnStyle}
                onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
                onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
              >
                + new
              </button>
            </>
          )}
        </div>
      </div>
      {newMenuOpen && (
        <NewMenu
          onNewProject={() => {
            setNewMenuOpen(false);
            onNewProject();
          }}
          onOpenDirectory={onOpenDirectory ? async () => {
            setNewMenuOpen(false);
            const dirPath = await window.loopAPI?.showOpenDirectoryDialog?.();
            if (dirPath) onOpenDirectory(dirPath);
          } : undefined}
          onClose={() => setNewMenuOpen(false)}
        />
      )}
      <div style={{ padding: "6px 12px 4px", flexShrink: 0 }}>
        <div style={{ position: "relative" }}>
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke={colors.textDim}
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{ position: "absolute", left: 7, top: "50%", transform: "translateY(-50%)", pointerEvents: "none" }}
          >
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            value={searchQuery}
            onChange={(e) => onSearchQueryChange(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Escape") onSearchQueryChange(""); }}
            placeholder="Search..."
            style={{
              width: "100%",
              background: colors.bg,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              color: colors.textLight,
              fontSize: 12,
              padding: "4px 8px 4px 24px",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
        </div>
      </div>
      {creatingChannel && (
        <div style={{ padding: "4px 12px 8px" }}>
          <input
            ref={newChannelInputRef}
            value={newChannelName}
            onChange={(e) => onNewChannelNameChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && newChannelName.trim()) {
                onCreateChannel(newChannelName.trim());
              } else if (e.key === "Escape") {
                onCancelCreateChannel();
              }
            }}
            onBlur={() => {
              onCancelCreateChannel();
            }}
            placeholder="Channel name..."
            style={{
              width: "100%",
              background: colors.bg,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              color: colors.textLight,
              fontSize: 13,
              padding: "4px 8px",
              outline: "none",
              boxSizing: "border-box",
            }}
          />
        </div>
      )}
    </>
  );
}
