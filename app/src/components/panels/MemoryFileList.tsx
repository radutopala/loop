import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";

// ── Tree data structure ──

export interface TreeNode {
  name: string;
  fullPath?: string;
  children: TreeNode[];
  isDir: boolean;
  key: string;
}

// ── File icon (matches EditorPanel style for .md) ──

export function MemoryFileIcon() {
  const color = "#519aba"; // .md color from EditorPanel
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" style={{ flexShrink: 0 }}>
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      <polyline points="14 2 14 8 20 8" fill="none" stroke={color} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" opacity="0.7" />
      <text x="12" y="18" textAnchor="middle" fill={color} fontSize="7" fontWeight="bold" fontFamily={fonts.mono}>
        M
      </text>
    </svg>
  );
}

// ── Recursive tree node ──

function MemoryTreeNode({
  node,
  depth,
  expandedDirs,
  selectedPath,
  onDirToggle,
  onFileClick,
  onFileDoubleClick,
}: {
  node: TreeNode;
  depth: number;
  expandedDirs: Set<string>;
  selectedPath: string | null;
  onDirToggle: (key: string) => void;
  onFileClick: (path: string) => void;
  onFileDoubleClick: (path: string) => void;
}) {
  const { colors } = useTheme();
  const isExpanded = expandedDirs.has(node.key);

  if (node.isDir) {
    return (
      <div>
        <button
          onClick={() => onDirToggle(node.key)}
          title={node.name}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 4,
            width: "max-content",
            minWidth: "100%",
            padding: `3px 8px 3px ${8 + depth * 16}px`,
            border: "none",
            background: "none",
            color: colors.textLight,
            cursor: "pointer",
            fontSize: 12,
            fontFamily: fonts.mono,
            fontWeight: depth === 0 ? 700 : 400,
            textAlign: "left",
            whiteSpace: "nowrap",
          }}
          onMouseEnter={(e) => {
            e.currentTarget.style.backgroundColor = colors.hoverBg;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.backgroundColor = "transparent";
          }}
        >
          <svg
            width="10"
            height="10"
            viewBox="0 0 10 10"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            style={{ flexShrink: 0, opacity: 0.6, transform: isExpanded ? "rotate(90deg)" : "none", transition: "transform 0.1s" }}
          >
            <polyline points="3,1 7,5 3,9" />
          </svg>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0, opacity: 0.6 }}>
            <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
          </svg>
          {node.name}
        </button>
        {isExpanded &&
          node.children.map((child) => (
            <MemoryTreeNode
              key={child.key}
              node={child}
              depth={depth + 1}
              expandedDirs={expandedDirs}
              selectedPath={selectedPath}
              onDirToggle={onDirToggle}
              onFileClick={onFileClick}
              onFileDoubleClick={onFileDoubleClick}
            />
          ))}
      </div>
    );
  }

  const isSelected = node.fullPath === selectedPath;
  return (
    <button
      onClick={() => {
        if (node.fullPath) onFileClick(node.fullPath);
      }}
      onDoubleClick={() => {
        if (node.fullPath) onFileDoubleClick(node.fullPath);
      }}
      title={node.fullPath}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 4,
        width: "max-content",
        minWidth: "100%",
        padding: `3px 8px 3px ${8 + depth * 16}px`,
        border: "none",
        background: isSelected ? colors.selectedBg : "none",
        color: isSelected ? colors.textLight : colors.text,
        cursor: "pointer",
        fontSize: 12,
        fontFamily: fonts.mono,
        textAlign: "left",
        whiteSpace: "nowrap",
      }}
      onMouseEnter={(e) => {
        if (!isSelected) e.currentTarget.style.backgroundColor = colors.hoverBg;
      }}
      onMouseLeave={(e) => {
        if (!isSelected) e.currentTarget.style.backgroundColor = "transparent";
      }}
    >
      <span style={{ width: 10, flexShrink: 0 }} />
      <MemoryFileIcon />
      {node.name}
    </button>
  );
}

// ── File list sidebar ──

export interface MemoryFileListProps {
  tree: TreeNode[];
  treeWidth: number;
  treeMinWidth: number;
  treeMaxWidth: number;
  treeResizing: boolean;
  expandedDirs: Set<string>;
  selectedPath: string | null;
  onLoadFiles: () => void;
  onTreeResize: (e: React.MouseEvent) => void;
  onDirToggle: (key: string) => void;
  onFileClick: (path: string) => void;
  onFileDoubleClick: (path: string) => void;
}

export function MemoryFileList({
  tree,
  treeWidth,
  treeMinWidth,
  treeMaxWidth,
  treeResizing,
  expandedDirs,
  selectedPath,
  onLoadFiles,
  onTreeResize,
  onDirToggle,
  onFileClick,
  onFileDoubleClick,
}: MemoryFileListProps) {
  const { colors } = useTheme();

  return (
    <>
      {/* File tree -- matches EditorPanel style */}
      <div
        style={{
          width: treeWidth,
          minWidth: treeMinWidth,
          maxWidth: treeMaxWidth,
          overflow: "auto",
          flexShrink: 0,
          display: "flex",
          flexDirection: "column",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", padding: "4px 8px 2px", flexShrink: 0 }}>
          <span style={{ flex: 1, fontSize: 10, fontWeight: 700, color: colors.textDim, textTransform: "uppercase", letterSpacing: 1 }}>Files</span>
          <button
            onClick={onLoadFiles}
            title="Refresh files"
            style={{ background: "none", border: "none", color: colors.textDim, cursor: "pointer", padding: 0, lineHeight: 1, display: "flex", alignItems: "center" }}
            onMouseEnter={(e) => {
              e.currentTarget.style.color = colors.textLight;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.color = colors.textDim;
            }}
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="23 4 23 10 17 10" />
              <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
            </svg>
          </button>
        </div>
        <div style={{ flex: 1, overflow: "auto", padding: "2px 0" }}>
          {tree.map((root) => (
            <MemoryTreeNode
              key={root.key}
              node={root}
              depth={0}
              expandedDirs={expandedDirs}
              selectedPath={selectedPath}
              onDirToggle={onDirToggle}
              onFileClick={onFileClick}
              onFileDoubleClick={onFileDoubleClick}
            />
          ))}
        </div>
      </div>
      {/* Tree resize handle */}
      <div
        onMouseDown={onTreeResize}
        style={{
          width: 4,
          cursor: "col-resize",
          backgroundColor: treeResizing ? colors.textDim : "transparent",
          flexShrink: 0,
          borderRight: `1px solid ${colors.border}`,
        }}
        onMouseEnter={(e) => {
          (e.currentTarget as HTMLDivElement).style.backgroundColor = colors.textDim;
        }}
        onMouseLeave={(e) => {
          if (!treeResizing) (e.currentTarget as HTMLDivElement).style.backgroundColor = "transparent";
        }}
      />
    </>
  );
}
