import { describe, expect, it } from "vitest";
import { orderTimelineItems } from "./orderTimelineItems";
import type { Message, TimelineItem } from "../../types";

// --- builders ---

function userMsg(msgId: string, position: number): TimelineItem {
  const data: Message = {
    id: position,
    channel_id: "c",
    msg_id: msgId,
    author_id: "u",
    author_name: "User",
    content: `msg ${msgId}`,
    is_bot: false,
    is_processed: false,
    created_at: "2026-01-01T00:00:00Z",
  };
  return { kind: "message", position, id: position, data };
}

function botMsg(msgId: string, triggerMsgId: string, position: number): TimelineItem {
  const data: Message = {
    id: position,
    channel_id: "c",
    msg_id: msgId,
    author_id: "bot",
    author_name: "Bot",
    content: `reply ${msgId}`,
    is_bot: true,
    is_processed: true,
    trigger_msg_id: triggerMsgId,
    created_at: "2026-01-01T00:00:00Z",
  };
  return { kind: "message", position, id: position, data, trigger_msg_id: triggerMsgId };
}

function toolUse(triggerMsgId: string, position: number): TimelineItem {
  return {
    kind: "tool_use",
    position,
    id: position,
    tool_use_id: `tu-${position}`,
    tool_name: "Bash",
    tool_input: "{}",
    trigger_msg_id: triggerMsgId,
  };
}

const ids = (items: TimelineItem[]) =>
  items.map((it) => (it.kind === "message" ? it.data.msg_id : `${it.kind}:${it.trigger_msg_id}`));

describe("orderTimelineItems", () => {
  it("preserves order when each user already sits before its first reply", () => {
    // Normal case: user A, its reply, user B, its reply. A relocates "before
    // a1" — its existing slot — so the sequence is unchanged.
    const list = [userMsg("A", 0), botMsg("a1", "A", 1), userMsg("B", 2), botMsg("b1", "B", 3)];
    expect(ids(orderTimelineItems(list))).toEqual(["A", "a1", "B", "b1"]);
  });

  it("moves a queued user message to just before its first reply", () => {
    // A is enqueued early (slot 0) but B's turn ran first. A's reply lands after
    // B's reply. A must render just before a1, after B's group.
    const list = [
      userMsg("A", 0), // queued, inserted early
      userMsg("B", 1),
      botMsg("b1", "B", 2),
      botMsg("a1", "A", 3),
    ];
    expect(ids(orderTimelineItems(list))).toEqual(["B", "b1", "A", "a1"]);
  });

  it("relocates only the user row, keeping the intervening turn intact", () => {
    const list = [
      userMsg("A", 0),
      userMsg("B", 1),
      toolUse("B", 2),
      botMsg("b1", "B", 3),
      toolUse("A", 4),
      botMsg("a1", "A", 5),
    ];
    // A jumps down to right before its first reply (the tool_use at idx 4);
    // B's group is untouched and stays first.
    expect(ids(orderTimelineItems(list))).toEqual([
      "B",
      "tool_use:B",
      "b1",
      "A",
      "tool_use:A",
      "a1",
    ]);
  });

  it("leaves a user message with no reply in the window in place", () => {
    const list = [userMsg("A", 0), userMsg("B", 1), botMsg("b1", "B", 2)];
    // A has no reply loaded → stays put; B already precedes its reply.
    expect(ids(orderTimelineItems(list))).toEqual(["A", "B", "b1"]);
  });

  it("matches replies by trigger_msg_id, not by adjacency", () => {
    // Two queued users A and B; replies interleaved. Each user moves before its
    // own first reply.
    const list = [
      userMsg("A", 0),
      userMsg("B", 1),
      botMsg("a1", "A", 2),
      botMsg("b1", "B", 3),
    ];
    // A's first reply is idx 2, B's is idx 3. A inserts before a1; B before b1.
    expect(ids(orderTimelineItems(list))).toEqual(["A", "a1", "B", "b1"]);
  });

  it("uses the FIRST reply position when a user has several replies", () => {
    const list = [
      userMsg("A", 0),
      userMsg("B", 1),
      botMsg("b1", "B", 2),
      botMsg("a1", "A", 3),
      botMsg("a2", "A", 4),
    ];
    expect(ids(orderTimelineItems(list))).toEqual(["B", "b1", "A", "a1", "a2"]);
  });

  it("does not relocate when the reply precedes the user row (ri <= i guard)", () => {
    // Pathological/out-of-order input where the reply index is not after the
    // user — must be left untouched rather than moved backwards.
    const list = [botMsg("a1", "A", 0), userMsg("A", 1)];
    expect(orderTimelineItems(list)).toBe(list);
  });

  it("handles an empty list", () => {
    expect(orderTimelineItems([])).toEqual([]);
  });
});
