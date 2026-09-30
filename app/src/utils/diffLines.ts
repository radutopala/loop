export type DiffLineKind = "add" | "del" | "hunk" | "meta" | "context";

/** Classifies each line of a unified diff for colouring. */
export function classifyDiffLines(diff: string): { kind: DiffLineKind; text: string }[] {
  const lines = diff.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  return lines.map((text) => {
    if (text.startsWith("+++") || text.startsWith("---")) return { kind: "meta", text };
    if (text.startsWith("@@")) return { kind: "hunk", text };
    if (text.startsWith("+")) return { kind: "add", text };
    if (text.startsWith("-")) return { kind: "del", text };
    return { kind: "context", text };
  });
}
