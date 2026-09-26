import { describe, expect, it } from "vitest";
import { MAX_TICKET_URL_LENGTH, ticketKey, ticketURLError } from "./ticketUrl";

describe("ticketURLError", () => {
  it.each([
    ["empty clears", "", null],
    ["blank clears", "   ", null],
    ["jira issue", "https://example.atlassian.net/browse/PROJ-123", null],
    ["http with surrounding space", "  http://tracker.example/42  ", null],
    ["bare key", "PROJ-123", "Must be an absolute http(s) URL"],
    ["other scheme", "ftp://example.com/x", "Must be an absolute http(s) URL"],
    ["javascript", "javascript:alert(1)", "Must be an absolute http(s) URL"],
    ["too long", `https://example.com/${"a".repeat(MAX_TICKET_URL_LENGTH)}`, `Longer than ${MAX_TICKET_URL_LENGTH} characters`],
  ])("%s", (_name, value, want) => {
    expect(ticketURLError(value)).toBe(want);
  });
});

describe("ticketKey", () => {
  it.each([
    ["jira browse", "https://example.atlassian.net/browse/PROJ-123", "PROJ-123"],
    ["jira board selectedIssue", "https://example.atlassian.net/jira/software/projects/PROJ/boards/1?selectedIssue=PROJ-9", "PROJ-9"],
    ["linear", "https://linear.app/acme/issue/ENG-42/fix-login", "ENG-42"],
    ["github issue", "https://github.com/o/r/issues/7", "o/r#7"],
    ["github pr", "https://github.com/o/r/pull/12/files", "o/r#12"],
    ["gitlab merge request", "https://gitlab.com/group/proj/-/merge_requests/5", "proj!5"],
    ["gitlab issue", "https://gitlab.com/group/proj/-/issues/3", "proj#3"],
    ["other tracker", "https://tracker.example/tickets/4711/", "4711"],
    ["malformed escape", "https://tracker.example/t/%E0%A4%A", "%E0%A4%A"],
    ["bare host", "https://tracker.example/", "tracker.example"],
    ["not a url", "PROJ-1 notes", "PROJ-1 notes"],
  ])("%s", (_name, url, want) => {
    expect(ticketKey(url)).toBe(want);
  });
});
