// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatState } from "../hooks/useChatState";
import type { Message } from "../types";
import { getChatInput } from "./registry";
import { chatPaneInput, useChatPaneInput } from "./useChatPaneInput";

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

describe("useChatPaneInput", () => {
  it.each([
    ["no channel", null, "chat-0", "c1"],
    ["no pane", "c1", undefined, "c1"],
    ["no chat channel", "c1", "chat-0", null],
  ])("registers nothing with %s", (_name, channelId, paneId, chatChannelId) => {
    renderHook(() => useChatPaneInput(channelId, paneId, chatChannelId, state));
    expect(getChatInput("c1", "chat-0")).toBeUndefined();
  });

  it("registers the pane's chat with its latest state until unmounted", () => {
    const hook = renderHook(({ s }) => useChatPaneInput("c1", "chat-0", "side-1", s), { initialProps: { s: state } });
    const registered = getChatInput("c1", "chat-0");
    expect(registered?.busy()).toBe(false);
    hook.rerender({ s: { ...state, isRunning: true } });
    expect(getChatInput("c1", "chat-0")).toBe(registered);
    expect(registered?.busy()).toBe(true);
    hook.unmount();
    expect(getChatInput("c1", "chat-0")).toBeUndefined();
  });
});
