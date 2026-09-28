import { describe, expect, it } from "vitest";
import type { LearnProposal } from "../../api/learn";
import {
  inBulk,
  isOpenProposal,
  LEARN_APPLY_STALE_MS,
  learnBadgeLabel,
  learnEffective,
  learnKindLabel,
  learnPassRunning,
  learnToggleTitle,
  mergeProposals,
  newlyAppliedShortcut,
  nextStaleIn,
  proposalCaveat,
  proposalDetail,
} from "./learnState";

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

  it("reopens one stuck applying past the stale window", () => {
    const p = proposal({ status: "applying", updated_at: "2026-09-27T10:00:00Z" });
    const at = Date.parse(p.updated_at);
    expect(isOpenProposal(p, at + LEARN_APPLY_STALE_MS)).toBe(false);
    expect(isOpenProposal(p, at + LEARN_APPLY_STALE_MS + 1)).toBe(true);
  });
});

describe("nextStaleIn", () => {
  const at = Date.parse("2026-09-27T10:00:00Z");
  const applying = (secsAgo: number, id = 1) => proposal({ id, status: "applying", updated_at: new Date(at - secsAgo * 1000).toISOString() });

  it("is null with nothing applying, or only stale ones", () => {
    expect(nextStaleIn([proposal({ status: "pending" })], at)).toBeNull();
    expect(nextStaleIn([applying(61)], at)).toBeNull();
  });

  it("is the time until the soonest one applying goes stale", () => {
    expect(nextStaleIn([applying(10, 1), applying(50, 2), applying(90, 3)], at)).toBe(LEARN_APPLY_STALE_MS - 50_000);
  });
});

describe("inBulk", () => {
  const at = Date.parse("2026-09-27T10:00:00Z");
  it.each([
    ["pending", true, true],
    ["failed", false, true],
    ["applying", false, false],
    ["applied", false, false],
    ["dismissed", false, false],
  ] as const)("%s → apply all %j, dismiss all %j", (status, apply, dismiss) => {
    const p = proposal({ status, updated_at: new Date(at).toISOString() });
    expect(inBulk("apply", p, at)).toBe(apply);
    expect(inBulk("dismiss", p, at)).toBe(dismiss);
  });

  it("dismisses one stuck applying, but doesn't apply it", () => {
    const p = proposal({ status: "applying", updated_at: new Date(at - LEARN_APPLY_STALE_MS - 1).toISOString() });
    expect(inBulk("dismiss", p, at)).toBe(true);
    expect(inBulk("apply", p, at)).toBe(false);
  });

  it("skips one no longer listed", () => {
    expect(inBulk("apply", undefined)).toBe(false);
    expect(inBulk("dismiss", undefined)).toBe(false);
  });
});

describe("newlyAppliedShortcut", () => {
  it.each([
    ["prompt_shortcut", "applied", true],
    ["bash_shortcut", "applied", true],
    ["prompt_shortcut", "pending", false],
    ["bash_shortcut", "failed", false],
    ["gate_rule", "applied", false],
  ] as const)("%s %s → %j", (kind, status, want) => {
    expect(newlyAppliedShortcut([], [proposal({ id: 1, kind: "rename", status: "applied" }), proposal({ id: 2, kind, status })])).toBe(want);
  });

  it("counts only a shortcut that wasn't applied in the current list", () => {
    const pending = proposal({ id: 2, kind: "bash_shortcut", status: "pending" });
    const applying = proposal({ id: 2, kind: "bash_shortcut", status: "applying" });
    const applied = proposal({ id: 2, kind: "bash_shortcut", status: "applied" });
    expect(newlyAppliedShortcut([pending], [applied])).toBe(true);
    expect(newlyAppliedShortcut([applying], [applied])).toBe(true);
    // A reconnect refetch brings back the ones applied before: nothing new.
    expect(newlyAppliedShortcut([applied], [applied])).toBe(false);
    expect(newlyAppliedShortcut([applied, proposal({ id: 3, kind: "rename", status: "applied" })], [applied, proposal({ id: 3, kind: "rename", status: "applied" })])).toBe(false);
    // One applied among the old ones still counts.
    expect(newlyAppliedShortcut([applied, proposal({ id: 4, kind: "prompt_shortcut" })], [applied, proposal({ id: 4, kind: "prompt_shortcut", status: "applied" })])).toBe(true);
  });
});

describe("learnPassRunning", () => {
  it.each([
    [false, "running", "learn", true],
    [false, "running", "learn-reply", false],
    [true, "running", "learn-reply", true],
    [false, "running", undefined, false],
    [true, "completed", "learn", false],
    [true, "completed", "learn-reply", false],
    [true, "failed", undefined, false],
  ] as const)("running=%j, %s trigger=%j → %j", (cur, status, trigger, want) => {
    expect(learnPassRunning(cur, status, trigger)).toBe(want);
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
    [
      "gate_rule",
      { type: "command", rule: { commands: ["rm"], args_patterns: ["^-rf /tmp/", "^/tmp/"], decision: "allow", message: "scratch only" } },
      "command rule: allow rm with args matching ^-rf /tmp/ | ^/tmp/ — “scratch only”",
    ],
    ["gate_rule", { type: "command", rule: { args_patterns: ["--force"], decision: "deny" } }, "command rule: deny any command with args matching --force"],
    ["gate_rule", { type: "path", rule: { pattern: "/run/x.sock", decision: "allow" } }, "path rule: allow /run/x.sock"],
    ["gate_rule", { type: "file", rule: { paths: ["/etc/**"], operations: ["write", "unlink"], decision: "deny" } }, "file rule: deny /etc/** on write, unlink"],
    ["gate_rule", { type: "file", rule: { decision: "approve" } }, "file rule: approve any path"],
    ["gate_rule", { type: "file" }, "file rule: any path"],
    ["gate_rule", { type: "http", rule: { methods: ["POST"], decision: "deny" } }, 'http rule: {"methods":["POST"],"decision":"deny"}'],
    ["mount", { mount: "~/.aws:~/.aws:ro" }, "~/.aws:~/.aws:ro"],
    ["rename", { name: "login bug" }, "→ login bug"],
    ["description", { description: "chasing it" }, "chasing it"],
    ["ticket_url", { ticket_url: "https://tracker.example.com/T-1" }, "https://tracker.example.com/T-1"],
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
