import { describe, expect, it } from "vitest";
import type { ReviewComment, ReviewOutcome, ReviewSession } from "../../api/review";
import type { ColorPalette } from "../../theme";
import { approveBody, approveDisabledReason, canApprove, deriveOutcome, impactLabel, isApprovedAtHead, outcomeColor, outcomeLabel, outcomeTitle } from "./reviewOutcome";

function comment(id: string, extra: Partial<ReviewComment> = {}): ReviewComment {
  return { id, path: "internal/api/x.go", line: 12, side: "RIGHT", body: "b", pushed: false, source: "agent", ...extra };
}

function session(extra: Partial<ReviewSession> = {}): ReviewSession {
  return { channel_id: "ch1", comments: [], status: "ready", updated_at: "", ...extra };
}

function outcome(extra: Partial<ReviewOutcome> = {}): ReviewOutcome {
  return { state: "approve", blocking: [], nits: [], pending: [], ...extra };
}

// The cases mirror the daemon's review.DeriveOutcome tests.
describe("deriveOutcome", () => {
  const nit = comment("nit", { verdict: "real", impact: "nice_to_have" });
  const block = comment("block", { verdict: "real", impact: "blocking" });

  it.each<[string, Partial<ReviewSession>, ReviewOutcome]>([
    ["nothing left on the reviewed head", { head_sha: "abc", reviewed_sha: "abc" }, outcome({ reviewed_sha: "abc" })],
    [
      "dismissed, resolved, replies and unsynced comments don't count",
      {
        head_sha: "abc",
        reviewed_sha: "abc",
        comments: [
          comment("fp", { verdict: "false_positive" }),
          comment("fixed", { verdict: "already_fixed" }),
          comment("done", { verdict: "real", impact: "blocking", pushed: true, resolved: true }),
          comment("reply", { source: "github", github_id: 2, reply_to: 1 }),
          comment("unsynced", { source: "github" }),
        ],
      },
      outcome({ reviewed_sha: "abc" }),
    ],
    ["only nits", { head_sha: "abc", reviewed_sha: "abc", comments: [nit] }, outcome({ state: "approve_with_nits", nits: ["nit"], reviewed_sha: "abc" })],
    [
      "a blocking finding wins over an unreviewed head",
      { head_sha: "abd", reviewed_sha: "abc", comments: [nit, block] },
      outcome({ state: "request_changes", blocking: ["block"], nits: ["nit"], reviewed_sha: "abc" }),
    ],
    ["never reviewed", { head_sha: "abc" }, outcome({ state: "unknown", reason: "the head commit isn't reviewed yet" })],
    [
      "reviewed at an older head",
      { head_sha: "abd", reviewed_sha: "abc", comments: [nit] },
      outcome({ state: "unknown", nits: ["nit"], reason: "the head commit isn't reviewed yet", reviewed_sha: "abc" }),
    ],
    [
      "a real finding without an impact, and an unjudged one, are pending",
      { head_sha: "abc", reviewed_sha: "abc", comments: [comment("real", { verdict: "real" }), comment("new")] },
      outcome({ state: "unknown", pending: ["real", "new"], reason: "2 comment(s) not judged yet", reviewed_sha: "abc" }),
    ],
    [
      "an impact on a comment without verdict real doesn't count",
      { head_sha: "abc", reviewed_sha: "abc", comments: [comment("stale", { impact: "blocking" })] },
      outcome({ state: "unknown", pending: ["stale"], reason: "1 comment(s) not judged yet", reviewed_sha: "abc" }),
    ],
    [
      "github threads count by their assessment",
      {
        head_sha: "abc",
        reviewed_sha: "abc",
        comments: [
          comment("gh-1", { source: "github", github_id: 1 }),
          comment("gh-2", { source: "github", github_id: 2 }),
          comment("gh-3", { source: "github", github_id: 3 }),
          comment("gh-4", { source: "github", github_id: 4 }),
        ],
        threads: { "1": { status: "addressed" }, "2": { status: "open", impact: "blocking" }, "3": { status: "open", impact: "nice_to_have" } },
      },
      outcome({ state: "request_changes", blocking: ["gh-2"], nits: ["gh-3"], pending: ["gh-4"], reviewed_sha: "abc" }),
    ],
  ])("%s", (_name, extra, want) => {
    expect(deriveOutcome(session(extra))).toEqual(want);
  });
});

describe("outcomeLabel", () => {
  it.each([
    ["approve", "Approve"],
    ["approve_with_nits", "Approve with nits"],
    ["request_changes", "Request changes"],
    ["unknown", "Outcome unknown"],
  ] as const)("%s is %s", (state, want) => {
    expect(outcomeLabel(state)).toBe(want);
  });
});

describe("outcomeColor", () => {
  const colors = { active: "#active", dangerText: "#danger", textDim: "#dim" } as ColorPalette;
  it.each([
    ["approve", "#active"],
    ["approve_with_nits", "#active"],
    ["request_changes", "#danger"],
    ["unknown", "#dim"],
  ] as const)("%s", (state, want) => {
    expect(outcomeColor(colors, state)).toBe(want);
  });
});

describe("outcomeTitle", () => {
  it.each<[ReviewOutcome, string]>([
    [outcome({ state: "request_changes", blocking: ["a", "b"] }), "2 blocking: a, b"],
    [outcome({ state: "approve_with_nits", nits: ["n"] }), "1 nice to have: n"],
    [outcome(), "Nothing left open"],
    [outcome({ state: "unknown", reason: "the head commit isn't reviewed yet" }), "Can't tell yet: the head commit isn't reviewed yet"],
    [outcome({ state: "unknown" }), "Can't tell yet"],
  ])("%o", (o, want) => {
    expect(outcomeTitle(o)).toBe(want);
  });
});

describe("canApprove", () => {
  it.each([
    ["approve", true],
    ["approve_with_nits", true],
    ["request_changes", false],
    ["unknown", false],
  ] as const)("%s is %s", (state, want) => {
    expect(canApprove(outcome({ state }))).toBe(want);
  });
});

describe("approveDisabledReason", () => {
  it.each<[ReviewOutcome, string]>([
    [outcome({ state: "request_changes", blocking: ["a"] }), "1 blocking comment left; Approve anyway is in the ⋯ menu"],
    [outcome({ state: "request_changes", blocking: ["a", "b"] }), "2 blocking comments left; Approve anyway is in the ⋯ menu"],
    [outcome({ state: "unknown", reason: "the head commit isn't reviewed yet" }), "The head commit isn't reviewed yet; Approve anyway is in the ⋯ menu"],
    [outcome({ state: "unknown" }), "The outcome is unknown; Approve anyway is in the ⋯ menu"],
  ])("%o", (o, want) => {
    expect(approveDisabledReason(o)).toBe(want);
  });
});

describe("isApprovedAtHead", () => {
  it.each([
    [{ head_sha: "abc", approved_sha: "abc" }, true],
    [{ head_sha: "abd", approved_sha: "abc" }, false],
    [{ head_sha: "abc" }, false],
    [{}, false],
  ])("%o is %s", (s, want) => {
    expect(isApprovedAtHead(s)).toBe(want);
  });
});

describe("approveBody", () => {
  it("is empty without nits", () => {
    expect(approveBody(outcome(), [comment("a")])).toBe("");
  });

  it("lists each nit with its place, first line and link", () => {
    const cs = [
      comment("n1", { body: "\nRename this helper\nmore detail", url: "https://example.com/pr/1#discussion_r1" }),
      comment("n2", { path: "", body: "Consider a test" }),
      comment("other", { body: "not a nit" }),
    ];
    expect(approveBody(outcome({ state: "approve_with_nits", nits: ["n1", "n2", "gone"] }), cs)).toBe(
      ["Approved. A few nice-to-haves, none blocking:", "", "- `internal/api/x.go:12` Rename this helper (https://example.com/pr/1#discussion_r1)", "- Consider a test"].join("\n"),
    );
  });
});

describe("impactLabel", () => {
  it("names each impact", () => {
    expect(impactLabel("blocking")).toBe("blocking");
    expect(impactLabel("nice_to_have")).toBe("nice to have");
  });
});
