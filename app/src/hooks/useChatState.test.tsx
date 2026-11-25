import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchQueuedMessages } from "../api/channels";
import { fetchTimeline } from "../api/loopApi";
import { deferred, message, messageItem } from "../test/hooks-b-fixtures";
import type { GateApprovalRequestedData, Message, TimelineResponse, WSEvent } from "../types";
import { revalidateMissing } from "../utils/fileLinks";
import { type ChatState, useChatState } from "./useChatState";
import type { ActiveChatState, ChatEventListener } from "./useChatStateStore";

vi.mock("../api/channels", () => ({ fetchQueuedMessages: vi.fn() }));
vi.mock("../api/loopApi", () => ({ fetchTimeline: vi.fn() }));
vi.mock("../utils/fileLinks", () => ({ revalidateMissing: vi.fn() }));

const ev = (type: string, data: unknown = {}, channelId = "ch"): WSEvent => ({ type, channel_id: channelId, data, timestamp: Date.UTC(2026, 0, 2) });
const gate = (req_id: string, source?: string): GateApprovalRequestedData => ({ req_id, kind: "net", target: "example.com", source });

type Opts = Parameters<typeof useChatState>[2];

function setup(opts: { ch?: string | null; running?: boolean; options?: Opts } = {}) {
  let listener: ChatEventListener | undefined;
  const unsubscribe = vi.fn();
  const subscribeChatEvents = vi.fn((l: ChatEventListener) => {
    listener = l;
    return unsubscribe;
  });
  const options: Opts = { subscribeChatEvents, ...opts.options };
  const hook = renderHook(({ ch, running, o }) => useChatState(ch, running, o), {
    initialProps: { ch: opts.ch === undefined ? "ch" : opts.ch, running: opts.running, o: options },
  });
  const send = (...events: WSEvent[]) =>
    act(() => {
      for (const e of events) listener!(e);
    });
  return { ...hook, send, subscribeChatEvents, unsubscribe, options };
}

async function ready(opts: Parameters<typeof setup>[0] = {}) {
  const hook = setup(opts);
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
}

const kinds = (s: ChatState) => s.liveTail.map((it) => it.kind);

describe("useChatState", () => {
  beforeEach(() => {
    vi.mocked(fetchTimeline).mockResolvedValue({ items: [], next_cursor: null });
    vi.mocked(fetchQueuedMessages).mockResolvedValue([]);
  });
  afterEach(() => {
    cleanup();
    vi.mocked(fetchTimeline).mockReset();
    vi.mocked(fetchQueuedMessages).mockReset();
    vi.mocked(revalidateMissing).mockReset();
    vi.restoreAllMocks();
  });

  describe("state", () => {
    it("starts idle", async () => {
      const { result } = await ready();
      expect(result.current).toMatchObject({
        streamingContent: null,
        isRunning: false,
        toolActivity: null,
        agentActivity: null,
        backgroundTasks: null,
        askUserQuestions: null,
        exitPlanRequest: null,
        agentTasks: null,
        mode: "agent",
        completionInfo: null,
        triggerContent: null,
        gateApprovals: {},
        processingMsgId: null,
        queuedMessages: [],
        messages: [],
      });
    });

    it("restores a saved state and hands back the latest one on unmount or channel switch", async () => {
      const saved: ActiveChatState = {
        streamingContent: "partial",
        isRunning: true,
        runId: "r1",
        toolActivity: { tool_name: "Bash", input: "ls" },
        agentActivity: { activity: "thinking" },
        backgroundTasks: [{ id: "b1", started_at: "now" }],
        askUserQuestions: { questions: [] },
        exitPlanRequest: { plan: "p" },
        agentTasks: { tasks: [] },
        mode: "plan",
        completionInfo: { duration_ms: 5 },
        triggerContent: "do it",
        gateApprovals: { chat: gate("g1") },
        processingMsgId: "u1",
      };
      const onUnmount = vi.fn();
      const { result, rerender, options, unmount } = await ready({ options: { initialState: saved, onUnmount } });
      const { runId: _runId, ...shown } = saved;
      expect(result.current).toMatchObject(shown);

      act(() => result.current.setMode("agent"));
      rerender({ ch: "other", running: undefined, o: options });
      expect(onUnmount).toHaveBeenCalledWith({ ...saved, mode: "agent" });
      unmount();
      expect(onUnmount).toHaveBeenCalledTimes(2);
    });

    it("lights the run when the channel list later reports one, but never clears it", async () => {
      const { result, rerender, options } = await ready({ running: false });
      expect(result.current.isRunning).toBe(false);
      rerender({ ch: "ch", running: true, o: options });
      expect(result.current.isRunning).toBe(true);
      rerender({ ch: "ch", running: false, o: options });
      expect(result.current.isRunning).toBe(true);
    });

    it("seeds the run from the channel list at mount", async () => {
      const { result } = await ready({ running: true });
      expect(result.current.isRunning).toBe(true);
    });

    it("unsubscribes on unmount and works without a subscription", async () => {
      const { unsubscribe, unmount } = await ready();
      unmount();
      expect(unsubscribe).toHaveBeenCalled();

      const bare = renderHook(() => useChatState("ch"));
      await waitFor(() => expect(bare.result.current.loading).toBe(false));
      expect(bare.result.current.isRunning).toBe(false);
    });

    it("lists persisted and live messages together", async () => {
      vi.mocked(fetchTimeline).mockResolvedValue({ items: [messageItem("m1", 1, 1), { kind: "thinking", position: 0, id: 0, text: "t" }], next_cursor: null } as TimelineResponse);
      const { result, send } = await ready({ ch: "listed" });
      send(ev("agent.thinking", { text: "x" }), ev("message.created", { ...message("m2"), is_bot: true }));
      expect(result.current.messages.map((m) => m.msg_id)).toEqual(["m1", "m2"]);
    });
  });

  describe("queue", () => {
    it("fetches the channel's queue, and empties it without a channel", async () => {
      vi.mocked(fetchQueuedMessages).mockResolvedValue([message("q1")]);
      const { result, rerender, options } = await ready();
      await waitFor(() => expect(result.current.queuedMessages).toHaveLength(1));
      expect(fetchQueuedMessages).toHaveBeenCalledWith("ch");
      rerender({ ch: null, running: undefined, o: options });
      expect(result.current.queuedMessages).toEqual([]);
      expect(fetchQueuedMessages).toHaveBeenCalledTimes(1);
    });

    it("drops a queue that lands after a channel switch or a newer fetch", async () => {
      const stale = deferred<Message[]>();
      const older = deferred<Message[]>();
      const newer = deferred<Message[]>();
      vi.mocked(fetchQueuedMessages).mockReturnValueOnce(stale.promise).mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
      const { result, rerender, options, send } = setup({ ch: "a" });
      rerender({ ch: "b", running: undefined, o: options });
      await act(async () => stale.resolve([message("qa")]));
      expect(result.current.queuedMessages).toEqual([]);

      send(ev("messages.requeued", {}, "b"));
      await act(async () => newer.resolve([message("new")]));
      await act(async () => older.resolve([message("old")]));
      expect(result.current.queuedMessages.map((m) => m.msg_id)).toEqual(["new"]);
    });

    it("logs a failed queue fetch", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      vi.mocked(fetchQueuedMessages).mockRejectedValue(new Error("down"));
      await ready();
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[chat] fetchQueuedMessages failed:", expect.any(Error)));
    });

    it.each([
      ["message.taken", { msg_id: "q1" }],
      ["messages.requeued", {}],
      ["message.deleted", { msg_id: "q1" }],
      ["message.updated", { msg_id: "q1", content: "x" }],
      ["message.created", { ...message("u2"), is_bot: false }],
    ])("refreshes the queue on %s", async (type, data) => {
      const { send } = await ready();
      const calls = vi.mocked(fetchQueuedMessages).mock.calls.length;
      send(ev(type, data));
      expect(fetchQueuedMessages).toHaveBeenCalledTimes(calls + 1);
    });
  });

  describe("message events", () => {
    it("streams a bot reply until the message lands", async () => {
      const { result, send } = await ready();
      send(ev("message.streaming", { content: "hel" }));
      expect(result.current.streamingContent).toBe("hel");
      const calls = vi.mocked(fetchQueuedMessages).mock.calls.length;
      const { id: _id, created_at: _c, channel_id: _ch, ...data } = message("b1", { is_bot: true, tags: { x: "1" } });
      send(ev("message.created", data));
      expect(result.current.streamingContent).toBeNull();
      expect(result.current.liveTail[0]).toMatchObject({ kind: "message", data: { id: 0, msg_id: "b1", channel_id: "ch", tags: { x: "1" }, created_at: "2026-01-02T00:00:00.000Z" } });
      expect(fetchQueuedMessages).toHaveBeenCalledTimes(calls);
    });

    it("keeps the stream when a user message lands", async () => {
      const { result, send } = await ready();
      send(ev("message.streaming", { content: "hel" }), ev("message.created", { ...message("u1", { id: 7 }), is_bot: false }));
      expect(result.current.streamingContent).toBe("hel");
      expect(result.current.liveTail[0]).toMatchObject({ data: { id: 7 } });
    });

    it("moves a taken message, removes a deleted one and edits an updated one", async () => {
      const { result, send } = await ready();
      send(ev("message.created", message("a")), ev("message.created", message("b")), ev("agent.thinking", { text: "t" }));
      send(ev("message.taken", { msg_id: "a" }));
      expect(result.current.liveTail.map((it) => it.kind === "message" && it.data.msg_id)).toEqual(["b", false, "a"]);
      send(ev("message.deleted", { msg_id: "b" }), ev("message.updated", { msg_id: "a", content: "edited" }));
      expect(result.current.messages.map((m) => [m.msg_id, m.content])).toEqual([["a", "edited"]]);
    });

    it("ends the run when its messages are processed", async () => {
      const { result, send } = await ready();
      send(
        ev("agent.status", { status: "running", run_id: "r1", msg_id: "u1" }),
        ev("agent.activity", { activity: "background_tasks", tasks: [{ id: "b", started_at: "t" }] }),
        ev("message.created", message("u1")),
      );
      send(ev("messages.processed", { msg_ids: ["other"] }));
      expect(result.current.isRunning).toBe(false);
      expect(result.current.backgroundTasks).toBeNull();
      expect(result.current.processingMsgId).toBe("u1");
      send(ev("messages.processed", { msg_ids: ["u1"] }));
      expect(result.current.processingMsgId).toBeNull();
      expect(result.current.messages[0]!.is_processed).toBe(true);
    });

    it("ends the run on processed messages with nothing processing", async () => {
      const { result, send } = await ready({ running: true });
      send(ev("messages.processed", { msg_ids: ["x"] }));
      expect(result.current.isRunning).toBe(false);
      expect(result.current.processingMsgId).toBeNull();
    });
  });

  describe("agent events", () => {
    it("adds tool calls, thinking and results to the live tail under the processing message", async () => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "running", msg_id: "u1" }));
      send(
        ev("tool.use", { tool_use_id: "t1", tool_name: "Bash", input: "ls" }),
        ev("agent.thinking", { text: "hmm" }),
        ev("tool.result", { tool_use_id: "t1", output: "ok", is_error: true }),
        ev("tool.result", { tool_use_id: "t2", output: "ok" }),
      );
      expect(result.current.toolActivity).toEqual({ tool_name: "Bash", input: "ls" });
      expect(result.current.liveTail).toMatchObject([
        { kind: "tool_use", tool_name: "Bash", trigger_msg_id: "u1" },
        { kind: "thinking", text: "hmm", trigger_msg_id: "u1" },
        { kind: "tool_result", is_error: true, trigger_msg_id: "u1" },
        { kind: "tool_result", is_error: false },
      ]);
      expect(revalidateMissing).toHaveBeenCalledWith("ch");
    });

    it("leaves live items unattributed with no processing message", async () => {
      const { result, send } = await ready();
      send(ev("tool.use", { tool_name: "Read", input: "f" }), ev("agent.thinking", { text: "x" }), ev("tool.result", { output: "o" }), ev("agent.activity", { activity: "compacting" }));
      expect(result.current.liveTail.every((it) => it.trigger_msg_id === undefined)).toBe(true);
    });

    it("follows plan mode from the plan tools", async () => {
      const { result, send } = await ready();
      send(ev("tool.use", { tool_name: "EnterPlanMode", input: "" }));
      expect(result.current.mode).toBe("plan");
      send(ev("tool.use", { tool_name: "ExitPlanMode", input: "" }));
      expect(result.current.mode).toBe("agent");
    });

    it("keeps background tasks apart from the rolling activity", async () => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "running", msg_id: "u1" }), ev("agent.activity", { activity: "compacting" }));
      expect(result.current.agentActivity).toEqual({ activity: "compacting" });
      expect(kinds(result.current)).toEqual(["compacting"]);
      expect(result.current.liveTail[0]!.trigger_msg_id).toBe("u1");

      const tasks = [{ id: "b", started_at: "t" }];
      send(ev("agent.activity", { activity: "background_tasks", tasks }), ev("agent.activity", { activity: "model" }));
      expect(result.current.backgroundTasks).toEqual(tasks);
      expect(result.current.agentActivity).toEqual({ activity: "model" });
      send(ev("agent.activity", { activity: "background_tasks", tasks: [] }));
      expect(result.current.backgroundTasks).toBeNull();
      send(ev("agent.activity", { activity: "background_tasks", tasks }), ev("agent.activity", { activity: "background_tasks" }));
      expect(result.current.backgroundTasks).toBeNull();
    });

    it.each([
      ["agent.ask_user", { questions: [] }, "askUserQuestions", "agent.ask_resolved"],
      ["agent.exit_plan", { plan: "p" }, "exitPlanRequest", "agent.plan_resolved"],
    ] as const)("parks the run on %s until it resolves", async (type, data, field, resolved) => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "running", run_id: "r1" }), ev(type, data));
      expect(result.current[field]).toEqual(data);
      expect(result.current.isRunning).toBe(false);
      // A new run doesn't drop the card.
      send(ev("agent.status", { status: "running" }));
      expect(result.current[field]).toEqual(data);
      send(ev(resolved));
      expect(result.current[field]).toBeNull();
    });

    it("shows the agent's task list until the turn ends", async () => {
      const { result, send } = await ready();
      const tasks = { tasks: [{ id: "1", subject: "s", status: "pending" as const }] };
      send(ev("agent.tasks", tasks));
      expect(result.current.agentTasks).toEqual(tasks);
      send(ev("agent.status", { status: "error" }));
      expect(result.current.agentTasks).toBeNull();
    });

    it("ignores events it doesn't know", async () => {
      const { result, send } = await ready();
      const before = result.current;
      send(ev("channel.renamed"));
      expect(result.current).toBe(before);
    });
  });

  describe("gate approvals", () => {
    it("keys requests by source, treating a missing one as the chat", async () => {
      const { result, send } = await ready();
      send(ev("gate.approval_requested", gate("g1")), ev("gate.approval_requested", gate("g2", "")), ev("gate.approval_requested", gate("g3", "terminal:a")));
      expect(result.current.gateApprovals).toEqual({ chat: gate("g2", ""), "terminal:a": gate("g3", "terminal:a") });

      const before = result.current.gateApprovals;
      send(ev("gate.approval_resolved", { req_id: "nope" }));
      expect(result.current.gateApprovals).toBe(before);
      send(ev("gate.approval_resolved", { req_id: "g3" }));
      expect(result.current.gateApprovals).toEqual({ chat: gate("g2", "") });
    });

    it("clears one source on demand", async () => {
      const { result, send } = await ready();
      send(ev("gate.approval_requested", gate("g1")), ev("gate.approval_requested", gate("g2", "terminal:a")));
      const before = result.current.gateApprovals;
      act(() => result.current.clearGateApproval("terminal:zz"));
      expect(result.current.gateApprovals).toBe(before);
      act(() => result.current.clearGateApproval("chat"));
      expect(result.current.gateApprovals).toEqual({ "terminal:a": gate("g2", "terminal:a") });
    });

    it("drops the chat's approval when its run ends, keeping terminal ones", async () => {
      const { result, send } = await ready();
      send(ev("gate.approval_requested", gate("g2", "terminal:a")));
      const before = result.current.gateApprovals;
      send(ev("agent.status", { status: "completed" }));
      expect(result.current.gateApprovals).toBe(before);
      send(ev("gate.approval_requested", gate("g1")), ev("agent.status", { status: "completed" }));
      expect(result.current.gateApprovals).toEqual({ "terminal:a": gate("g2", "terminal:a") });
    });
  });

  describe("agent.status", () => {
    it("starts a run with its trigger and processing message", async () => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "completed", duration_ms: 1 }));
      expect(result.current.completionInfo).not.toBeNull();
      const calls = vi.mocked(fetchQueuedMessages).mock.calls.length;
      send(ev("agent.status", { status: "running", run_id: "r1", trigger_content: "go", msg_id: "u1" }));
      expect(result.current).toMatchObject({ isRunning: true, triggerContent: "go", processingMsgId: "u1", completionInfo: null });
      expect(fetchQueuedMessages).toHaveBeenCalledTimes(calls + 1);

      send(ev("agent.status", { status: "running", trigger_content: "" }));
      expect(result.current.triggerContent).toBeNull();
      expect(result.current.processingMsgId).toBeNull();
    });

    it("ends only the run it tracks, and refetches the timeline head", async () => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "running", run_id: "r1", trigger_content: "go" }), ev("tool.use", { tool_name: "Bash", input: "ls" }), ev("agent.activity", { activity: "model" }));
      const fetches = vi.mocked(fetchTimeline).mock.calls.length;
      send(ev("agent.status", { status: "completed", run_id: "r0" }));
      expect(result.current.isRunning).toBe(true);
      expect(fetchTimeline).toHaveBeenCalledTimes(fetches);

      send(ev("agent.status", { status: "completed", run_id: "r1" }));
      expect(result.current).toMatchObject({ isRunning: false, toolActivity: null, agentActivity: null, triggerContent: null, processingMsgId: null });
      expect(fetchTimeline).toHaveBeenCalledTimes(fetches + 1);
      await act(async () => {});
    });

    it("ends any run when the finish carries no run id", async () => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "running", run_id: "r1" }), ev("agent.status", { status: "error" }));
      expect(result.current.isRunning).toBe(false);
      await act(async () => {});
    });

    it.each([
      ["a duration", { duration_ms: 10, num_turns: 2, model: "m" }, { duration_ms: 10, num_turns: 2, stop_reason: undefined, model: "m" }],
      ["a stop reason", { stop_reason: "max_turns" }, { stop_reason: "max_turns" }],
      ["neither", {}, null],
    ])("records completion info for a completed run with %s", async (_name, extra, want) => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "completed", ...extra }));
      if (want === null) expect(result.current.completionInfo).toBeNull();
      else expect(result.current.completionInfo).toMatchObject(want);
      await act(async () => {});
    });

    it("records no completion info for an error", async () => {
      const { result, send } = await ready();
      send(ev("agent.status", { status: "error", duration_ms: 10 }));
      expect(result.current.completionInfo).toBeNull();
      await act(async () => {});
    });
  });

  describe("clearing cards", () => {
    it("clears the ask and plan cards along with their sidebar pills", async () => {
      const clearAskUserPill = vi.fn();
      const clearPlanPill = vi.fn();
      const { result, send } = await ready({ options: { clearAskUserPill, clearPlanPill } });
      send(ev("agent.ask_user", { questions: [] }), ev("agent.exit_plan", { plan: "p" }));
      act(() => result.current.clearAskUser());
      act(() => result.current.clearExitPlan());
      expect(result.current.askUserQuestions).toBeNull();
      expect(result.current.exitPlanRequest).toBeNull();
      expect(clearAskUserPill).toHaveBeenCalledWith("ch");
      expect(clearPlanPill).toHaveBeenCalledWith("ch");
    });

    it("clears cards without pills or a channel", async () => {
      const clearAskUserPill = vi.fn();
      const { result } = setup({ ch: null, options: { clearAskUserPill } });
      act(() => result.current.clearAskUser());
      act(() => result.current.clearExitPlan());
      expect(clearAskUserPill).not.toHaveBeenCalled();

      const bare = await ready();
      act(() => bare.result.current.clearAskUser());
      act(() => bare.result.current.clearExitPlan());
      expect(bare.result.current.askUserQuestions).toBeNull();
    });
  });
});
