import { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import { marked } from "marked";
import { fonts } from "../theme";
import type { ColorPalette } from "../theme";
import { useTheme } from "../ThemeContext";
import { fetchReadme } from "../api/loopApi";
import { storageGet, storageSet } from "../utils/storage";

const MIN_WIDTH = 280;
const MAX_WIDTH_PERCENT = 0.6;
const WIDTH_STORAGE_KEY = "loop-file-panel-width";

function loadWidth(): number {
  const stored = storageGet(WIDTH_STORAGE_KEY);
  if (stored) {
    const w = parseInt(stored, 10);
    if (w >= MIN_WIDTH) return w;
  }
  return Math.floor(window.innerWidth * MAX_WIDTH_PERCENT);
}

function saveWidth(w: number) {
  storageSet(WIDTH_STORAGE_KEY, String(w));
}

function buildHeaderBtnStyle(colors: ColorPalette): React.CSSProperties {
  return {
    background: "none",
    border: "none",
    color: colors.textDim,
    cursor: "pointer",
    padding: 4,
    lineHeight: 1,
    borderRadius: 4,
    display: "flex",
    alignItems: "center",
  };
}

// ── Base FilePanel ──

interface FilePanelProps {
  title: string;
  dirPath?: string;
  branch?: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  noPadding?: boolean;
  /** When true, suppress outer chrome (resize, drag region, header) and render only children. */
  embedded?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
  children: ReactNode;
}

export function FilePanel({ title, dirPath, branch, maximized, sidebarOpen, noPadding, embedded, onToggleSidebar, onOpenPalette, onToggleMaximize, onClose, children }: FilePanelProps) {
  const { colors, fontSizes } = useTheme();
  const [width, setWidth] = useState(loadWidth);
  const [resizing, setResizing] = useState(false);

  const headerBtnStyle = buildHeaderBtnStyle(colors);
  const hoverIn = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = colors.hoverBg;
    e.currentTarget.style.color = colors.textLight;
  };
  const hoverOut = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.currentTarget.style.backgroundColor = "transparent";
    e.currentTarget.style.color = colors.textDim;
  };

  if (embedded) {
    return (
      <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar, zoom: fontSizes.panels / 12, borderRadius: colors.islandRadius, boxShadow: colors.islandShadow, border: colors.islandBorder }}>
        <div style={{ flex: 1, overflow: "auto", padding: noPadding ? 0 : "12px 16px" }}>
          {children}
        </div>
      </div>
    );
  }

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      setResizing(true);
      const startX = e.clientX;
      const startWidth = width;

      let lastWidth = startWidth;
      const onMouseMove = (ev: MouseEvent) => {
        const maxWidth = window.innerWidth * MAX_WIDTH_PERCENT;
        const newWidth = Math.min(maxWidth, Math.max(MIN_WIDTH, startWidth - (ev.clientX - startX)));
        lastWidth = newWidth;
        setWidth(newWidth);
      };

      const onMouseUp = () => {
        setResizing(false);
        saveWidth(lastWidth);
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
      };

      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [width],
  );

  return (
    <div
      style={{
        width: maximized ? "100%" : width,
        minWidth: maximized ? 0 : MIN_WIDTH,
        maxWidth: maximized ? "none" : `${MAX_WIDTH_PERCENT * 100}vw`,
        flex: maximized ? 1 : undefined,
        flexShrink: maximized ? undefined : 1,
        backgroundColor: colors.sidebar,
        zoom: fontSizes.panels / 12,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        position: "relative",
        userSelect: resizing ? "none" : undefined,
        borderRadius: colors.islandRadius,
        boxShadow: colors.islandShadow,
        border: colors.islandBorder,
      }}
    >
      {/* Resize handle */}
      {!maximized && (
        <div
          onMouseDown={handleMouseDown}
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            width: 4,
            height: "100%",
            cursor: "col-resize",
            backgroundColor: resizing ? colors.textDim : "transparent",
            zIndex: 1,
          }}
          onMouseEnter={(e) => { (e.currentTarget as HTMLDivElement).style.backgroundColor = colors.textDim; }}
          onMouseLeave={(e) => { if (!resizing) (e.currentTarget as HTMLDivElement).style.backgroundColor = "transparent"; }}
        />
      )}

      {/* Drag region */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: maximized && !sidebarOpen ? 76 : maximized ? 4 : 0,
          WebkitAppRegion: "drag",
        }}
      >
        {maximized && onToggleSidebar && (
          <button
            onClick={onToggleSidebar}
            title="Toggle sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              {sidebarOpen
                ? <polyline points="15,9 12,12 15,15" />
                : <polyline points="13,9 16,12 13,15" />
              }
            </svg>
          </button>
        )}
        {maximized && onOpenPalette && (
          <button
            onClick={onOpenPalette}
            title="Search messages (Cmd+K)"
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 8px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              gap: 4,
              fontSize: 11,
              fontFamily: fonts.mono,
              marginLeft: 6,
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
          </button>
        )}
        {maximized && dirPath && (
          <span
            style={{
              fontSize: 12,
              color: colors.textDim,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              minWidth: 0,
              display: "flex",
              alignItems: "center",
              gap: 6,
              marginLeft: 12,
            }}
          >
            {dirPath}
            {branch && (
              <>
                <span style={{ color: colors.border, flexShrink: 0 }}>|</span>
                <span style={{ fontSize: 11, color: colors.active, fontFamily: fonts.mono, flexShrink: 0 }}>
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
          </span>
        )}
        <div style={{ flex: 1 }} />
      </div>

      {/* Header */}
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
        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, overflow: "hidden" }}>
          <span
            style={{
              fontSize: 10,
              fontWeight: 700,
              color: colors.textDim,
              textTransform: "uppercase",
              letterSpacing: 1,
              flexShrink: 0,
            }}
          >
            {title}
          </span>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
          {onToggleMaximize && (
            <button
              onClick={onToggleMaximize}
              title={maximized ? "Restore panel" : "Maximize panel"}
              style={headerBtnStyle}
              onMouseEnter={hoverIn}
              onMouseLeave={hoverOut}
            >
              {maximized ? (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="4,14 10,14 10,20" />
                  <polyline points="20,10 14,10 14,4" />
                  <line x1="14" y1="10" x2="21" y2="3" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="15,3 21,3 21,9" />
                  <polyline points="9,21 3,21 3,15" />
                  <line x1="21" y1="3" x2="14" y2="10" />
                  <line x1="3" y1="21" x2="10" y2="14" />
                </svg>
              )}
            </button>
          )}
          <button
            onClick={onClose}
            title="Close panel"
            style={headerBtnStyle}
            onMouseEnter={hoverIn}
            onMouseLeave={hoverOut}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>

      {/* Content */}
      <div style={{ flex: 1, overflow: "auto", padding: noPadding ? 0 : "12px 16px" }}>
        {children}
      </div>
    </div>
  );
}

// ── MarkdownFilePanel ──

interface MarkdownFilePanelProps {
  dirPath?: string;
  branch?: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

export function MarkdownFilePanel({ dirPath, branch, ...props }: MarkdownFilePanelProps) {
  const { colors } = useTheme();
  const [content, setContent] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetchReadme()
      .then(setContent)
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load"));
  }, []);

  const html = useMemo(() => {
    if (!content) return "";
    return marked.parse(content, { async: false }) as string;
  }, [content]);

  return (
    <FilePanel title="README" dirPath={dirPath} branch={branch} {...props}>
      {error && (
        <div style={{ color: colors.error, fontSize: 13 }}>{error}</div>
      )}
      {!content && !error && (
        <div style={{ color: colors.textDim, fontSize: 13 }}>Loading...</div>
      )}
      {content && (
        <div
          className="readme-content"
          dangerouslySetInnerHTML={{ __html: html }}
          style={{
            fontSize: 13,
            fontFamily: fonts.sans,
            color: colors.text,
            lineHeight: 1.7,
          }}
        />
      )}
      <style>{buildMarkdownStyles(colors)}</style>
    </FilePanel>
  );
}

// ── Styles ──

export function buildMarkdownStyles(colors: ColorPalette): string {
  return `
.readme-content h1, .readme-content h2, .readme-content h3,
.readme-content h4, .readme-content h5, .readme-content h6 {
  color: ${colors.textLight};
  margin: 1.2em 0 0.4em;
  line-height: 1.3;
}
.readme-content h1 { font-size: 1.6em; border-bottom: 1px solid ${colors.border}; padding-bottom: 0.3em; }
.readme-content h2 { font-size: 1.3em; border-bottom: 1px solid ${colors.border}; padding-bottom: 0.2em; }
.readme-content h3 { font-size: 1.1em; }
.readme-content p { margin: 0.6em 0; }
.readme-content a { color: ${colors.active}; text-decoration: none; }
.readme-content a:hover { text-decoration: underline; }
.readme-content code {
  font-family: ${fonts.mono};
  font-size: 0.9em;
  background: ${colors.codeBg};
  padding: 2px 5px;
  border-radius: 3px;
}
.readme-content pre {
  background: ${colors.codeBlockBg};
  border: 1px solid ${colors.border};
  border-radius: 6px;
  padding: 12px;
  overflow-x: auto;
  margin: 0.8em 0;
}
.readme-content pre code {
  background: none;
  padding: 0;
  font-size: 12px;
  line-height: 1.5;
}
.readme-content ul, .readme-content ol { padding-left: 1.5em; margin: 0.5em 0; }
.readme-content li { margin: 0.25em 0; }
.readme-content blockquote {
  border-left: 3px solid ${colors.border};
  padding-left: 12px;
  margin: 0.8em 0;
  color: ${colors.textDim};
}
.readme-content table { border-collapse: collapse; margin: 0.8em 0; width: 100%; }
.readme-content th, .readme-content td {
  border: 1px solid ${colors.border};
  padding: 6px 10px;
  text-align: left;
  font-size: 12px;
}
.readme-content th { background: ${colors.codeBg}; font-weight: 600; }
.readme-content hr { border: none; border-top: 1px solid ${colors.border}; margin: 1.2em 0; }
.readme-content img { max-width: 100%; }
`;
}
