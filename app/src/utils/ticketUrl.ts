/** The backend's cap, in characters. */
export const MAX_TICKET_URL_LENGTH = 2048;

/**
 * Why the backend would reject value as a ticket URL, or null when it takes
 * it. Same rules: trimmed, empty clears, otherwise an absolute http(s) URL.
 */
export function ticketURLError(value: string): string | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  if (trimmed.length > MAX_TICKET_URL_LENGTH) return `Longer than ${MAX_TICKET_URL_LENGTH} characters`;
  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    return "Must be an absolute http(s) URL";
  }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || !url.hostname) return "Must be an absolute http(s) URL";
  return null;
}

/** An issue key as Jira, Linear, YouTrack and friends spell it: PROJ-123. */
const ISSUE_KEY = /\b[A-Z][A-Z0-9_]*-\d+\b/;

/**
 * A short label for a ticket URL: the issue key when the URL carries one
 * (PROJ-123, including Jira's ?selectedIssue=), owner/repo#7 for GitHub
 * issues and PRs, project!12 or project#12 for GitLab merge requests and
 * issues, otherwise the last path segment, or the host.
 */
export function ticketKey(ticketURL: string): string {
  let url: URL;
  try {
    url = new URL(ticketURL.trim());
  } catch {
    return ticketURL.trim();
  }
  const path = url.pathname;
  const key = ISSUE_KEY.exec(url.searchParams.get("selectedIssue") ?? "") ?? ISSUE_KEY.exec(path);
  if (key) return key[0];
  const segments = path.split("/").filter(Boolean);
  if (url.hostname === "github.com" && segments.length >= 4 && (segments[2] === "issues" || segments[2] === "pull")) {
    return `${segments[0]}/${segments[1]}#${segments[3]}`;
  }
  const dash = segments.indexOf("-");
  if (dash > 0 && segments.length > dash + 2 && (segments[dash + 1] === "issues" || segments[dash + 1] === "merge_requests")) {
    return `${segments[dash - 1]}${segments[dash + 1] === "issues" ? "#" : "!"}${segments[dash + 2]}`;
  }
  return segments.at(-1) ?? url.hostname;
}
