import { describe, expect, it } from "vitest";
import type { LearnProposal } from "../../api/learn";
import { isOpenProposal, learnBadgeLabel, learnEffective, learnKindLabel, learnToggleTitle, mergeProposals, proposalCaveat, proposalDetail } from "./learnState";

describe("learnEffective", () => {
  it.each([
    ["", false, false],
    ["", true, true],
    ["on", false, true],
    ["off", true, false],
  ] as const)("learn=%j default=%j → %j", (learn, def, want) => {
    expect(learnEffective(learn, def)).toBe(want);
  });
});

describe("learnToggleTitle", () => {
  it("names the config default when the channel inherits it", () => {
    expect(learnToggleTitle("", true)).toMatch(/^Learn is on — config default \(on\)\./);
    expect(learnToggleTitle("", true)).toMatch(/Click to turn it off\.$/);
  });

  it("says when the channel has its own setting", () => {
    expect(learnToggleTitle("off", true)).toMatch(/^Learn is off — set for this channel\./);
    expect(learnToggleTitle("off", true)).toMatch(/Click to turn it on\.$/);
  });
});

function proposal(over: Partial<LearnProposal>): LearnProposal {
  return {
    id: 1,
    channel_id: "c",
    learn_channel_id: "l",
    kind: "rename",
    title: "t",
    rationale: "",
    payload: "{}",
    status: "pending",
    created_at: "",
    updated_at: "",
    ...over,
  };
}

describe("learnBadgeLabel", () => {
  it.each([
    [true, 3, "learning…"],
    [false, 1, "1 proposal"],
    [false, 2, "2 proposals"],
    [false, 0, null],
  ] as const)("running=%j open=%j → %j", (running, open, want) => {
    expect(learnBadgeLabel(running, open)).toBe(want);
  });
});

describe("isOpenProposal", () => {
  it.each([
    ["pending", true],
    ["failed", true],
    ["applying", false],
    ["applied", false],
    ["dismissed", false],
  ] as const)("%s → %j", (status, want) => {
    expect(isOpenProposal(proposal({ status }))).toBe(want);
  });
});

describe("mergeProposals", () => {
  it("adds new ones, replaces updated ones and sorts newest first", () => {
    const list = [proposal({ id: 1 }), proposal({ id: 2 })];
    const got = mergeProposals(list, [proposal({ id: 3 }), proposal({ id: 1, status: "applied" })]);
    expect(got.map((p) => [p.id, p.status])).toEqual([
      [3, "pending"],
      [2, "pending"],
      [1, "applied"],
    ]);
  });
});

describe("learnKindLabel", () => {
  it("names known kinds and passes unknown ones through", () => {
    expect(learnKindLabel("gate_rule")).toBe("gate rule");
    expect(learnKindLabel("wish")).toBe("wish");
  });
});

describe("proposalDetail", () => {
  it.each([
    ["prompt_shortcut", { name: "fix", prompt: "make test" }, "#fix → make test"],
    ["bash_shortcut", { name: "lint", command: "make lint" }, "lint → $ make lint"],
    ["scheduled_task", { type: "cron", schedule: "0 9 * * *", prompt: "check deps" }, "cron 0 9 * * * → check deps"],
    ["scheduled_task", { type: "interval", schedule: "1h", bash_script: "curl x" }, "interval 1h → $ curl x"],
    ["gate_rule", { type: "command", rule: { commands: ["git", "gh"], decision: "approve" } }, "command rule: approve git, gh"],
    ["gate_rule", { type: "path", rule: { pattern: "/run/x.sock", decision: "allow" } }, "path rule: allow /run/x.sock"],
    ["gate_rule", { type: "file" }, "file rule:"],
    ["mount", { mount: "~/.aws:~/.aws:ro" }, "~/.aws:~/.aws:ro"],
    ["rename", { name: "login bug" }, "→ login bug"],
    ["description", { description: "chasing it" }, "chasing it"],
    ["rename", { name: 7 }, "→ "],
  ] as const)("%s %j", (kind, payload, want) => {
    expect(proposalDetail(proposal({ kind, payload: JSON.stringify(payload) }))).toBe(want);
  });

  it("shows the raw payload when it isn't JSON or the kind is unknown", () => {
    expect(proposalDetail(proposal({ payload: "{" }))).toBe("{");
    expect(proposalDetail(proposal({ kind: "wish" as LearnProposal["kind"], payload: "{}" }))).toBe("{}");
  });
});

describe("proposalCaveat", () => {
  it("warns only for renaming a worktree thread", () => {
    expect(proposalCaveat(proposal({ kind: "rename" }), true)).toMatch(/branch and worktree folder/);
    expect(proposalCaveat(proposal({ kind: "rename" }), false)).toBeNull();
    expect(proposalCaveat(proposal({ kind: "mount" }), true)).toBeNull();
  });
});
