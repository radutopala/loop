import { useEffect, useMemo, useState } from "react";
import { marked } from "marked";
import { colors, fonts } from "../theme";
import { fetchMemoryFiles, fetchMemoryFileContent, type MemoryFileInfo } from "../api/loopApi";
import { FilePanel, markdownStyles } from "./FilePanel";

interface MemoryPanelProps {
  channelId: string;
  dirPath: string;
  branch: string;
  maximized?: boolean;
  sidebarOpen?: boolean;
  onToggleSidebar?: () => void;
  onOpenPalette?: () => void;
  onToggleMaximize?: () => void;
  onClose: () => void;
}

export function MemoryPanel({ channelId, dirPath, branch, ...panelProps }: MemoryPanelProps) {
  const [files, setFiles] = useState<MemoryFileInfo[]>([]);
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [content, setContent] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<string | null>(null);
  const [contentError, setContentError] = useState<string | null>(null);

  // Fetch file list when channelId changes.
  useEffect(() => {
    setLoading(true);
    setListError(null);
    setContentError(null);
    setFiles([]);
    setSelectedFile(null);
    setContent(null);
    fetchMemoryFiles(channelId)
      .then((f) => {
        setFiles(f);
        if (f.length > 0 && f[0]) setSelectedFile(f[0].file_path);
      })
      .catch((err) => setListError(err instanceof Error ? err.message : "Failed to load"))
      .finally(() => setLoading(false));
  }, [channelId]);

  // Fetch content when selected file changes.
  useEffect(() => {
    if (!selectedFile) {
      setContent(null);
      return;
    }
    setContent(null);
    setContentError(null);
    fetchMemoryFileContent(selectedFile)
      .then(setContent)
      .catch((err) => setContentError(err instanceof Error ? err.message : "Failed to load file"));
  }, [selectedFile]);

  // Group files by dir_path.
  const groups = useMemo(() => {
    const map = new Map<string, MemoryFileInfo[]>();
    for (const f of files) {
      const list = map.get(f.dir_path) || [];
      list.push(f);
      map.set(f.dir_path, list);
    }
    return map;
  }, [files]);

  const multipleGroups = groups.size > 1;

  const html = useMemo(() => {
    if (!content) return "";
    return marked.parse(content, { async: false }) as string;
  }, [content]);

  return (
    <FilePanel title="Memory" dirPath={dirPath} branch={branch} {...panelProps}>
      {listError && (
        <div style={{ color: colors.error, fontSize: 13 }}>{listError}</div>
      )}
      {loading && (
        <div style={{ color: colors.textDim, fontSize: 13 }}>Loading...</div>
      )}
      {!loading && !listError && files.length === 0 && (
        <div style={{ color: colors.textDim, fontSize: 13 }}>No memory files indexed</div>
      )}
      {!loading && files.length > 0 && (
        <div style={{ display: "flex", height: "100%", margin: "-12px -16px" }}>
          {/* File tree */}
          <div
            style={{
              width: 200,
              minWidth: 200,
              borderRight: `1px solid ${colors.border}`,
              overflow: "auto",
              padding: "8px 0",
            }}
          >
            {[...groups.entries()].map(([dp, groupFiles]) => (
              <div key={dp}>
                {multipleGroups && (
                  <div
                    style={{
                      fontSize: 10,
                      fontWeight: 700,
                      color: colors.textDim,
                      textTransform: "uppercase",
                      letterSpacing: 0.5,
                      padding: "6px 10px 2px",
                      whiteSpace: "nowrap",
                    }}
                    title={dp}
                  >
                    {dp.split("/").pop() || dp}
                  </div>
                )}
                {groupFiles.map((f) => {
                  const fileName = f.file_path.split("/").pop() || f.file_path;
                  const isSelected = f.file_path === selectedFile;
                  return (
                    <button
                      key={f.file_path}
                      onClick={() => setSelectedFile(f.file_path)}
                      title={f.file_path}
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 6,
                        width: "max-content",
                        minWidth: "100%",
                        padding: "4px 10px",
                        border: "none",
                        background: isSelected ? colors.selectedBg : "none",
                        color: isSelected ? colors.textLight : colors.text,
                        cursor: "pointer",
                        fontSize: 12,
                        fontFamily: fonts.mono,
                        textAlign: "left",
                        whiteSpace: "nowrap",
                      }}
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
                        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                        <polyline points="14 2 14 8 20 8" />
                      </svg>
                      {fileName}
                    </button>
                  );
                })}
              </div>
            ))}
          </div>
          {/* Content viewer */}
          <div style={{ flex: 1, overflowY: "auto", padding: "12px 16px" }}>
            {selectedFile && (
              <div
                style={{
                  fontSize: 11,
                  fontFamily: fonts.mono,
                  color: colors.textDim,
                  marginBottom: 12,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  borderBottom: `1px solid ${colors.border}`,
                  paddingBottom: 8,
                }}
                title={selectedFile}
              >
                {selectedFile}
              </div>
            )}
            {!selectedFile && (
              <div style={{ color: colors.textDim, fontSize: 13 }}>Select a file</div>
            )}
            {selectedFile && contentError && (
              <div style={{ color: colors.textDim, fontSize: 13, fontStyle: "italic" }}>
                File not available on disk
              </div>
            )}
            {selectedFile && !content && !contentError && (
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
            <style>{markdownStyles}</style>
          </div>
        </div>
      )}
    </FilePanel>
  );
}
