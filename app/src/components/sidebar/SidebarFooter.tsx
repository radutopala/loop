import type { ImageBuildStatusData, ImageUpdateAvailableData, UpdateStatus } from "../../types";
import { useTheme } from "../../ThemeContext";

interface SidebarFooterProps {
  updateStatus?: UpdateStatus | null;
  onDownloadUpdate?: () => void;
  onInstallUpdate?: () => void;
  imageBuildStatus?: ImageBuildStatusData | null;
  imageUpdateAvailable?: ImageUpdateAvailableData | null;
  onRebuildImage?: () => void;
  onOpenSettings?: () => void;
  onOpenTasks?: () => void;
  onOpenContainers?: () => void;
  onOpenReadme?: () => void;
}

export function SidebarFooter({
  updateStatus,
  onDownloadUpdate,
  onInstallUpdate,
  imageBuildStatus,
  imageUpdateAvailable,
  onRebuildImage,
  onOpenSettings,
  onOpenTasks,
  onOpenContainers,
  onOpenReadme,
}: SidebarFooterProps) {
  const { colors } = useTheme();

  const footerBtnStyle: React.CSSProperties = {
    display: "flex",
    alignItems: "center",
    gap: 8,
    width: "100%",
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: "6px 8px",
    fontSize: 12,
    borderRadius: 6,
    fontFamily: "inherit",
  };

  return (
    <div style={{ padding: "8px 12px", borderTop: `1px solid ${colors.border}`, display: "flex", flexDirection: "column", gap: 2 }}>
      {updateStatus?.available && (
        <button
          onClick={updateStatus.downloaded ? onInstallUpdate : updateStatus.downloading ? undefined : onDownloadUpdate}
          disabled={updateStatus.downloading}
          style={{
            ...footerBtnStyle,
            color: updateStatus.downloaded ? colors.active : updateStatus.downloading ? colors.textDim : colors.active,
            cursor: updateStatus.downloading ? "default" : "pointer",
          }}
          onMouseEnter={(e) => { if (!updateStatus.downloading) { e.currentTarget.style.backgroundColor = colors.hoverBg; } }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="7 10 12 15 17 10" />
            <line x1="12" y1="15" x2="12" y2="3" />
          </svg>
          {updateStatus.downloaded
            ? "Restart to update"
            : updateStatus.error
              ? "Update failed — click to retry"
              : updateStatus.downloading
                ? "Downloading..."
                : `Update available${updateStatus.version ? ` v${updateStatus.version}` : ""}`}
        </button>
      )}
      {imageBuildStatus?.state === "building" && (
        <div
          style={{
            ...footerBtnStyle,
            color: colors.warning,
            cursor: "default",
          }}
        >
          <svg
            width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
            style={{ animation: "spin 1s linear infinite" }}
          >
            <path d="M21 12a9 9 0 1 1-3-6.7" />
            <polyline points="21,3 21,9 15,9" />
          </svg>
          Building image...
        </div>
      )}
      {imageBuildStatus?.state === "failed" && (
        <div
          style={{
            ...footerBtnStyle,
            color: colors.error,
            cursor: "default",
          }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="12" r="10" />
            <line x1="15" y1="9" x2="9" y2="15" />
            <line x1="9" y1="9" x2="15" y2="15" />
          </svg>
          Image build failed
        </div>
      )}
      {imageUpdateAvailable && imageBuildStatus?.state !== "building" && imageBuildStatus?.state !== "failed" && (
        <button
          onClick={onRebuildImage}
          style={{
            ...footerBtnStyle,
            color: colors.active,
          }}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="7 10 12 15 17 10" />
            <line x1="12" y1="15" x2="12" y2="3" />
          </svg>
          Claude update available
        </button>
      )}
      <button
        onClick={onOpenSettings}
        style={footerBtnStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
          <circle cx="12" cy="12" r="3" />
        </svg>
        Settings
      </button>
      <button
        onClick={onOpenTasks}
        style={footerBtnStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
        Tasks
      </button>
      <button
        onClick={onOpenContainers}
        style={footerBtnStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
          <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
          <line x1="12" y1="22.08" x2="12" y2="12" />
        </svg>
        Containers
      </button>
      <button
        onClick={onOpenReadme}
        style={footerBtnStyle}
        onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = colors.hoverBg; e.currentTarget.style.color = colors.textLight; }}
        onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = "transparent"; e.currentTarget.style.color = colors.textDim; }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
          <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
        </svg>
        README
      </button>
    </div>
  );
}
