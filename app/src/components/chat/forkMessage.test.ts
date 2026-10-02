import { describe, expect, it } from "vitest";
import type { Message } from "../../types";
import { canForkMessage } from "./forkMessage";

function msg(over: Partial<Message> = {}): Message {
  return {
    id: 1,
    channel_id: "ch-1",
    msg_id: "m-1",
    author_id: "u",
    author_name: "u",
    content: "hi",
    is_bot: false,
    is_processed: true,
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

describe("canForkMessage", () => {
  const cases: { name: string; message: Message; state?: { queued?: boolean; processing?: boolean }; want: boolean }[] = [
    { name: "forkable bot reply", message: msg({ is_bot: true, forkable: true }), want: true },
    { name: "bot reply without a transcript position", message: msg({ is_bot: true }), want: false },
    { name: "bot reply marked not forkable", message: msg({ is_bot: true, forkable: false }), want: false },
    { name: "bot reply ignores the queue state", message: msg({ is_bot: true, forkable: true, is_processed: false }), state: { queued: true }, want: true },
    { name: "processed user message", message: msg(), want: true },
    { name: "unprocessed user message", message: msg({ is_processed: false }), want: false },
    { name: "queued user message", message: msg(), state: { queued: true }, want: false },
    { name: "user message being processed", message: msg(), state: { processing: true }, want: false },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(canForkMessage(c.message, c.state)).toBe(c.want);
    });
  }
});
