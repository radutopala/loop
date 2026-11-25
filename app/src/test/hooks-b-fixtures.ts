import type { Message, TimelineItem } from "../types";

/** A promise with its resolve and reject, to settle a mocked call later. */
export function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** A user message with the given msg_id; fields can be overridden. */
export function message(msgId: string, over: Partial<Message> = {}): Message {
  return {
    id: 1,
    channel_id: "ch",
    msg_id: msgId,
    author_id: "u1",
    author_name: "user",
    content: `text of ${msgId}`,
    is_bot: false,
    is_processed: false,
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

/** A timeline row holding message(msgId) at position/id. */
export function messageItem(msgId: string, position: number, id: number, over: Partial<Message> = {}): TimelineItem {
  return { kind: "message", position, id, data: message(msgId, { id, ...over }), trigger_msg_id: over.trigger_msg_id };
}
