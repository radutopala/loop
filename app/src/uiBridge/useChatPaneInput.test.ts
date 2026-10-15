import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatState } from "../hooks/useChatState";
import type { Message } from "../types";
import { chatPaneInput } from "./useChatPaneInput";

const sendMessage = vi.fn(async (..._args: unknown[]) => {});
vi.mock("../api/channels", () => ({ sendMessage: (...args: unknown[]) => sendMessage(...args) }));

const msg = (over: Partial<Message>): Message =>
  ({ id: 1, channel_id: "c1", msg_id: "m", author_id: "u", author_name: "Ann", content: "hi", is_bot: false, is_processed: true, created_at: "", ...over }) as Message;

let state: ChatState;
beforeEach(() => {
  sendMessage.mockClear();
  state = { isRunning: false, queuedMessages: [], messages: [], askUserQuestions: null, exitPlanRequest: null, gateApprovals: {}, mode: "agent" } as unknown as ChatState;
});

const input = () =>
  chatPaneInput(
    "side-1",
    () => state,
    () => 10_000,
  );

describe("chatPaneInput", () => {
  it("is busy while it runs or has a message queued to run now", () => {
    expect(input().busy()).toBe(false);
    state.isRunning = true;
    expect(input().busy()).toBe(true);
    state.isRunning = false;
    state.queuedMessages = [msg({ not_before: 20 })];
    expect(input().busy()).toBe(false);
    state.queuedMessages = [msg({ not_before: 5 })];
    expect(input().busy()).toBe(true);
    state.queuedMessages = [msg({})];
    expect(input().busy()).toBe(true);
  });

  it("says what it waits on an answer to", () => {
    expect(input().blocked()).toBeNull();
    const approval = { req_id: "r", kind: "bash", target: "ls" } as ChatState["gateApprovals"][string];
    state.gateApprovals = { "terminal:x": approval };
    expect(input().blocked()).toBeNull();
    state.gateApprovals = { chat: approval };
    expect(input().blocked()).toBe("an approval");
    state.exitPlanRequest = {} as ChatState["exitPlanRequest"];
    expect(input().blocked()).toBe("a plan");
    state.askUserQuestions = {} as ChatState["askUserQuestions"];
    expect(input().blocked()).toBe("a question");
  });

  it("lists its messages", () => {
    state.messages = [msg({}), msg({ id: 2, author_name: "Loop", is_bot: true, content: "hello" })];
    expect(input().messages()).toEqual([
      { id: 1, author: "Ann", bot: false, content: "hi" },
      { id: 2, author: "Loop", bot: true, content: "hello" },
    ]);
  });

  it("sends to its channel in the chat's mode", async () => {
    state.mode = "plan";
    await input().send("go");
    expect(sendMessage).toHaveBeenCalledWith("side-1", "go", "plan");
  });
});
