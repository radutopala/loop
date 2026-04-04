import { fonts } from "../theme";
import { useTheme } from "../ThemeContext";
import type { FileEntry } from "../api/loopApi";

// ── Multi-root path key helpers ──
// Internal path keys use "{rootIndex}:{relativePath}" to disambiguate across roots.
// When there's only one root (index 0), the prefix is still present internally
// but stripped for display / API calls.

export function makePathKey(rootIndex: number, relativePath: string): string {
  return `${rootIndex}:${relativePath}`;
}

export function parsePathKey(key: string): { rootIndex: number; relativePath: string } {
  const colonIdx = key.indexOf(":");
  if (colonIdx < 0) return { rootIndex: 0, relativePath: key };
  const rootIndex = parseInt(key.substring(0, colonIdx), 10);
  return { rootIndex: isNaN(rootIndex) ? 0 : rootIndex, relativePath: key.substring(colonIdx + 1) };
}

// ── Tree sidebar constants ──

export const TREE_MIN_WIDTH = 120;
export const TREE_MAX_WIDTH = 500;
export const TREE_DEFAULT_WIDTH = 280;
const TREE_WIDTH_KEY = "loop-editor-tree-width";

export function loadTreeWidth(): number {
  try {
    const stored = localStorage.getItem(TREE_WIDTH_KEY);
    if (stored) {
      const w = parseInt(stored, 10);
      if (w >= TREE_MIN_WIDTH && w <= TREE_MAX_WIDTH) return w;
    }
  } catch { /* ignore */ }
  return TREE_DEFAULT_WIDTH;
}

export function saveTreeWidth(width: number): void {
  try { localStorage.setItem(TREE_WIDTH_KEY, String(width)); } catch { /* ignore */ }
}

// ── File Icons ──

function fileIcon(name: string): { color: string; label: string } {
  const ext = name.split(".").pop()?.toLowerCase() || "";
  switch (ext) {
    case "go": return { color: "#00add8", label: "Go" };
    case "ts": case "tsx": return { color: "#3178c6", label: "TS" };
    case "js": case "jsx": case "mjs": case "cjs": return { color: "#f7df1e", label: "JS" };
    case "py": return { color: "#3776ab", label: "Py" };
    case "rs": return { color: "#dea584", label: "Rs" };
    case "json": case "jsonl": return { color: "#cbcb41", label: "{}" };
    case "yaml": case "yml": return { color: "#cb171e", label: "Y" };
    case "toml": return { color: "#9c4221", label: "T" };
    case "md": case "mdx": return { color: "#519aba", label: "M" };
    case "html": case "htm": return { color: "#e34c26", label: "<>" };
    case "css": case "scss": case "less": return { color: "#563d7c", label: "#" };
    case "svg": return { color: "#ffb13b", label: "S" };
    case "sh": case "bash": case "zsh": return { color: "#89e051", label: "$" };
    case "sql": return { color: "#e38c00", label: "Q" };
    case "mod": return { color: "#00add8", label: "Go" };
    case "sum": return { color: "#00add8", label: "Go" };
    case "dockerfile": return { color: "#384d54", label: "D" };
    case "makefile": return { color: "#6d8086", label: "M" };
    case "txt": case "log": case "out": return { color: "#6d8086", label: "" };
    case "png": case "jpg": case "jpeg": case "gif": case "webp": case "ico": return { color: "#a074c4", label: "I" };
    default: break;
  }
  const lower = name.toLowerCase();
  if (lower === "makefile") return { color: "#6d8086", label: "M" };
  if (lower === "dockerfile") return { color: "#384d54", label: "D" };
  if (lower === "license") return { color: "#d4930d", label: "L" };
  if (lower.startsWith(".git")) return { color: "#f14e32", label: "G" };
  if (lower.startsWith(".env")) return { color: "#ecd53f", label: "E" };
  return { color: "", label: "" };
}

export function FileIcon({ name }: { name: string }) {
  const { colors } = useTheme();
  const info = fileIcon(name);
  const color = info.color || colors.textDim;
  const label = info.label;
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" style={{ flexShrink: 0 }}>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      <polyline points="14 2 14 8 20 8" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      {label && (
        <text x="12" y="18" textAnchor="middle" fill={color} fontSize="7" fontWeight="bold" fontFamily={fonts.mono}>{label}</text>
      )}
    </svg>
  );
}

// ── File Tree ──

export interface FileTreeProps {
  entries: FileEntry[];
  dirContents: Map<string, FileEntry[]>;
  expandedDirs: Set<string>;
  selectedPath: string | null;
  previewTab: string | null;
  selectedDir: string;
  depth: number;
  parentPath: string;
  rootIndex: number;
  onDirClick: (path: string) => void;
  onFileClick: (path: string, entry: FileEntry) => void;
  onFileDoubleClick: (path: string, entry: FileEntry) => void;
  onContextMenu: (e: React.MouseEvent, path: string, isDir: boolean) => void;
}

export function FileTree({ entries, dirContents, expandedDirs, selectedPath, previewTab, selectedDir, depth, parentPath, rootIndex, onDirClick, onFileClick, onFileDoubleClick, onContextMenu }: FileTreeProps) {
  const { colors } = useTheme();
  return (
    <>
      {entries.map((entry) => {
        // parentPath is already a root-prefixed key (e.g. "0:" or "0:src").
        // For children, strip the root prefix from parentPath to get the relative parent,
        // then build the child relative path, then re-prefix.
        const { relativePath: parentRel } = parsePathKey(parentPath);
        const childRel = parentRel ? `${parentRel}/${entry.name}` : entry.name;
        const pathKey = makePathKey(rootIndex, childRel);
        const isDir = entry.type === "dir";
        const isExpanded = expandedDirs.has(pathKey);
        const isSelected = pathKey === selectedPath;
        const isDirSelected = isDir && pathKey === selectedDir;

        return (
          <div key={pathKey}>
            <button
              onClick={() => isDir ? onDirClick(pathKey) : onFileClick(pathKey, entry)}
              onDoubleClick={() => { if (!isDir) onFileDoubleClick(pathKey, entry); }}
              onContextMenu={(e) => onContextMenu(e, pathKey, isDir)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 4,
                width: "max-content",
                minWidth: "100%",
                padding: `3px 8px 3px ${8 + depth * 16}px`,
                border: "none",
                background: isSelected ? colors.selectedBg : isDirSelected ? colors.dirSelectedBg : "none",
                color: isSelected || isDirSelected ? colors.textLight : colors.text,
                cursor: "pointer",
                fontSize: 12,
                fontFamily: fonts.mono,
                textAlign: "left",
                whiteSpace: "nowrap",
              }}
              onMouseEnter={(e) => { if (!isSelected && !isDirSelected) e.currentTarget.style.backgroundColor = colors.hoverBg; }}
              onMouseLeave={(e) => { if (!isSelected && !isDirSelected) e.currentTarget.style.backgroundColor = "transparent"; }}
            >
              {isDir ? (
                <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1.5" style={{ flexShrink: 0, opacity: 0.6, transform: isExpanded ? "rotate(90deg)" : "none", transition: "transform 0.1s" }}>
                  <polyline points="3,1 7,5 3,9" />
                </svg>
              ) : (
                <span style={{ width: 10, flexShrink: 0 }} />
              )}
              {isDir ? (
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.6 }}>
                  <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                </svg>
              ) : (
                <FileIcon name={entry.name} />
              )}
              {entry.name}
            </button>
            {isDir && isExpanded && dirContents.has(pathKey) && (
              <FileTree
                entries={dirContents.get(pathKey)!}
                dirContents={dirContents}
                expandedDirs={expandedDirs}
                selectedPath={selectedPath}
                previewTab={previewTab}
                selectedDir={selectedDir}
                depth={depth + 1}
                parentPath={pathKey}
                rootIndex={rootIndex}
                onDirClick={onDirClick}
                onFileClick={onFileClick}
                onFileDoubleClick={onFileDoubleClick}
                onContextMenu={onContextMenu}
              />
            )}
          </div>
        );
      })}
    </>
  );
}
