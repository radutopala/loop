import { describe, expect, it } from "vitest";
import type { ReviewComment, ReviewSession } from "../../api/review";
import { ADDRESS_REQUEST, buildDiscussDraft, WHY_QUESTION } from "./ReviewPanel";

function comment(body: string, extra: Partial<ReviewComment> = {}): ReviewComment {
  return { id: "c1", path: "internal/api/x.go", line: 12, side: "RIGHT", body, pushed: false, ...extra };
}

function session(extra: Partial<ReviewSession> = {}): ReviewSession {
  return { channel_id: "ch1", comments: [], status: "ready", updated_at: "", ...extra };
}

describe("buildDiscussDraft", () => {
  it("quotes the body under a path:line locator", () => {
    expect(buildDiscussDraft(comment("leaks the lock on the error path"))).toBe("> internal/api/x.go:12\n> leaks the lock on the error path\n\n");
  });

  // Agent findings are summary + blank line + failure scenario, so the blank
  // line has to be quoted too — an unquoted one would close the blockquote
  // and leave the scenario rendering as body text.
  it("quotes every line of a multi-paragraph finding, blank lines included", () => {
    expect(buildDiscussDraft(comment("leaks the lock\n\nWhen Foo returns err the mutex stays held."))).toBe(
      "> internal/api/x.go:12\n> leaks the lock\n>\n> When Foo returns err the mutex stays held.\n\n",
    );
  });

  it("still produces a usable locator for an empty body", () => {
    expect(buildDiscussDraft(comment(""))).toBe("> internal/api/x.go:12\n>\n\n");
  });

  it("appends one transcript path per run, oldest first", () => {
    const sess = session({ transcript_dir: "/home/u/.claude/projects/-repo--worktrees-pr-7", run_session_ids: ["sess-1", "sess-2"] });
    expect(buildDiscussDraft(comment("leaks the lock"), sess)).toBe(
      "> internal/api/x.go:12\n> leaks the lock\n>\n" +
        "> transcripts of the review runs that produced this, oldest first:\n" +
        "> /home/u/.claude/projects/-repo--worktrees-pr-7/sess-1.jsonl\n" +
        "> /home/u/.claude/projects/-repo--worktrees-pr-7/sess-2.jsonl\n\n",
    );
  });

  // Half an address is worse than none: an id without its directory is not a
  // path the agent can open, and a directory with no ids points at nothing.
  it("omits the transcript block unless both a dir and an id are known", () => {
    const body = "> internal/api/x.go:12\n> leaks the lock\n\n";
    const dir = "/home/u/.claude/projects/-repo--worktrees-pr-7";
    expect(buildDiscussDraft(comment("leaks the lock"))).toBe(body);
    expect(buildDiscussDraft(comment("leaks the lock"), session())).toBe(body);
    expect(buildDiscussDraft(comment("leaks the lock"), session({ run_session_ids: ["sess-1"] }))).toBe(body);
    expect(buildDiscussDraft(comment("leaks the lock"), session({ transcript_dir: dir }))).toBe(body);
    expect(buildDiscussDraft(comment("leaks the lock"), session({ transcript_dir: dir, run_session_ids: [""] }))).toBe(body);
  });

  // The draft always ends with a blank line so the caret lands below the
  // quote rather than inside it.
  it("ends with a blank line", () => {
    expect(buildDiscussDraft(comment("x", { path: "a.go", line: 1 }))).toMatch(/\n\n$/);
  });

  // "Why?" sends this text as-is, so the question has to read as a question to
  // the agent on its own: it lands in the blank line Discuss leaves for the
  // user, below the quote rather than inside it.
  it("types the question into the blank line when one is given", () => {
    expect(buildDiscussDraft(comment("leaks the lock"), null, WHY_QUESTION)).toBe(`> internal/api/x.go:12\n> leaks the lock\n\n${WHY_QUESTION}`);
  });

  it("keeps the question below the transcript block", () => {
    const sess = session({ transcript_dir: "/home/u/.claude/projects/-repo--worktrees-pr-7", run_session_ids: ["sess-1"] });
    expect(buildDiscussDraft(comment("leaks the lock"), sess, WHY_QUESTION)).toBe(
      "> internal/api/x.go:12\n> leaks the lock\n>\n" +
        "> transcripts of the review runs that produced this, oldest first:\n" +
        `> /home/u/.claude/projects/-repo--worktrees-pr-7/sess-1.jsonl\n\n${WHY_QUESTION}`,
    );
  });

  it("falls back to the plain draft for an empty ask", () => {
    expect(buildDiscussDraft(comment("leaks the lock"), null, "")).toBe("> internal/api/x.go:12\n> leaks the lock\n\n");
  });

  // Address and Why? differ by their ask and nothing else, which is the point:
  // one builder means the quote, the transcripts and the spacing can't drift
  // apart between the two buttons.
  it("builds the same draft for either canned ask", () => {
    const sess = session({ transcript_dir: "/home/u/.claude/projects/-repo--worktrees-pr-7", run_session_ids: ["sess-1"] });
    const why = buildDiscussDraft(comment("leaks the lock"), sess, WHY_QUESTION);
    const address = buildDiscussDraft(comment("leaks the lock"), sess, ADDRESS_REQUEST);
    expect(address).toBe(why.replace(WHY_QUESTION, ADDRESS_REQUEST));
    expect(address.endsWith(`\n\n${ADDRESS_REQUEST}`)).toBe(true);
  });

  // The asks are sent verbatim, so their wording is part of the contract: one
  // asks for an explanation, the other for a change.
  it("sends asks that read as instructions on their own", () => {
    expect(WHY_QUESTION).toBe("Please explain why we need this.");
    expect(ADDRESS_REQUEST).toBe("Please address this with a fix.");
  });
});
