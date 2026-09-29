import { describe, expect, it } from "vitest";
import type { Explanation } from "../../api/explain";
import type { Message } from "../../types";
import { explainAction, explainActionLabel, explainToggleTitle, explanationPending, mergeExplanations, snippetLine, turnEndMsgIds } from "./explainState";

function expl(id: number, messageId: string, over: Partial<Explanation> = {}): Explanation {
  return {
    id,
    channel_id: "ch-1",
    message_id: messageId,
    explain_channel_id: "explain-1",
    status: "done",
    content: "",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

function msg(id: number, msgId: string, over: Partial<Message> = {}): Message {
  return {
    id,
    channel_id: "ch-1",
    msg_id: msgId,
    author_id: "bot",
    author_name: "bot",
    content: "",
    is_bot: true,
    is_processed: true,
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

describe("explainToggleTitle", () => {
  it("says what applies and where it comes from", () => {
    expect(explainToggleTitle("", true)).toMatch(/^Explain is on — config default \(on\)\./);
    expect(explainToggleTitle("off", true)).toMatch(/^Explain is off — set for this channel\./);
    expect(explainToggleTitle("on", false)).toMatch(/Click to turn it off\.$/);
  });
});

describe("mergeExplanations", () => {
  it("adds new ones, newest first", () => {
    const got = mergeExplanations([expl(1, "a")], [expl(3, "c"), expl(2, "b")]);
    expect(got.map((e) => e.message_id)).toEqual(["c", "b", "a"]);
  });

  it("replaces one by message, keeping the snippets an event doesn't carry", () => {
    const cur = expl(1, "a", { status: "queued", message_row_id: 7, prompt: "fix it", reply: "Fixed." });
    const got = mergeExplanations([cur], [expl(1, "a", { status: "done", content: "## Summary" })]);
    expect(got).toEqual([{ ...cur, status: "done", content: "## Summary" }]);
  });

  it("takes newer snippets", () => {
    const got = mergeExplanations([expl(1, "a", { prompt: "old" })], [expl(1, "a", { prompt: "new" })]);
    expect(got[0]!.prompt).toBe("new");
  });
});

describe("explainAction", () => {
  it.each([
    [undefined, "explain", "Explain", false],
    [expl(1, "a", { status: "queued" }), "pending", "Explain queued…", true],
    [expl(1, "a", { status: "running" }), "pending", "Explaining…", true],
    [expl(1, "a", { status: "done" }), "open", "Explained", false],
    [expl(1, "a", { status: "failed" }), "open", "Explain failed", false],
  ] as const)("%# → %j %j", (e, action, label, pending) => {
    expect(explainAction(e)).toBe(action);
    expect(explainActionLabel(explainAction(e), e)).toBe(label);
    expect(explanationPending(e)).toBe(pending);
  });
});

describe("turnEndMsgIds", () => {
  const messages = [
    msg(1, "u1", { is_bot: false }),
    msg(2, "b1", { trigger_msg_id: "u1" }),
    msg(4, "b2", { trigger_msg_id: "u1" }),
    msg(3, "b3", { trigger_msg_id: "u1" }),
    msg(5, "u2", { is_bot: false }),
    msg(6, "b4", { trigger_msg_id: "u2" }),
    msg(7, "legacy"),
    msg(0, "live", { trigger_msg_id: "u3" }),
  ];

  it("is each turn's last stored bot message", () => {
    expect([...turnEndMsgIds(messages, null)].sort()).toEqual(["b2", "b4"]);
  });

  it("leaves out the turn still running", () => {
    expect([...turnEndMsgIds(messages, "u2")]).toEqual(["b2"]);
  });
});

describe("snippetLine", () => {
  it("flattens and cuts", () => {
    expect(snippetLine(undefined)).toBe("");
    expect(snippetLine("  a\n\n b  ")).toBe("a b");
    expect(snippetLine("abcdef", 3)).toBe("abc…");
  });
});
