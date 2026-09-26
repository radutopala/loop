import { describe, expect, it } from "vitest";
import type { Message } from "../../types";
import { processingMsgIdOf } from "./processingMsg";

const msg = (msg_id: string, is_running?: boolean): Message => ({
  id: 1,
  channel_id: "ch",
  msg_id,
  author_id: "u",
  author_name: "u",
  content: "",
  is_bot: false,
  is_processed: false,
  is_running,
  created_at: "",
});

describe("processingMsgIdOf", () => {
  it.each([
    { name: "agent.status names it", id: "m2", running: true, queue: [msg("m1", true)], want: "m2" },
    { name: "nothing running", id: null, running: false, queue: [msg("m1", true)], want: null },
    { name: "the row a chat run claimed", id: null, running: true, queue: [msg("m1"), msg("m2", true)], want: "m2" },
    { name: "a task run claims no row", id: null, running: true, queue: [msg("m1"), msg("m2")], want: null },
    { name: "empty queue", id: null, running: true, queue: [], want: null },
  ])("$name", ({ id, running, queue, want }) => {
    expect(processingMsgIdOf(id, running, queue)).toBe(want);
  });
});
