import { describe, expect, it } from "vitest";
import type { ReviewComment, ReviewSession } from "../../api/review";
import { buildAddressAllPrompt, buildAddressPrompt, buildDiscussDraft, reviewEffortOptions, reviewModelOptions, WHY_QUESTION } from "./ReviewPanel";

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

  // The question is sent verbatim, so its wording is part of the contract.
  it("sends a question that reads as an instruction on its own", () => {
    expect(WHY_QUESTION).toBe("Please explain why we need this.");
  });
});

describe("buildAddressPrompt", () => {
  const dir = "/home/u/.claude/projects/-repo--worktrees-pr-7";

  it("leads with the instruction, then the metadata, then the finding", () => {
    expect(buildAddressPrompt(comment("leaks the lock"))).toBe(
      "Please address this review comment from the PR:\n\n" + "- File: `internal/api/x.go`\n" + "- Line: 12 (RIGHT \u2014 added/new)\n\n" + "Comment:\n\n" + "> leaks the lock",
    );
  });

  it("adds the PR, commit and author when the session knows them", () => {
    const sess = session({ head_sha: "abc1234", pr: { number: 7, title: "t", url: "u", head_ref: "h", base_ref: "b", state: "open" } });
    expect(buildAddressPrompt(comment("leaks the lock", { author: "octocat" }), sess)).toBe(
      "Please address this review comment from the PR:\n\n" +
        "- File: `internal/api/x.go`\n" +
        "- Line: 12 (RIGHT \u2014 added/new)\n" +
        "- PR: #7\n" +
        "- Commit: abc1234\n" +
        "- Author: @octocat\n\n" +
        "Comment:\n\n" +
        "> leaks the lock",
    );
  });

  it("labels a comment on the old side of the diff", () => {
    expect(buildAddressPrompt(comment("was load-bearing", { side: "LEFT" }))).toContain("- Line: 12 (LEFT \u2014 deleted/old)");
  });

  // Address replaced "Push to chat", which sent this metadata without the
  // transcripts. Carrying both is the whole reason the two buttons collapsed
  // into one: the agent gets the location and the reasoning behind the finding.
  it("appends one transcript path per run, oldest first", () => {
    const sess = session({ transcript_dir: dir, run_session_ids: ["sess-1", "sess-2"] });
    expect(buildAddressPrompt(comment("leaks the lock"), sess)).toBe(
      "Please address this review comment from the PR:\n\n" +
        "- File: `internal/api/x.go`\n" +
        "- Line: 12 (RIGHT \u2014 added/new)\n\n" +
        "Comment:\n\n" +
        "> leaks the lock\n\n" +
        "Transcripts of the review runs that produced this, oldest first:\n" +
        `- ${dir}/sess-1.jsonl\n` +
        `- ${dir}/sess-2.jsonl`,
    );
  });

  // Same rule as the Discuss draft: half an address is worse than none.
  it("omits the transcript block unless both a dir and an id are known", () => {
    const tail = "> leaks the lock";
    expect(buildAddressPrompt(comment("leaks the lock")).endsWith(tail)).toBe(true);
    expect(buildAddressPrompt(comment("leaks the lock"), session()).endsWith(tail)).toBe(true);
    expect(buildAddressPrompt(comment("leaks the lock"), session({ run_session_ids: ["sess-1"] })).endsWith(tail)).toBe(true);
    expect(buildAddressPrompt(comment("leaks the lock"), session({ transcript_dir: dir })).endsWith(tail)).toBe(true);
    expect(buildAddressPrompt(comment("leaks the lock"), session({ transcript_dir: dir, run_session_ids: [""] })).endsWith(tail)).toBe(true);
  });

  it("quotes every line of a multi-paragraph finding", () => {
    expect(buildAddressPrompt(comment("leaks the lock\n\nWhen Foo returns err the mutex stays held."))).toContain("> leaks the lock\n> \n> When Foo returns err the mutex stays held.");
  });
});

describe("buildAddressAllPrompt", () => {
  const dir = "/home/u/.claude/projects/-repo--worktrees-pr-7";

  // "Address all" is Address over every pending finding, so the two have
  // to agree on what the agent is told: same instruction, same metadata, same
  // transcripts. The only difference is that findings become numbered blocks.
  it("states the count, then the metadata, then one block per finding", () => {
    const sess = session({ head_sha: "abc1234", pr: { number: 7, url: "u", base_ref: "b", head_ref: "h", state: "open" } });
    expect(buildAddressAllPrompt([comment("leaks the lock"), comment("drops the error", { id: "c2", path: "b.go", line: 3 })], sess)).toBe(
      "Please address the following 2 review comments from the PR:\n\n" +
        "- PR: #7\n" +
        "- Commit: abc1234\n\n" +
        "---\n" +
        "### 1. `internal/api/x.go`:12 (RIGHT — added/new)\n\n" +
        "> leaks the lock\n\n" +
        "---\n" +
        "### 2. `b.go`:3 (RIGHT — added/new)\n\n" +
        "> drops the error",
    );
  });

  it("says comment, singular, for one finding", () => {
    expect(buildAddressAllPrompt([comment("leaks the lock")])).toContain("the following 1 review comment from the PR:");
  });

  // Session-level, not finding-level: the runs produced the whole batch, so
  // the paths are listed once at the end rather than repeated under each block.
  it("lists the run transcripts once, after the last finding", () => {
    const sess = session({ transcript_dir: dir, run_session_ids: ["sess-1", "sess-2"] });
    expect(buildAddressAllPrompt([comment("leaks the lock"), comment("drops the error", { id: "c2" })], sess)).toBe(
      "Please address the following 2 review comments from the PR:\n\n" +
        "---\n" +
        "### 1. `internal/api/x.go`:12 (RIGHT — added/new)\n\n" +
        "> leaks the lock\n\n" +
        "---\n" +
        "### 2. `internal/api/x.go`:12 (RIGHT — added/new)\n\n" +
        "> drops the error\n\n" +
        "---\n\n" +
        "Transcripts of the review runs that produced these, oldest first:\n" +
        `- ${dir}/sess-1.jsonl\n` +
        `- ${dir}/sess-2.jsonl`,
    );
  });

  it("omits the transcript block unless both a dir and an id are known", () => {
    const tail = "> leaks the lock";
    expect(buildAddressAllPrompt([comment("leaks the lock")]).endsWith(tail)).toBe(true);
    expect(buildAddressAllPrompt([comment("leaks the lock")], session({ transcript_dir: dir })).endsWith(tail)).toBe(true);
    expect(buildAddressAllPrompt([comment("leaks the lock")], session({ run_session_ids: ["sess-1"] })).endsWith(tail)).toBe(true);
  });

  it("carries the author when the finding has one", () => {
    expect(buildAddressAllPrompt([comment("leaks the lock", { author: "octocat" })])).toContain("### 1. `internal/api/x.go`:12 (RIGHT — added/new) — @octocat");
  });
});

describe("reviewModelOptions", () => {
  it("names the config default and lists the presets", () => {
    const opts = reviewModelOptions("", "claude-opus-5-5");
    expect(opts[0]).toEqual({ value: "", label: "Default (opus-5-5)" });
    expect(opts[1]).toEqual({ value: "claude-opus-5-5", label: "opus-5-5" });
  });

  it.each([
    ["no config default", "", "", "Default model", false],
    ["keeps an id outside the presets", "claude-custom-1", "", "Default model", true],
    ["preset not duplicated", "claude-sonnet-5", "", "Default model", false],
  ])("%s", (_name, current, def, wantDefault, wantExtra) => {
    const opts = reviewModelOptions(current, def);
    expect(opts[0]?.label).toBe(wantDefault);
    expect(opts.filter((o) => o.value === current && current !== "").length).toBe(current ? 1 : 0);
    expect(opts.some((o) => o.value === "claude-custom-1")).toBe(wantExtra);
  });
});

describe("reviewEffortOptions", () => {
  it("marks high, xhigh and max as recommended", () => {
    expect(reviewEffortOptions("").map((o) => o.label)).toEqual(["Default effort", "low", "medium", "high (recommended)", "xhigh (recommended)", "max (recommended)"]);
  });

  it.each([
    ["medium", "Default (medium)"],
    ["xhigh", "Default (xhigh)"],
  ])("names the config default %s", (def, want) => {
    expect(reviewEffortOptions(def)[0]?.label).toBe(want);
  });
});
