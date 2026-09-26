import { useCallback, useEffect, useState } from "react";
import type { CommitEntry, DiffResponse } from "../../api/loopApi";
import { fetchCommitDiff } from "../../api/loopApi";
import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { Chevron } from "../shared/Chevron";
import type { ParsedFile } from "./DiffViewer";
import { DiffViewer, fileKey, parseUnifiedDiff } from "./DiffViewer";

interface CommitDiffViewProps {
  channelId: string;
  commit: CommitEntry;
  rootIndex: number;
  onBack: () => void;
  onFileContextMenu: (e: React.MouseEvent, path: string) => void;
}

// CommitDiffView shows what one commit changed: its message on top, the
// files it touched below in the same DiffViewer the other git tabs use. A
// commit never changes, so it is fetched once — no polling.
export function CommitDiffView({ channelId, commit, rootIndex, onBack, onFileContextMenu }: CommitDiffViewProps) {
  const { colors } = useTheme();
  const [data, setData] = useState<DiffResponse | null>(null);
  const [parsedFiles, setParsedFiles] = useState<ParsedFile[]>([]);
  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [bodyOpen, setBodyOpen] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetchCommitDiff(channelId, commit.hash, rootIndex)
      .then((d) => {
        if (cancelled) return;
        setData(d);
        setParsedFiles(parseUnifiedDiff(d.diff));
        // A commit is usually small and was opened to be read — start expanded.
        setExpandedFiles(new Set(d.files.map(fileKey)));
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message);
      });
    return () => {
      cancelled = true;
    };
  }, [channelId, commit.hash, rootIndex]);

  const toggleFile = useCallback((key: string) => {
    setExpandedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);
  const expandAll = useCallback(() => setExpandedFiles(new Set((data?.files ?? []).map(fileKey))), [data]);
  const collapseAll = useCallback(() => setExpandedFiles(new Set()), []);

  const hasBody = !!commit.body && commit.body.trim() !== "";
  const files = data?.files ?? [];

  return (
    <div data-testid="commit-diff" style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ padding: "6px 12px 8px", borderBottom: `1px solid ${colors.border}`, fontSize: 12 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <button
            data-testid="commit-diff-back"
            onClick={onBack}
            title="Back to commits"
            style={{ display: "flex", alignItems: "center", gap: 4, background: "none", border: "none", padding: 0, color: colors.textDim, cursor: "pointer", fontSize: 11, flexShrink: 0 }}
            onMouseEnter={(e) => {
              e.currentTarget.style.color = colors.textLight;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.color = colors.textDim;
            }}
          >
            <Chevron deg={180} />
            Commits
          </button>
          <span data-testid="commit-diff-hash" style={{ fontFamily: fonts.mono, fontSize: 11, color: colors.active, flexShrink: 0 }} title={commit.hash}>
            {commit.short}
          </span>
          <span style={{ flex: 1 }} />
          {data && (
            <span style={{ fontFamily: fonts.mono, fontSize: 11, flexShrink: 0 }}>
              <span style={{ color: colors.textDim }}>{files.length} </span>
              <span style={{ color: colors.diffAddText }}>+{data.total_additions}</span> <span style={{ color: colors.diffDelText }}>-{data.total_deletions}</span>
            </span>
          )}
        </div>
        <div
          data-testid="commit-diff-subject"
          onClick={hasBody ? () => setBodyOpen((v) => !v) : undefined}
          title={hasBody ? (bodyOpen ? "Collapse message" : "Expand full message") : undefined}
          style={{ display: "flex", alignItems: "baseline", gap: 6, marginTop: 4, color: colors.textLight, cursor: hasBody ? "pointer" : "default" }}
        >
          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: bodyOpen ? "normal" : "nowrap" }}>{commit.subject}</span>
          {hasBody && <span style={{ color: colors.textDim, fontSize: 10, flexShrink: 0, fontFamily: fonts.mono }}>{bodyOpen ? "▾" : "▸"}</span>}
        </div>
        <div style={{ display: "flex", gap: 8, fontSize: 11, color: colors.textDim, marginTop: 2 }}>
          <span>{commit.author}</span>
          <span>{new Date(commit.date).toLocaleString()}</span>
        </div>
        {hasBody && bodyOpen && (
          <pre
            style={{
              margin: "6px 0 0",
              padding: "8px 10px",
              background: colors.bg,
              border: `1px solid ${colors.border}`,
              borderRadius: 4,
              fontFamily: fonts.mono,
              fontSize: 11,
              color: colors.textLight,
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
              maxHeight: 200,
              overflow: "auto",
            }}
          >
            {commit.body!.trim()}
          </pre>
        )}
      </div>
      {error ? (
        <div style={{ padding: "20px 12px", color: colors.error, fontSize: 13 }}>{error}</div>
      ) : (
        <DiffViewer
          key={commit.hash}
          channelId={channelId}
          files={files}
          parsedFiles={parsedFiles}
          expandedFiles={expandedFiles}
          loading={data === null}
          hasData={data !== null}
          totalFiles={files.length}
          onToggleFile={toggleFile}
          onExpandAll={expandAll}
          onCollapseAll={collapseAll}
          onFileContextMenu={onFileContextMenu}
          rootIndex={rootIndex}
          fileRef={commit.hash}
        />
      )}
    </div>
  );
}
