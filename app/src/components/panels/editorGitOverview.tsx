import { useMemo } from "react";
import { useTheme } from "../../ThemeContext";
import { gitGutterColors, gitOverviewMarks, hasGitLineChanges, type GitLineChanges } from "./editorGitGutter";

interface GitChangeOverviewProps {
  changes: GitLineChanges;
  totalLines: number;
  /** Jump the editor to a 1-based line when a mark (or the track) is clicked. */
  onJumpToLine: (line: number) => void;
}

/**
 * GoLand-style right-side VCS overview ruler: a thin full-height strip beside
 * the editor that maps every changed/added/deleted region onto the whole
 * document, giving a bird's-eye view of all changes. Clicking jumps the editor
 * to the corresponding line. Hidden when the file has no uncommitted changes.
 */
export function GitChangeOverview({ changes, totalLines, onJumpToLine }: GitChangeOverviewProps) {
  const { colors } = useTheme();
  const palette = gitGutterColors(colors.isDark);

  const marks = useMemo(() => gitOverviewMarks(changes, totalLines), [changes, totalLines]);

  if (totalLines <= 0 || !hasGitLineChanges(changes)) return null;

  const colorFor = (kind: "added" | "modified" | "deleted") =>
    kind === "added" ? palette.added : kind === "modified" ? palette.modified : palette.deleted;

  return (
    <div
      data-testid="git-overview-ruler"
      title="Changed lines — click to jump"
      onClick={(e) => {
        const rect = e.currentTarget.getBoundingClientRect();
        if (rect.height <= 0) return;
        const frac = (e.clientY - rect.top) / rect.height;
        onJumpToLine(Math.max(1, Math.min(totalLines, Math.round(frac * totalLines))));
      }}
      style={{
        width: 10,
        flexShrink: 0,
        position: "relative",
        cursor: "pointer",
        backgroundColor: colors.sidebar,
        borderLeft: `1px solid ${colors.border}`,
      }}
    >
      {marks.map((m, i) => (
        <div
          key={i}
          style={{
            position: "absolute",
            top: `${m.topFrac * 100}%`,
            left: 1,
            right: 1,
            height: m.kind === "deleted" ? 2 : `${m.heightFrac * 100}%`,
            minHeight: 2,
            backgroundColor: colorFor(m.kind),
            borderRadius: 1,
          }}
        />
      ))}
    </div>
  );
}
