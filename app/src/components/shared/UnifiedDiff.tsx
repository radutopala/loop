import { useTheme } from "../../ThemeContext";
import { fonts } from "../../theme";
import { classifyDiffLines } from "../../utils/diffLines";

/** A unified diff, its added, removed and hunk lines coloured. */
export function UnifiedDiff({ diff, testId, style }: { diff: string; testId?: string; style?: React.CSSProperties }) {
  const { colors } = useTheme();
  return (
    <pre
      data-testid={testId}
      style={{
        fontSize: 12,
        fontFamily: fonts.mono,
        margin: "0 0 10px",
        padding: "6px 0",
        borderRadius: 6,
        backgroundColor: colors.codeBlockBg,
        color: colors.textDim,
        maxHeight: 320,
        overflow: "auto",
        ...style,
      }}
    >
      {classifyDiffLines(diff).map((line, i) => (
        <div
          key={i}
          style={{
            padding: "0 10px",
            whiteSpace: "pre",
            color: line.kind === "add" ? colors.diffAddText : line.kind === "del" ? colors.diffDelText : line.kind === "context" ? colors.text : colors.textDim,
            backgroundColor: line.kind === "add" ? colors.diffAddBg : line.kind === "del" ? colors.diffDelBg : line.kind === "hunk" ? colors.diffHunkBg : undefined,
          }}
        >
          {line.text || " "}
        </div>
      ))}
    </pre>
  );
}
