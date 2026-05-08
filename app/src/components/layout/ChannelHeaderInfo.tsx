import type { Channel } from "../../types";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";

interface ChannelHeaderInfoProps {
  channel: Channel;
  colors: ColorPalette;
  /** When true, omit the branch display (e.g. when a HeaderBranchPicker is rendered separately). */
  hideBranch?: boolean;
}

/** Read-only channel info (dir path, session, commit, branch) for use in header drag regions. */
export function ChannelHeaderInfo({ channel, colors, hideBranch }: ChannelHeaderInfoProps) {
  const dirPath = channel.dir_path || "";
  const branch = channel.branch || "";
  const commit = channel.commit || "";

  if (!dirPath) return null;

  return (
    <>
      <span
        onDoubleClick={(e) => { navigator.clipboard.writeText(dirPath); const sel = window.getSelection(); sel?.selectAllChildren(e.currentTarget); }}
        title="Double-click to copy path"
        style={{
          fontSize: 12,
          color: colors.textDim,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          minWidth: 0,
          marginLeft: 12,
          cursor: "default",
          WebkitAppRegion: "no-drag",
        }}
      >
        {dirPath}
      </span>
      <span style={{ color: colors.border, flexShrink: 0, margin: "0 8px 0 12px" }}>|</span>
      <span style={{ fontSize: 11, color: colors.textDim, fontFamily: fonts.mono, flexShrink: 0, display: "inline-flex", alignItems: "center", gap: 4 }}>
        {channel.parent_id ? (
          <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-label="Thread">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
          </svg>
        ) : (
          <span aria-label="Channel">#</span>
        )}
        <span
          onDoubleClick={(e) => { navigator.clipboard.writeText(channel.id); const sel = window.getSelection(); sel?.selectAllChildren(e.currentTarget); }}
          title={`${channel.parent_id ? "Thread" : "Channel"} ${channel.id}\nDouble-click to copy`}
          style={{ cursor: "default", WebkitAppRegion: "no-drag" }}
        >
          {channel.id}
        </span>
      </span>
      <>
        <span style={{ color: colors.border, flexShrink: 0, margin: "0 8px 0 12px" }}>|</span>
        <span style={{ fontSize: 11, color: colors.textDim, fontFamily: fonts.mono, flexShrink: 0, display: "inline-flex", alignItems: "center", gap: 4 }}>
          <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" aria-label="Claude">
            <rect x="6" y="3" width="12" height="6" rx="3" />
            <rect x="2" y="10" width="20" height="6" rx="3" />
            <rect x="6" y="18" width="3" height="3" rx="0.5" />
            <rect x="15" y="18" width="3" height="3" rx="0.5" />
          </svg>
          {channel.session_id ? (
            <span
              onDoubleClick={(e) => { navigator.clipboard.writeText(channel.session_id); const sel = window.getSelection(); sel?.selectAllChildren(e.currentTarget); }}
              title={`Session: ${channel.session_id}\nDouble-click to copy`}
              style={{ cursor: "default", WebkitAppRegion: "no-drag" }}
            >
              {channel.session_id}
            </span>
          ) : (
            <span>no session</span>
          )}
        </span>
      </>
      {commit && (
        <>
          <span style={{ color: colors.border, flexShrink: 0, margin: "0 8px 0 12px" }}>|</span>
          <span style={{ fontSize: 11, color: colors.textDim, fontFamily: fonts.mono, flexShrink: 0, display: "inline-flex", alignItems: "center", gap: 4 }}>
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-label="Commit">
              <circle cx="12" cy="12" r="3" />
              <line x1="3" y1="12" x2="9" y2="12" />
              <line x1="15" y1="12" x2="21" y2="12" />
            </svg>
            <span
              onDoubleClick={(e) => { navigator.clipboard.writeText(commit); const sel = window.getSelection(); sel?.selectAllChildren(e.currentTarget); }}
              title="Double-click to copy commit hash"
              style={{ cursor: "default", WebkitAppRegion: "no-drag" }}
            >
              {commit}
            </span>
          </span>
        </>
      )}
      {branch && !hideBranch && (
        <>
          <span style={{ color: colors.border, flexShrink: 0, margin: "0 8px" }}>|</span>
          <span
            onDoubleClick={(e) => { navigator.clipboard.writeText(branch); const sel = window.getSelection(); sel?.selectAllChildren(e.currentTarget); }}
            title="Double-click to copy branch name"
            style={{ fontSize: 11, color: channel.worktree ? colors.active : colors.textDim, fontFamily: fonts.mono, flexShrink: 0, cursor: "default",
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ marginRight: 2, verticalAlign: -1 }}>
              <line x1="6" y1="3" x2="6" y2="15" />
              <circle cx="18" cy="6" r="3" />
              <circle cx="6" cy="18" r="3" />
              <path d="M18 9a9 9 0 0 1-9 9" />
            </svg>
            {branch}
          </span>
        </>
      )}
    </>
  );
}
