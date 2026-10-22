import { describe, expect, it } from "vitest";
import type { ReviewComment, ReviewSession } from "../../api/review";
import {
  allActionItems,
  buildAddressAllPrompt,
  buildAddressPrompt,
  buildCheckAllPrompt,
  buildDiscussDraft,
  isActionable,
  isBlocking,
  liveReviewRun,
  moreMenuItems,
  reviewEffortOptions,
  reviewModelOptions,
  reviewScopeChip,
  runSettingsSummary,
  skippedNote,
  startedReviewRun,
  WHY_QUESTION,
  withoutComment,
  withThreads,
  withUpdatedComment,
  withVerdictState,
} from "./ReviewPanel";

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

  it("appends the comment's transcripts, oldest first", () => {
    const c = comment("leaks the lock", { transcripts: ["/home/u/.claude/projects/-repo--worktrees-pr-7/sess-1.jsonl", "/home/u/.claude/projects/-repo--worktrees-pr-7/sess-2.jsonl"] });
    expect(buildDiscussDraft(c)).toBe(
      "> internal/api/x.go:12\n> leaks the lock\n>\n" +
        "> transcripts of the review passes behind this, oldest first:\n" +
        "> /home/u/.claude/projects/-repo--worktrees-pr-7/sess-1.jsonl\n" +
        "> /home/u/.claude/projects/-repo--worktrees-pr-7/sess-2.jsonl\n\n",
    );
  });

  it("omits the transcript block when the comment has none", () => {
    const body = "> internal/api/x.go:12\n> leaks the lock\n\n";
    expect(buildDiscussDraft(comment("leaks the lock"))).toBe(body);
    expect(buildDiscussDraft(comment("leaks the lock", { transcripts: [] }))).toBe(body);
    expect(buildDiscussDraft(comment("leaks the lock", { transcripts: [""] }))).toBe(body);
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
    expect(buildDiscussDraft(comment("leaks the lock"), WHY_QUESTION)).toBe(`> internal/api/x.go:12\n> leaks the lock\n\n${WHY_QUESTION}`);
  });

  it("keeps the question below the transcript block", () => {
    const c = comment("leaks the lock", { transcripts: ["/home/u/.claude/projects/-repo--worktrees-pr-7/sess-1.jsonl"] });
    expect(buildDiscussDraft(c, WHY_QUESTION)).toBe(
      "> internal/api/x.go:12\n> leaks the lock\n>\n" +
        "> transcripts of the review passes behind this, oldest first:\n" +
        `> /home/u/.claude/projects/-repo--worktrees-pr-7/sess-1.jsonl\n\n${WHY_QUESTION}`,
    );
  });

  it("falls back to the plain draft for an empty ask", () => {
    expect(buildDiscussDraft(comment("leaks the lock"), "")).toBe("> internal/api/x.go:12\n> leaks the lock\n\n");
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
  it("appends the comment's transcripts, oldest first", () => {
    const c = comment("leaks the lock", { transcripts: [`${dir}/sess-1.jsonl`, `${dir}/sess-2.jsonl`] });
    expect(buildAddressPrompt(c, session())).toBe(
      "Please address this review comment from the PR:\n\n" +
        "- File: `internal/api/x.go`\n" +
        "- Line: 12 (RIGHT \u2014 added/new)\n\n" +
        "Comment:\n\n" +
        "> leaks the lock\n\n" +
        "Transcripts of the review passes behind this, oldest first:\n" +
        `- ${dir}/sess-1.jsonl\n` +
        `- ${dir}/sess-2.jsonl`,
    );
  });

  it("omits the transcript block when the comment has none", () => {
    const tail = "> leaks the lock";
    expect(buildAddressPrompt(comment("leaks the lock")).endsWith(tail)).toBe(true);
    expect(buildAddressPrompt(comment("leaks the lock"), session()).endsWith(tail)).toBe(true);
    expect(buildAddressPrompt(comment("leaks the lock", { transcripts: [""] })).endsWith(tail)).toBe(true);
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

  // Each finding carries its own transcripts under its block, as Address
  // does, so the agent knows which pass explains which finding; one run's
  // transcript shared by two findings shows under both.
  it("lists each finding's transcripts under its block", () => {
    const cs = [
      comment("leaks the lock", { transcripts: [`${dir}/sess-1.jsonl`, `${dir}/dedup.jsonl`] }),
      comment("drops the error", { id: "c2", transcripts: [`${dir}/sess-1.jsonl`, `${dir}/sess-2.jsonl`] }),
      comment("off by one", { id: "c3" }),
    ];
    expect(buildAddressAllPrompt(cs, session())).toBe(
      "Please address the following 3 review comments from the PR:\n\n" +
        "---\n" +
        "### 1. `internal/api/x.go`:12 (RIGHT — added/new)\n\n" +
        "> leaks the lock\n\n" +
        "Transcripts of the review passes behind this, oldest first:\n" +
        `- ${dir}/sess-1.jsonl\n` +
        `- ${dir}/dedup.jsonl\n\n` +
        "---\n" +
        "### 2. `internal/api/x.go`:12 (RIGHT — added/new)\n\n" +
        "> drops the error\n\n" +
        "Transcripts of the review passes behind this, oldest first:\n" +
        `- ${dir}/sess-1.jsonl\n` +
        `- ${dir}/sess-2.jsonl\n\n` +
        "---\n" +
        "### 3. `internal/api/x.go`:12 (RIGHT — added/new)\n\n" +
        "> off by one",
    );
  });

  it("omits the transcript block when no finding has one", () => {
    const tail = "> leaks the lock";
    expect(buildAddressAllPrompt([comment("leaks the lock")]).endsWith(tail)).toBe(true);
    expect(buildAddressAllPrompt([comment("leaks the lock", { transcripts: [] })], session()).endsWith(tail)).toBe(true);
  });

  it("carries the author when the finding has one", () => {
    expect(buildAddressAllPrompt([comment("leaks the lock", { author: "octocat" })])).toContain("### 1. `internal/api/x.go`:12 (RIGHT — added/new) — @octocat");
  });
});

describe("buildCheckAllPrompt", () => {
  // The findings aren't quoted: the agent dedups them, then reads what is
  // left through get_review_comments, so the request names the tools and the
  // checkout to read the code in, and leaves the comments otherwise alone.
  it("names the PR, commit and checkout, then the steps", () => {
    const sess = session({ head_sha: "abc1234", worktree_path: "/repo/.worktrees/pr-7", pr: { number: 7, url: "https://github.com/o/r/pull/7", base_ref: "b", head_ref: "h", state: "open" } });
    expect(buildCheckAllPrompt(sess)).toBe(
      "Please check the review comments this review session reported on the PR.\n\n" +
        "- PR: #7\n" +
        "- Commit: abc1234\n" +
        "- Checkout: /repo/.worktrees/pr-7\n\n" +
        "1. Run the dedup_review_findings tool first, so comments that report the same issue are merged before you check them. If it fails, say why and carry on.\n" +
        "2. Read the comments left with the get_review_comments tool. Skip the ones whose source is github.\n" +
        "3. For each one, read the code it points at in the checkout above and decide whether it is a real issue, a false positive, or already fixed.\n" +
        "4. Record your verdict on each one with the update_review_comment tool: its comment_id, verdict (real, false_positive or already_fixed) and a one-sentence reason citing the line you read. Don't pass a body. Your verdict replaces the dedup pass's, so the Review panel shows yours.\n" +
        "5. Reply with one entry per comment: its id, path:line, category (when it has one), verdict and why. Link a pushed comment to https://github.com/o/r/pull/7#discussion_r<its GitHub id>. Only show a severity the comment itself states; don't rate them yourself.\n" +
        "6. End with what the dedup pass removed and how many comments got each verdict.\n\n" +
        "Don't change any code. Apart from the dedup pass and recording verdicts, don't edit, delete or push any comment.",
    );
  });

  it("drops what the session doesn't know", () => {
    expect(buildCheckAllPrompt()).toBe(
      "Please check the review comments this review session reported on the PR.\n\n" +
        "1. Run the dedup_review_findings tool first, so comments that report the same issue are merged before you check them. If it fails, say why and carry on.\n" +
        "2. Read the comments left with the get_review_comments tool. Skip the ones whose source is github.\n" +
        "3. For each one, read the code it points at and decide whether it is a real issue, a false positive, or already fixed.\n" +
        "4. Record your verdict on each one with the update_review_comment tool: its comment_id, verdict (real, false_positive or already_fixed) and a one-sentence reason citing the line you read. Don't pass a body. Your verdict replaces the dedup pass's, so the Review panel shows yours.\n" +
        "5. Reply with one entry per comment: its id, path:line, category (when it has one), verdict and why. Link a pushed comment to its GitHub thread. Only show a severity the comment itself states; don't rate them yourself.\n" +
        "6. End with what the dedup pass removed and how many comments got each verdict.\n\n" +
        "Don't change any code. Apart from the dedup pass and recording verdicts, don't edit, delete or push any comment.",
    );
  });
});

describe("isActionable", () => {
  it.each([
    ["unjudged", {}, true],
    ["real", { verdict: "real" }, true],
    ["false positive", { verdict: "false_positive" }, false],
    ["already fixed", { verdict: "already_fixed" }, false],
    ["pushed", { pushed: true, verdict: "real" }, false],
  ] as [string, Partial<ReviewComment>, boolean][])("%s", (_name, extra, want) => {
    expect(isActionable(comment("b", extra))).toBe(want);
  });
});

describe("isBlocking", () => {
  it.each([
    ["blocking", { verdict: "real", impact: "blocking" }, true],
    ["nice to have", { verdict: "real", impact: "nice_to_have" }, false],
    ["unjudged", {}, false],
    ["pushed blocking", { pushed: true, verdict: "real", impact: "blocking" }, false],
  ] as [string, Partial<ReviewComment>, boolean][])("%s", (_name, extra, want) => {
    expect(isBlocking(comment("b", extra))).toBe(want);
  });
});

describe("skippedNote", () => {
  it("names the unpushed dismissed comments by verdict", () => {
    const cs = [
      comment("a", { verdict: "false_positive" }),
      comment("b", { verdict: "already_fixed" }),
      comment("c", { verdict: "already_fixed" }),
      comment("d", { verdict: "false_positive", pushed: true }),
      comment("e", { verdict: "real" }),
      comment("f"),
    ];
    expect(skippedNote(cs)).toBe(" · skips 1 false positive, 2 already fixed");
    expect(skippedNote([comment("a", { verdict: "already_fixed" })])).toBe(" · skips 1 already fixed");
  });

  it("is empty when nothing is skipped", () => {
    expect(skippedNote([comment("a", { verdict: "real" }), comment("b", { verdict: "false_positive", pushed: true })])).toBe("");
  });
});

describe("allActionItems", () => {
  function items(newCount: number, pendingCount: number, busy = false) {
    const calls: string[] = [];
    const list = allActionItems({
      newCount,
      pendingCount,
      busy,
      onCheckAll: () => calls.push("check"),
      onAddressAll: () => calls.push("address"),
      onPushAll: () => calls.push("push"),
    });
    return { list, calls };
  }

  it("lists each action with its count", () => {
    const { list, calls } = items(3, 2);
    expect(list.map((it) => it.label)).toEqual(["Check all (3)", "Address all (2)", "Push all to GitHub (2)"]);
    for (const it of list) it.onClick();
    expect(calls).toEqual(["check", "address", "push"]);
  });

  it("leaves out actions with nothing to act on", () => {
    expect(items(0, 2).list.map((it) => it.label)).toEqual(["Address all (2)", "Push all to GitHub (2)"]);
    expect(items(1, 0).list.map((it) => it.label)).toEqual(["Check all (1)"]);
    expect(items(0, 0).list).toEqual([]);
  });

  it("says what Address all and Push all skip", () => {
    const list = allActionItems({ newCount: 12, pendingCount: 11, skipped: " · skips 1 false positive", busy: false, onCheckAll: () => {}, onAddressAll: () => {}, onPushAll: () => {} });
    expect(list.map((it) => it.label)).toEqual(["Check all (12)", "Address all (11) · skips 1 false positive", "Push all to GitHub (11) · skips 1 false positive"]);
  });

  it("adds the blocking variants when only some are blocking", () => {
    const calls: string[] = [];
    const base = {
      newCount: 0,
      pendingCount: 3,
      busy: false,
      onCheckAll: () => {},
      onAddressAll: () => {},
      onPushAll: () => {},
      onAddressBlocking: () => calls.push("address"),
      onPushBlocking: () => calls.push("push"),
    };
    const list = allActionItems({ ...base, blockingCount: 2 });
    expect(list.map((it) => it.label)).toEqual(["Address all (3)", "Address blocking only (2)", "Push all to GitHub (3)", "Push blocking to GitHub (2)"]);
    list[1]!.onClick();
    list[3]!.onClick();
    expect(calls).toEqual(["address", "push"]);
    // None blocking, or all of them: the All items already say it.
    expect(allActionItems({ ...base, blockingCount: 0 })).toHaveLength(2);
    expect(allActionItems({ ...base, blockingCount: 3 })).toHaveLength(2);
  });

  it("does nothing while another action is busy", () => {
    const { list, calls } = items(1, 1, true);
    for (const it of list) it.onClick();
    expect(calls).toEqual([]);
  });
});

describe("reviewModelOptions", () => {
  const models = ["claude-opus-5-5", "claude-opus-5-5[1m]", "claude-sonnet-5"];

  it("names the config default and lists the daemon's models", () => {
    const opts = reviewModelOptions("", "claude-opus-5-5", models);
    expect(opts.map((o) => o.value)).toEqual(["", ...models]);
    expect(opts[0]).toEqual({ value: "", label: "Default (opus-5-5)" });
    expect(opts[1]).toEqual({ value: "claude-opus-5-5", label: "opus-5-5" });
  });

  it.each([
    ["no config default", "", "", "Default model", false],
    ["keeps an id outside the models", "claude-custom-1", "", "Default model", true],
    ["model not duplicated", "claude-sonnet-5", "", "Default model", false],
  ])("%s", (_name, current, def, wantDefault, wantExtra) => {
    const opts = reviewModelOptions(current, def, models);
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

describe("withoutComment", () => {
  it("drops only the named comment", () => {
    const sess = session({ comments: [comment("a", { id: "a" }), comment("b", { id: "b" })] });
    expect(withoutComment(sess, "a")?.comments.map((c) => c.id)).toEqual(["b"]);
  });

  it("leaves the session as it was for an unknown id", () => {
    const sess = session({ comments: [comment("a", { id: "a" })] });
    expect(withoutComment(sess, "zz")?.comments).toEqual(sess.comments);
  });

  it("keeps a missing session missing", () => {
    expect(withoutComment(null, "a")).toBeNull();
  });
});

describe("withUpdatedComment", () => {
  it("swaps in the new body and keeps the rest", () => {
    const sess = session({ comments: [comment("old", { id: "a", pushed: true }), comment("b", { id: "b" })] });
    const got = withUpdatedComment(sess, { id: "a", body: "old\n\nAlso flagged: more" })?.comments;
    expect(got?.[0]).toEqual(comment("old\n\nAlso flagged: more", { id: "a", pushed: true }));
    expect(got?.[1]).toBe(sess.comments[1]);
  });

  it("keeps a missing session missing", () => {
    expect(withUpdatedComment(null, { id: "a", body: "x" })).toBeNull();
  });

  // The event leaves out empty fields: a body edit clears the verdict and
  // impact, and the event then carries neither.
  it("clears a verdict, its reason and impact the event leaves out", () => {
    const sess = session({ comments: [comment("old", { id: "a", verdict: "real", verdict_reason: "line 3", impact: "blocking" })] });
    const got = withUpdatedComment(sess, { id: "a", body: "new" })?.comments[0];
    expect(got?.verdict).toBeUndefined();
    expect(got?.verdict_reason).toBeUndefined();
    expect(got?.impact).toBeUndefined();
  });

  it("takes a new verdict and impact", () => {
    const sess = session({ comments: [comment("b", { id: "a" })] });
    const got = withUpdatedComment(sess, { id: "a", body: "b", verdict: "real", verdict_reason: "r", impact: "nice_to_have" })?.comments[0];
    expect(got).toMatchObject({ verdict: "real", verdict_reason: "r", impact: "nice_to_have" });
  });
});

describe("withVerdictState", () => {
  it("takes the threads, the reviewed and approved heads and the review's runs, and keeps the rest", () => {
    const held = session({
      status: "reviewing",
      raw_diff: "diff",
      comments: [comment("a")],
      reviewed_sha: "old",
      approved_sha: "old",
      threads: { "1": { status: "open" } },
      review_id: "r0",
      review_runs: [3],
    });
    const fresh = session({ status: "ready", comments: [], reviewed_sha: "abc", threads: { "1": { status: "addressed" } }, review_id: "r1", review_runs: [4, 5] });
    expect(withVerdictState(held, fresh)).toEqual({
      ...held,
      reviewed_sha: "abc",
      approved_sha: undefined,
      threads: { "1": { status: "addressed" } },
      review_id: "r1",
      review_runs: [4, 5],
    });
  });

  it("keeps a missing session missing", () => {
    expect(withVerdictState(null, session())).toBeNull();
  });
});

describe("withThreads", () => {
  const root = comment("root", { id: "r", source: "github", github_id: 1 });
  const reply = comment("reply", { id: "re", source: "github", github_id: 2, reply_to: 1 });
  const agent = comment("agent", { id: "ag", github_id: 1 });

  it("hands each GitHub thread start its assessment", () => {
    const got = withThreads([root, reply, agent], { "1": { status: "open", impact: "blocking", reason: "r" }, "2": { status: "addressed" } });
    expect(got[0]).toEqual({ ...root, thread: { status: "open", impact: "blocking", reason: "r" } });
    expect(got[1]).toBe(reply);
    expect(got[2]).toBe(agent);
  });

  it("leaves the comments as they are without assessments", () => {
    const cs = [root, reply];
    expect(withThreads(cs)).toBe(cs);
    expect(withThreads(cs, {})[0]).toBe(root);
  });
});

describe("moreMenuItems", () => {
  function items(extra: Partial<Parameters<typeof moreMenuItems>[0]> = {}) {
    const calls: string[] = [];
    const list = moreMenuItems({
      syncDisabled: false,
      all: [{ label: "Check all (1)", onClick: () => calls.push("check") }],
      approveAnyway: false,
      showStats: false,
      closeDisabled: false,
      onSync: () => calls.push("sync"),
      onApproveAnyway: () => calls.push("approve"),
      onToggleStats: () => calls.push("stats"),
      onClose: () => calls.push("close"),
      ...extra,
    });
    return { list, calls };
  }

  it("lists Sync, the All actions, Stats and Close", () => {
    const { list, calls } = items();
    expect(list.map((it) => (it.separator ? `--- ${it.label}` : it.label))).toEqual(["Sync with GitHub", "Check all (1)", "--- Stats", "Close review"]);
    expect(list[3]?.danger).toBe(true);
    for (const it of list) it.onClick();
    expect(calls).toEqual(["sync", "check", "stats", "close"]);
  });

  it("offers Approve anyway when asked", () => {
    const { list, calls } = items({ approveAnyway: true });
    const it = list.find((x) => x.label === "Approve anyway…");
    it?.onClick();
    expect(calls).toEqual(["approve"]);
  });

  it("leaves out what can't run, and the separator with nothing above it", () => {
    const { list } = items({ syncDisabled: true, all: [], closeDisabled: true, showStats: true });
    expect(list.map((it) => it.label)).toEqual(["Back to the review"]);
    expect(list[0]?.separator).toBe(false);
  });
});

describe("runSettingsSummary", () => {
  it.each([
    [{ maxIter: 1, model: "", effort: "", forkMode: "", scope: "" }, "1 iteration, default model, default effort, fresh session, Auto scope"],
    [{ maxIter: 3, model: "claude-opus-5-5", effort: "high", forkMode: "current", scope: "full" }, "3 iterations, opus-5-5, high, fork chat session, Whole PR"],
    [{ maxIter: 2, model: "", effort: "", forkMode: "custom", scope: "incremental" }, "2 iterations, default model, default effort, fork session id, Changes since last review"],
  ] as const)("%o", (a, want) => {
    expect(runSettingsSummary(a)).toBe(want);
  });
});

describe("liveReviewRun", () => {
  it("finds the channel's running or paused review run, with its mode", () => {
    expect(
      liveReviewRun([
        { id: "w1", workflow_name: "deploy", status: "running" },
        { id: "w2", workflow_name: "review-loop", status: "completed" },
        { id: "w3", workflow_name: "review-fix-loop", status: "paused" },
      ]),
    ).toEqual({ runId: "w3", mode: "review-fix" });
    expect(liveReviewRun([{ id: "w4", workflow_name: "review-loop", status: "running" }])).toEqual({ runId: "w4", mode: "review-only" });
  });

  it("is null when no review is live", () => {
    expect(liveReviewRun([{ id: "w2", workflow_name: "review-loop", status: "failed" }])).toBeNull();
    expect(liveReviewRun([])).toBeNull();
  });
});

describe("startedReviewRun", () => {
  const started = (data: unknown, type = "workflow.run_started") => ({ type, data }) as Parameters<typeof startedReviewRun>[0];

  it("picks up a review run starting in this channel", () => {
    expect(startedReviewRun(started({ run_id: "r1", workflow_name: "review-loop", channel_id: "ch1" }), "ch1")).toEqual({ runId: "r1", mode: "review-only" });
    expect(startedReviewRun(started({ run_id: "r2", workflow_name: "review-fix-loop", channel_id: "ch1" }), "ch1")).toEqual({ runId: "r2", mode: "review-fix" });
  });

  it("ignores other events, channels and workflows", () => {
    expect(startedReviewRun(started({ run_id: "r1", workflow_name: "review-loop", channel_id: "ch1" }, "workflow.run_completed"), "ch1")).toBeNull();
    expect(startedReviewRun(started({ run_id: "r1", workflow_name: "review-loop", channel_id: "ch2" }), "ch1")).toBeNull();
    expect(startedReviewRun(started({ run_id: "r1", workflow_name: "deploy", channel_id: "ch1" }), "ch1")).toBeNull();
    expect(startedReviewRun(started({ workflow_name: "review-loop", channel_id: "ch1" }), "ch1")).toBeNull();
    expect(startedReviewRun(started(undefined), "ch1")).toBeNull();
  });
});

describe("reviewScopeChip", () => {
  it("names the commit an incremental run started at", () => {
    expect(reviewScopeChip(session({ last_run_scope: "incremental", last_run_from_sha: "abc1234def5678" }))).toBe("since abc1234");
  });

  it("says a full run reviewed the whole PR", () => {
    expect(reviewScopeChip(session({ last_run_scope: "full" }))).toBe("whole PR");
  });

  it("is empty before any run", () => {
    expect(reviewScopeChip(null)).toBe("");
    expect(reviewScopeChip(session())).toBe("");
    expect(reviewScopeChip(session({ last_run_scope: "incremental" }))).toBe("");
  });
});
