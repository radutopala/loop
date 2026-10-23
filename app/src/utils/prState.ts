import type { PRInfo } from "../api/git";

/** GitHub's colors for a PR's state dot. */
const MERGED = "#a371f7";
const CLOSED = "#cf222e";
const OPEN = "#3fb950";

/**
 * A PR's state as GitHub shows it: "draft", "open", "merged" or "closed",
 * with its dot color. draft is the given dim color. GitLab's "opened" reads
 * as "open".
 */
export function prState(pr: PRInfo, draft: string): { label: string; dot: string } {
  if (pr.is_draft) return { label: "draft", dot: draft };
  const state = pr.state.toLowerCase();
  const label = state === "opened" ? "open" : state;
  return { label, dot: label === "merged" ? MERGED : label === "closed" ? CLOSED : OPEN };
}

/** "PR" or, for a GitLab merge request, "MR". */
export function prKind(pr: PRInfo): string {
  return pr.kind === "mr" ? "MR" : "PR";
}

/** "#12" for a PR, "!12" for a GitLab merge request. */
export function prRef(pr: PRInfo): string {
  return `${pr.kind === "mr" ? "!" : "#"}${pr.number}`;
}

/** "#12 Fix the login (open)", or "#12 (open)" without a title. */
export function prSummary(pr: PRInfo): string {
  const { label } = prState(pr, "");
  return pr.title ? `${prRef(pr)} ${pr.title} (${label})` : `${prRef(pr)} (${label})`;
}
