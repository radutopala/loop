// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { listPendingAsks, listPendingPlans, type PendingAsk, type PendingPlan } from "../api/channels";
import { listPendingApprovals, type PendingApproval } from "../api/gate";
import { listReviewSessions, type ReviewSessionSummary } from "../api/review";
import { deferred } from "../test/hooks-b-fixtures";
import type { WSEvent } from "../types";
import { applyEvent, confirmedIdle, createEmptyState, useChatStateStore } from "./useChatStateStore";
import { useWebSocketConnection } from "./useWebSocketConnection";

vi.mock("../api/channels", () => ({ listPendingAsks: vi.fn(), listPendingPlans: vi.fn() }));
vi.mock("../api/gate", () => ({ listPendingApprovals: vi.fn() }));
vi.mock("../api/review", () => ({ listReviewSessions: vi.fn() }));
vi.mock("./useWebSocketConnection", () => ({ useWebSocketConnection: vi.fn() }));

function runningState() {
  const state = createEmptyState();
  applyEvent(state, {
    type: "agent.status",
    channel_id: "ch1",
    data: { status: "running", run_id: "run-1", msg_id: "m1" },
    timestamp: 0,
  } as WSEvent);
  return state;
}

function processed(msgIds: string[]): WSEvent {
  return { type: "messages.processed", channel_id: "ch1", data: { msg_ids: msgIds }, timestamp: 0 } as WSEvent;
}

describe("applyEvent messages.processed", () => {
  // A channel that finishes while another one is open only sees its events
  // through the store; if agent.status "done" is missed, messages.processed
  // must still clear the stop button before the view remounts from it.
  const cases: { name: string; msgIds: string[]; wantProcessing: string | null }[] = [
    { name: "the turn's own message", msgIds: ["m1"], wantProcessing: null },
    { name: "a different message", msgIds: ["m2"], wantProcessing: "m1" },
  ];
  for (const tc of cases) {
    it(`clears the run for ${tc.name}`, () => {
      const state = runningState();
      expect(state.isRunning).toBe(true);
      applyEvent(state, processed(tc.msgIds));
      expect(state.isRunning).toBe(false);
      expect(state.runId).toBeNull();
      expect(state.processingMsgId).toBe(tc.wantProcessing);
    });
  }
});

describe("confirmedIdle", () => {
  // Only a fetch that started after the last "running" event can prove the
  // run ended; an older one may predate the run and hide a live Stop.
  const cases: { name: string; agentRunning: boolean; fetchStartedAt: number; lastRunningAt: number | undefined; want: boolean }[] = [
    { name: "idle, fetched after the run started", agentRunning: false, fetchStartedAt: 200, lastRunningAt: 100, want: true },
    { name: "idle, no run seen", agentRunning: false, fetchStartedAt: 200, lastRunningAt: undefined, want: true },
    { name: "idle, fetched before the run started", agentRunning: false, fetchStartedAt: 100, lastRunningAt: 200, want: false },
    { name: "idle, fetched at the same instant", agentRunning: false, fetchStartedAt: 100, lastRunningAt: 100, want: false },
    { name: "still running", agentRunning: true, fetchStartedAt: 200, lastRunningAt: 100, want: false },
  ];
  for (const tc of cases) {
    it(tc.name, () => {
      expect(confirmedIdle(tc.agentRunning, tc.fetchStartedAt, tc.lastRunningAt)).toBe(tc.want);
    });
  }
});

describe("applyEvent background tasks", () => {
  const watch = [{ id: "b1", type: "local_bash", description: "watch CI", started_at: "2026-10-16T09:00:00Z" }];
  function activity(kind: string, description = "", tasks?: unknown[]): WSEvent {
    return { type: "agent.activity", channel_id: "ch1", data: { activity: kind, description, tasks }, timestamp: 0 } as WSEvent;
  }

  it("keeps them apart from the activity other events replace", () => {
    const state = runningState();
    applyEvent(state, activity("background_tasks", "1 background task: watch CI", watch));
    applyEvent(state, activity("thinking", "120"));
    expect(state.backgroundTasks).toEqual(watch);
    expect(state.agentActivity?.activity).toBe("thinking");
  });

  const clears: { name: string; event: WSEvent }[] = [
    { name: "none are left", event: activity("background_tasks") },
    { name: "the run ends", event: { type: "agent.status", channel_id: "ch1", data: { status: "completed", run_id: "run-1" }, timestamp: 0 } as WSEvent },
    { name: "the turn is processed", event: processed(["m1"]) },
  ];
  for (const tc of clears) {
    it(`clears them when ${tc.name}`, () => {
      const state = runningState();
      applyEvent(state, activity("background_tasks", "1 background task: watch CI", watch));
      applyEvent(state, tc.event);
      expect(state.backgroundTasks).toBeNull();
    });
  }
});

describe("applyEvent", () => {
  const ev = (type: string, data: unknown = {}): WSEvent => ({ type, channel_id: "ch1", data, timestamp: 0 });

  it("follows the stream, tools, plan mode, cards and tasks", () => {
    const state = createEmptyState();
    applyEvent(state, ev("message.streaming", { content: "hel" }));
    expect(state.streamingContent).toBe("hel");
    applyEvent(state, ev("message.created", { is_bot: false }));
    expect(state.streamingContent).toBe("hel");
    applyEvent(state, ev("message.created", { is_bot: true }));
    expect(state.streamingContent).toBeNull();

    applyEvent(state, ev("tool.use", { tool_name: "EnterPlanMode", input: "" }));
    expect(state.mode).toBe("plan");
    applyEvent(state, ev("tool.use", { tool_name: "Bash", input: "ls" }));
    expect(state).toMatchObject({ mode: "plan", toolActivity: { tool_name: "Bash", input: "ls" } });
    applyEvent(state, ev("tool.use", { tool_name: "ExitPlanMode", input: "" }));
    expect(state.mode).toBe("agent");

    applyEvent(state, ev("agent.ask_user", { questions: [] }));
    applyEvent(state, ev("agent.exit_plan", { plan: "p" }));
    applyEvent(state, ev("agent.tasks", { tasks: [] }));
    expect(state).toMatchObject({ askUserQuestions: { questions: [] }, exitPlanRequest: { plan: "p" }, agentTasks: { tasks: [] } });
    applyEvent(state, ev("agent.ask_resolved"));
    applyEvent(state, ev("agent.plan_resolved"));
    expect(state).toMatchObject({ askUserQuestions: null, exitPlanRequest: null });
    applyEvent(state, ev("channel.renamed"));
  });

  it("keys gate approvals by source and drops resolved ones", () => {
    const state = createEmptyState();
    applyEvent(state, ev("gate.approval_requested", { req_id: "g1" }));
    applyEvent(state, ev("gate.approval_requested", { req_id: "g2", source: "terminal:a" }));
    applyEvent(state, ev("gate.approval_requested", { req_id: "g3", source: "" }));
    expect(Object.keys(state.gateApprovals)).toEqual(["chat", "terminal:a"]);
    const before = state.gateApprovals;
    applyEvent(state, ev("gate.approval_resolved", { req_id: "nope" }));
    expect(state.gateApprovals).toBe(before);
    applyEvent(state, ev("gate.approval_resolved", { req_id: "g2" }));
    expect(Object.keys(state.gateApprovals)).toEqual(["chat"]);
  });

  it("starts a run without a run id or trigger", () => {
    const state = createEmptyState();
    applyEvent(state, ev("agent.status", { status: "running", trigger_content: "" }));
    expect(state).toMatchObject({ isRunning: true, runId: null, triggerContent: null, processingMsgId: null });
    applyEvent(state, ev("agent.status", { status: "running", trigger_content: "go" }));
    expect(state.triggerContent).toBe("go");
  });

  it("ends only the tracked run, dropping the chat's approval and keeping terminal ones", () => {
    const state = runningState();
    applyEvent(state, ev("gate.approval_requested", { req_id: "g2", source: "terminal:a" }));
    applyEvent(state, ev("agent.status", { status: "completed", run_id: "other" }));
    expect(state.isRunning).toBe(true);
    applyEvent(state, ev("agent.status", { status: "error" }));
    expect(state.isRunning).toBe(false);
    expect(Object.keys(state.gateApprovals)).toEqual(["terminal:a"]);

    applyEvent(state, ev("gate.approval_requested", { req_id: "g1" }));
    applyEvent(state, ev("agent.status", { status: "completed", run_id: "r9" }));
    expect(Object.keys(state.gateApprovals)).toEqual(["terminal:a"]);
  });

  it.each([
    ["a duration", { duration_ms: 3 }, { duration_ms: 3 }],
    ["a stop reason", { stop_reason: "end" }, { stop_reason: "end" }],
    ["neither", {}, null],
  ])("records completion info for a completed run with %s", (_name, extra, want) => {
    const state = createEmptyState();
    applyEvent(state, ev("agent.status", { status: "completed", ...extra }));
    if (want) expect(state.completionInfo).toMatchObject(want);
    else expect(state.completionInfo).toBeNull();
  });

  it("clears a processed run that had no processing message", () => {
    const state = createEmptyState();
    applyEvent(state, processed(["x"]));
    expect(state.processingMsgId).toBeNull();
  });
});

describe("useChatStateStore", () => {
  type Props = Parameters<typeof useChatStateStore>[0];
  type Channel = Props["channels"][number];
  const ch = (id: string, agent_running = false, name = `#${id}`): Channel => ({ id, name, agent_running });
  const ev = (type: string, data: unknown = {}, channelId = "sel"): WSEvent => ({ type, channel_id: channelId, data, timestamp: 0 });
  const status = (s: string, extra: Record<string, unknown> = {}, channelId = "sel") => ev("agent.status", { status: s, ...extra }, channelId);
  const approval = (req_id: string, channel_id: string, source?: string): PendingApproval => ({ req_id, channel_id, container_id: "c", kind: "net", target: "t", source });

  let notifications: { title: string; body: string }[];
  let loopAPI: {
    reconcileApprovals: ReturnType<typeof vi.fn>;
    notifyTurnEnd: ReturnType<typeof vi.fn>;
    notifyApprovalNeeded: ReturnType<typeof vi.fn>;
    notifyApprovalResolved: ReturnType<typeof vi.fn>;
  };

  function stubNotification(permission: string) {
    const requestPermission = vi.fn();
    vi.stubGlobal(
      "Notification",
      Object.assign(
        class {
          constructor(title: string, opts: { body: string }) {
            notifications.push({ title, body: opts.body });
          }
        },
        { permission, requestPermission },
      ),
    );
    return requestPermission;
  }

  function setup(props: Partial<Props> = {}) {
    const onAppEvent = vi.fn();
    const send = vi.fn();
    vi.mocked(useWebSocketConnection).mockReturnValue({ send } as unknown as ReturnType<typeof useWebSocketConnection>);
    let current: Props = { channels: [], selectedId: "sel", onAppEvent, ...props };
    const hook = renderHook((p: Props) => useChatStateStore(p), { initialProps: current });
    const wsOpts = () => vi.mocked(useWebSocketConnection).mock.calls.at(-1)![0];
    const emit = (...events: (WSEvent | string)[]) =>
      act(() => {
        for (const e of events) wsOpts().onMessage!({ data: typeof e === "string" ? e : JSON.stringify(e) } as MessageEvent);
      });
    const open = async () => {
      const sock = { send: vi.fn() };
      await act(async () => wsOpts().onOpen!(sock as unknown as WebSocket));
      return sock;
    };
    const update = (next: Partial<Props>) => {
      current = { ...current, ...next };
      hook.rerender(current);
    };
    const pill = (kind: Parameters<typeof hook.result.current.syncPill>[0]) => [...(hook.result.current.pillsRef.current.get(kind) ?? [])].sort();
    const listen = () => {
      const listener = vi.fn();
      hook.result.current.subscribeChatEvents(listener);
      return listener;
    };
    return { ...hook, onAppEvent, send, emit, open, update, pill, listen, wsOpts };
  }

  beforeEach(() => {
    notifications = [];
    loopAPI = { reconcileApprovals: vi.fn(), notifyTurnEnd: vi.fn(), notifyApprovalNeeded: vi.fn(), notifyApprovalResolved: vi.fn() };
    vi.stubGlobal("loopAPI", loopAPI);
    stubNotification("granted");
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.mocked(listPendingApprovals).mockResolvedValue([]);
    vi.mocked(listPendingAsks).mockResolvedValue([]);
    vi.mocked(listPendingPlans).mockResolvedValue([]);
    vi.mocked(listReviewSessions).mockResolvedValue([]);
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    for (const fn of [listPendingApprovals, listPendingAsks, listPendingPlans, listReviewSessions, useWebSocketConnection]) vi.mocked(fn).mockReset();
  });

  describe("setup", () => {
    it("opens the WS and asks for notification permission once it's undecided", () => {
      const ask = stubNotification("default");
      const { wsOpts } = setup();
      expect(wsOpts()).toMatchObject({ path: "/api/ws", enabled: true });
      expect(ask).toHaveBeenCalled();
    });

    it("doesn't ask when the permission is decided or notifications are missing", () => {
      const ask = stubNotification("denied");
      setup();
      cleanup();
      vi.stubGlobal("Notification", undefined);
      Reflect.deleteProperty(window, "Notification");
      setup();
      expect(ask).not.toHaveBeenCalled();
    });

    it("seeds running channels from the channel list without replacing a live run id", () => {
      const { result, emit, update } = setup();
      emit(status("running", { run_id: "r1" }, "a"));
      update({ channels: [ch("a", true), ch("b", true), ch("c")] });
      expect([...result.current.isRunningMapRef.current]).toEqual([
        ["a", "r1"],
        ["b", ""],
      ]);
    });
  });

  describe("confirmed idle channels", () => {
    it("ends a leftover run once a later channel list shows the channel idle", () => {
      const { result, update, emit } = setup({ channels: [ch("sel", true), ch("bg", true), ch("gone", true)] });
      const listener = result.current.subscribeChatEvents(vi.fn()) && vi.fn();
      result.current.subscribeChatEvents(listener);
      emit(ev("message.streaming", { content: "x" }, "sel"), ev("message.streaming", { content: "y" }, "bg"));
      result.current.saveState("sel", { ...result.current.getState("sel")!, isRunning: true });
      listener.mockClear();

      update({ channels: [ch("sel"), ch("bg"), ch("gone"), ch("idle")], channelsFetchedAt: performance.now() + 1000 });
      expect(result.current.isRunningMapRef.current.size).toBe(0);
      expect(result.current.getState("sel")!.isRunning).toBe(false);
      expect(listener).toHaveBeenCalledWith(expect.objectContaining({ type: "agent.status", channel_id: "sel", data: { status: "completed" } }));
      expect(listener).toHaveBeenCalledTimes(1);
    });

    it("ends a stored run the map doesn't track", () => {
      const { result, update } = setup();
      result.current.saveState("a", { ...createEmptyState(), isRunning: true });
      result.current.isRunningMapRef.current.clear();
      update({ channels: [ch("a")], channelsFetchedAt: performance.now() + 1000 });
      expect(result.current.getState("a")!.isRunning).toBe(false);
    });

    it("keeps a run that started after the list was fetched, or that the list still shows", () => {
      const { result, update, emit } = setup();
      emit(status("running", { run_id: "r1" }, "a"), status("running", { run_id: "r2" }, "b"));
      update({ channels: [ch("a"), ch("b", true)], channelsFetchedAt: 0 });
      expect([...result.current.isRunningMapRef.current.keys()]).toEqual(["a", "b"]);
    });
  });

  describe("WS open", () => {
    it("subscribes, rehydrates and counts the opens", async () => {
      const { result, open, emit } = setup();
      emit(status("running", {}, "bg"));
      const done = deferred<PendingApproval[]>();
      vi.mocked(listPendingApprovals).mockReturnValueOnce(done.promise);
      const sock = await open();
      expect(sock.send).toHaveBeenCalledWith(JSON.stringify({ type: "subscribe", channels: ["bg", "sel"] }));
      expect(window.__loopWsRehydrated).toBe(false);
      expect(result.current.wsOpens).toBe(1);
      await act(async () => done.resolve([]));
      expect(window.__loopWsRehydrated).toBe(true);
      expect(listReviewSessions).toHaveBeenCalled();
      expect(listPendingAsks).toHaveBeenCalled();
      expect(listPendingPlans).toHaveBeenCalled();
    });

    it("logs failed rehydrates and still settles", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      for (const fn of [listPendingApprovals, listPendingAsks, listPendingPlans, listReviewSessions]) vi.mocked(fn).mockRejectedValue(new Error("down"));
      const { open } = setup();
      await open();
      for (const what of ["listPendingApprovals", "listPendingAsks", "listPendingPlans", "listReviewSessions"]) {
        expect(warn).toHaveBeenCalledWith(`[rehydrate] ${what} failed:`, expect.any(Error));
      }
      expect(window.__loopWsRehydrated).toBe(true);
    });
  });

  describe("gate rehydrate", () => {
    it("drops approvals the gate no longer has and adds the ones it missed", async () => {
      const { result, emit, open, pill, listen } = setup();
      const listener = listen();
      emit(
        ev("gate.approval_requested", { req_id: "gone-sel" }, "sel"),
        ev("gate.approval_requested", { req_id: "gone-bg" }, "bg"),
        ev("gate.approval_requested", { req_id: "kept", source: "terminal:a" }, "bg"),
      );
      expect(pill("gate")).toEqual(["bg", "sel"]);
      listener.mockClear();

      vi.mocked(listPendingApprovals).mockResolvedValue([approval("kept", "bg", "terminal:a"), approval("new-sel", "sel", "terminal:b"), approval("new", "fresh")]);
      await open();
      expect(Object.keys(result.current.getState("bg")!.gateApprovals)).toEqual(["terminal:a"]);
      expect(result.current.getState("sel")!.gateApprovals).toEqual({ "terminal:b": approval("new-sel", "sel", "terminal:b") });
      expect(result.current.getState("fresh")!.gateApprovals.chat!.req_id).toBe("new");
      expect(listener.mock.calls.map(([e]) => [e.type, e.data.req_id])).toEqual([
        ["gate.approval_resolved", "gone-sel"],
        ["gate.approval_requested", "new-sel"],
      ]);
      expect(loopAPI.reconcileApprovals).toHaveBeenCalledWith(["kept", "new-sel", "new"]);
      expect(pill("gate")).toEqual(["bg", "fresh", "sel"]);
    });

    it("keeps approvals injected by the test hook", async () => {
      const { result, open } = setup();
      act(() => {
        window.dispatchEvent(new CustomEvent("loop:test-event", { detail: JSON.stringify(ev("gate.approval_requested", { req_id: "fake" })) }));
        window.dispatchEvent(new CustomEvent("loop:test-event", { detail: JSON.stringify(ev("gate.approval_requested", { source: "other" })) }));
        window.dispatchEvent(new CustomEvent("loop:test-event", { detail: "{not json" }));
      });
      expect(result.current.getState("sel")!.gateApprovals.chat!.req_id).toBe("fake");
      await open();
      expect(result.current.getState("sel")!.gateApprovals.chat!.req_id).toBe("fake");
    });

    it("works without the app bridge", async () => {
      vi.stubGlobal("loopAPI", undefined);
      vi.mocked(listPendingApprovals).mockResolvedValue([approval("a", "sel")]);
      const { result, open, emit } = setup();
      await open();
      emit(ev("gate.approval_resolved", { req_id: "a" }), status("completed"));
      expect(result.current.getState("sel")!.gateApprovals).toEqual({});
    });

    it("rehydrates when the window comes back, until unmount", async () => {
      const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
      const { unmount } = setup();
      await act(async () => {
        document.dispatchEvent(new Event("visibilitychange"));
      });
      expect(listPendingApprovals).not.toHaveBeenCalled();
      hidden.mockReturnValue(false);
      await act(async () => {
        document.dispatchEvent(new Event("visibilitychange"));
        window.dispatchEvent(new Event("focus"));
      });
      expect(listPendingApprovals).toHaveBeenCalledTimes(2);
      unmount();
      window.dispatchEvent(new Event("focus"));
      expect(listPendingApprovals).toHaveBeenCalledTimes(2);
    });

    it("sweeps every 30s while a gate card shows", async () => {
      vi.useFakeTimers();
      const { emit } = setup();
      await act(async () => vi.advanceTimersByTimeAsync(30_000));
      expect(listPendingApprovals).not.toHaveBeenCalled();
      emit(ev("gate.approval_requested", { req_id: "g" }));
      await act(async () => vi.advanceTimersByTimeAsync(30_000));
      expect(listPendingApprovals).toHaveBeenCalledTimes(1);
    });
  });

  describe("ask and plan rehydrate", () => {
    const ask = (channel_id: string): PendingAsk => ({ channel_id, data: { questions: [] } });
    const plan = (channel_id: string): PendingPlan => ({ channel_id, data: { plan: `plan ${channel_id}` } });

    it("restores parked asks, and drops pills the backend no longer has", async () => {
      const { result, open, pill, listen, emit } = setup();
      const listener = listen();
      emit(ev("agent.ask_user", { questions: [] }, "old"), ev("message.streaming", { content: "" }, "bg"));
      act(() => result.current.syncPill("ask", new Set(["old", "nostate"])));
      vi.mocked(listPendingAsks).mockResolvedValue([ask("sel"), ask("bg")]);
      await open();
      expect(pill("ask")).toEqual(["bg", "sel"]);
      expect(result.current.getState("sel")!.askUserQuestions).toEqual({ questions: [] });
      expect(result.current.getState("old")!.askUserQuestions).toBeNull();
      expect(listener).toHaveBeenCalledWith(expect.objectContaining({ type: "agent.ask_user", channel_id: "sel" }));
      expect(listener).toHaveBeenCalledTimes(1);
    });

    it("restores parked plans, and drops cards and pills the backend no longer has", async () => {
      const { result, open, pill, listen, emit } = setup();
      const listener = listen();
      emit(ev("agent.exit_plan", { plan: "old" }, "old"), ev("message.streaming", { content: "" }, "idle"), ev("message.streaming", { content: "" }, "bg"));
      act(() => result.current.syncPill("plan", new Set(["old", "nostate"])));
      vi.mocked(listPendingPlans).mockResolvedValue([plan("sel"), plan("bg")]);
      await open();
      expect(pill("plan")).toEqual(["bg", "sel"]);
      expect(result.current.getState("bg")!.exitPlanRequest).toEqual({ plan: "plan bg" });
      expect(result.current.getState("old")!.exitPlanRequest).toBeNull();
      expect(listener).toHaveBeenCalledWith(expect.objectContaining({ type: "agent.exit_plan", channel_id: "sel" }));
      expect(listener).toHaveBeenCalledTimes(1);
    });

    it.each([
      ["asks", listPendingAsks, "ask"],
      ["plans", listPendingPlans, "plan"],
      ["reviews", listReviewSessions, "rev"],
    ] as const)("drops a %s snapshot a newer open overtook", async (_name, list, kind) => {
      const stale = deferred<never[]>();
      const row = kind === "rev" ? { channel_id: "late", status: "ready" } : { channel_id: "late", data: {} };
      vi.mocked(list as typeof listPendingAsks)
        .mockReturnValueOnce(stale.promise)
        .mockResolvedValueOnce([]);
      const { open, pill } = setup();
      await open();
      await open();
      await act(async () => stale.resolve([row] as never[]));
      expect(pill(kind)).toEqual([]);
    });
  });

  describe("review sessions", () => {
    const session = (channel_id: string, s: string) => ({ channel_id, status: s }) as ReviewSessionSummary;

    it("lights ready sessions except ones in view, and drops the rest", async () => {
      const { result, open, pill } = setup();
      act(() => result.current.syncPill("rev", new Set(["stale"])));
      act(() => void result.current.registerReviewView("viewing"));
      vi.mocked(listReviewSessions).mockResolvedValue([session("a", "ready"), session("viewing", "ready"), session("b", "running")]);
      await open();
      expect(pill("rev")).toEqual(["a"]);
    });

    it("follows review.status events, and a registered view clears its pill until it leaves", () => {
      const { result, emit, pill, onAppEvent } = setup();
      emit(ev("review.status", { status: "ready" }, "a"));
      expect(pill("rev")).toEqual(["a"]);
      expect(onAppEvent).toHaveBeenCalledWith(expect.objectContaining({ type: "review.status" }));
      emit(ev("review.status", { status: "running" }, "a"));
      expect(pill("rev")).toEqual([]);

      emit(ev("review.status", { status: "ready" }, "a"));
      let leave!: () => void;
      act(() => {
        leave = result.current.registerReviewView("a");
      });
      expect(pill("rev")).toEqual([]);
      emit(ev("review.status", { status: "ready" }, "a"));
      expect(pill("rev")).toEqual([]);
      leave();
      emit(ev("review.status", { status: "ready" }, "a"));
      expect(pill("rev")).toEqual(["a"]);
    });
  });

  describe("events", () => {
    it("ignores malformed frames", () => {
      const { onAppEvent, emit } = setup();
      emit("{nope");
      expect(onAppEvent).not.toHaveBeenCalled();
    });

    it("stores state only once a channel shows activity", () => {
      const { result, emit } = setup();
      emit(ev("message.created", { is_bot: true }, "a"));
      expect(result.current.getState("a")).toBeUndefined();
      emit(ev("tool.use", { tool_name: "Bash", input: "ls" }, "a"), ev("message.streaming", { content: "x" }, "a"));
      expect(result.current.getState("a")).toMatchObject({ toolActivity: { tool_name: "Bash" }, streamingContent: "x" });
    });

    it("forwards the selected channel's events, workflow events and sidebar events", () => {
      const { result, emit, onAppEvent, listen } = setup();
      const listener = listen();
      const watcher = vi.fn();
      const stop = result.current.subscribeChannelEvents("bg", watcher);
      emit(ev("message.streaming", { content: "s" }), ev("message.streaming", { content: "b" }, "bg"), ev("workflow.run_completed", {}, ""), ev("channel.updated", {}, "bg"));
      expect(listener.mock.calls.map(([e]) => e.type)).toEqual(["message.streaming", "workflow.run_completed"]);
      expect(watcher.mock.calls.map(([e]) => e.type)).toEqual(["message.streaming", "channel.updated"]);
      expect(onAppEvent.mock.calls.map(([e]) => `${e.type}@${e.channel_id}`)).toEqual(["message.streaming@sel", "workflow.run_completed@", "channel.updated@bg"]);
      stop();
    });

    it("mirrors ask and plan cards onto the sidebar pills", () => {
      const { emit, pill } = setup();
      emit(ev("agent.ask_user", { questions: [] }, "a"), ev("agent.exit_plan", { plan: "p" }, "a"));
      expect(pill("ask")).toEqual(["a"]);
      expect(pill("plan")).toEqual(["a"]);
      emit(ev("agent.ask_resolved", {}, "a"), ev("agent.plan_resolved", {}, "a"));
      expect(pill("ask")).toEqual([]);
      expect(pill("plan")).toEqual([]);
      emit(ev("agent.ask_resolved", {}, "nostate"), ev("agent.plan_resolved", {}, "nostate"), ev("gate.approval_resolved", { req_id: "x" }, "nostate"));
      expect(pill("ask")).toEqual([]);
    });
  });

  describe("approval notices", () => {
    it("notifies about approvals outside the open channel and bounces for every one", () => {
      const { emit, pill } = setup({ channels: [ch("bg", false, "Background")] });
      emit(ev("gate.approval_requested", { req_id: "g1", target: "example.com" }, "bg"), ev("gate.approval_requested", { req_id: "g2" }, "nameless"));
      expect(notifications).toEqual([
        { title: "Loop — Background", body: "Approval needed: example.com" },
        { title: "Loop — nameless", body: "Approval needed" },
      ]);
      expect(loopAPI.notifyApprovalNeeded.mock.calls).toEqual([["g1"], ["g2"]]);
      expect(pill("gate")).toEqual(["bg", "nameless"]);

      emit(ev("gate.approval_resolved", { req_id: "g1" }, "bg"));
      expect(loopAPI.notifyApprovalResolved).toHaveBeenCalledWith("g1");
      expect(pill("gate")).toEqual(["nameless"]);
    });

    it("notifies about the open channel's approvals only while the window is hidden", () => {
      const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
      const { emit } = setup({ channels: [ch("sel", false, "")] });
      emit(ev("gate.approval_requested", { req_id: "g1" }));
      expect(notifications).toEqual([]);
      hidden.mockReturnValue(true);
      emit(ev("gate.approval_requested", { req_id: "g2" }));
      expect(notifications).toEqual([{ title: "Loop — sel", body: "Approval needed" }]);
    });

    it("names a thread's approval after its parent when the thread isn't listed", () => {
      const { emit } = setup({ channels: [ch("parent", false, "Parent")] });
      emit(status("running", { thread_id: "thr" }, "parent"));
      emit(ev("gate.approval_requested", { req_id: "g1" }, "parent"));
      expect(notifications).toEqual([{ title: "Loop — Parent", body: "Approval needed" }]);
    });
  });

  describe("runs", () => {
    it("tracks running channels by run id and re-subscribes when they change", () => {
      vi.useFakeTimers();
      const { result, emit, send } = setup();
      expect(send).toHaveBeenLastCalledWith(JSON.stringify({ type: "subscribe", channels: ["sel"] }));
      act(() => vi.advanceTimersByTime(500));
      const sends = send.mock.calls.length;
      act(() => vi.advanceTimersByTime(500));
      expect(send).toHaveBeenCalledTimes(sends);

      emit(status("running", { run_id: "r1" }, "a"), status("running", {}, "b"), status("running", { run_id: "r2" }, "a"));
      expect([...result.current.isRunningMapRef.current]).toEqual([
        ["a", "r2"],
        ["b", ""],
      ]);
      act(() => vi.advanceTimersByTime(500));
      expect(send).toHaveBeenLastCalledWith(JSON.stringify({ type: "subscribe", channels: ["a", "b", "sel"] }));
    });

    it.each([
      ["the tracked run", "r1", "r1", false],
      ["a run with no id", "r1", undefined, false],
      ["a seeded run", "", "r1", false],
      ["another run", "r1", "r2", true],
    ])("ends %s", (_name, tracked, finishing, stillRunning) => {
      const { result, emit } = setup();
      emit(status("running", tracked ? { run_id: tracked } : {}, "a"), status("completed", finishing ? { run_id: finishing } : {}, "a"));
      expect(result.current.isRunningMapRef.current.has("a")).toBe(stillRunning);
    });

    it("ignores the end of a run it never tracked", () => {
      const { result, emit } = setup();
      emit(status("completed", {}, "a"));
      expect(result.current.isRunningMapRef.current.size).toBe(0);
    });

    it("routes thread runs to the thread and clears a parent tracking the same run", () => {
      const { result, emit, listen } = setup({ selectedId: "parent" });
      const listener = listen();
      // The first run's "running" reached the parent before the thread existed.
      emit(status("running", { run_id: "r1" }, "parent"));
      emit(status("running", { run_id: "r1", thread_id: "thr" }, "parent"));
      expect(result.current.getState("thr")!.isRunning).toBe(true);
      listener.mockClear();

      emit(status("completed", { run_id: "r1", thread_id: "thr" }, "parent"));
      expect(result.current.isRunningMapRef.current.size).toBe(0);
      expect(result.current.getState("parent")!.isRunning).toBe(false);
      expect(result.current.getState("thr")!.isRunning).toBe(false);
      expect(listener).toHaveBeenCalledTimes(1);
    });

    it("clears a seeded parent without stored state, and keeps a parent on another run", () => {
      const { result, emit, update } = setup({ channels: [ch("p1", true)] });
      emit(status("running", { run_id: "r1", thread_id: "t1" }, "p1"), status("completed", { run_id: "r1", thread_id: "t1" }, "p1"));
      expect(result.current.isRunningMapRef.current.has("p1")).toBe(false);

      update({ channels: [] });
      emit(status("running", { run_id: "own" }, "p2"), status("running", { run_id: "r2", thread_id: "t2" }, "p2"), status("completed", { run_id: "r2", thread_id: "t2" }, "p2"));
      emit(status("completed", { thread_id: "t3" }, "p3"));
      expect([...result.current.isRunningMapRef.current]).toEqual([["p2", "own"]]);
    });

    it("marks finished runs unread outside the open channel, or while hidden", () => {
      const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(false);
      const { result, emit } = setup();
      emit(status("completed", {}, "sel"));
      expect(result.current.unreadCount).toBe(0);
      emit(status("completed", {}, "a"), status("completed", { trigger: "learn" }, "b"), status("completed", { thread_id: "thr" }, "a"));
      expect([...result.current.unreadIdsRef.current]).toEqual(["a", "thr"]);
      hidden.mockReturnValue(true);
      emit(status("error", {}, "sel"));
      expect(result.current.unreadCount).toBe(3);

      act(() => result.current.markRead("a"));
      expect(result.current.unreadCount).toBe(2);
      act(() => result.current.markAllRead());
      expect(result.current.unreadCount).toBe(0);
    });

    it("notifies and bounces for runs the user started", () => {
      const { emit } = setup({ channels: [ch("a", false, "Alpha"), ch("thr", false, "Thread")] });
      emit(
        status("completed", { duration_ms: 2400 }, "a"),
        status("completed", {}, "nameless"),
        status("error", { error: "boom", thread_id: "thr" }, "a"),
        status("error", { thread_id: "unlisted" }, "a"),
        status("completed", { trigger: "scheduled" }, "a"),
        status("completed", { trigger: "bot" }, "a"),
        status("completed", { trigger: "learn" }, "a"),
        status("completed"),
      );
      expect(notifications).toEqual([
        { title: "Loop — Alpha", body: "Completed in 2s" },
        { title: "Loop — nameless", body: "Completed in 0s" },
        { title: "Loop — Thread", body: "Error: boom" },
        { title: "Loop — Alpha", body: "Error: unknown" },
        { title: "Loop — Alpha", body: "Completed in 0s" },
      ]);
      expect(loopAPI.notifyTurnEnd).toHaveBeenCalledTimes(5);
    });
  });

  describe("store API", () => {
    it("saves, reads and removes channel state, tracking saved runs", () => {
      const { result } = setup();
      const state = { ...createEmptyState(), isRunning: true, runId: "r1" };
      result.current.saveState("a", state);
      result.current.saveState("b", { ...createEmptyState(), isRunning: true });
      result.current.saveState("c", createEmptyState());
      expect(result.current.getState("a")).toBe(state);
      expect([...result.current.isRunningMapRef.current]).toEqual([
        ["a", "r1"],
        ["b", ""],
      ]);
      result.current.removeState("a");
      expect(result.current.getState("a")).toBeUndefined();
    });

    it("stops forwarding to an unsubscribed listener", () => {
      const { result, emit } = setup();
      const listener = vi.fn();
      const stop = result.current.subscribeChatEvents(listener);
      stop();
      emit(ev("message.streaming", { content: "x" }));
      expect(listener).not.toHaveBeenCalled();
    });

    it("keeps a watched channel subscribed while any of its listeners remain", () => {
      vi.useFakeTimers();
      const { result, send, emit } = setup();
      const first = vi.fn();
      const second = vi.fn();
      const stopFirst = result.current.subscribeChannelEvents("w", first);
      expect(send).toHaveBeenLastCalledWith(JSON.stringify({ type: "subscribe", channels: ["sel", "w"] }));
      const sends = send.mock.calls.length;
      const stopSecond = result.current.subscribeChannelEvents("w", second);
      expect(send).toHaveBeenCalledTimes(sends);

      stopFirst();
      emit(ev("message.streaming", { content: "x" }, "w"));
      expect(first).not.toHaveBeenCalled();
      expect(second).toHaveBeenCalled();
      stopSecond();
      stopSecond();
      act(() => vi.advanceTimersByTime(500));
      expect(send).toHaveBeenLastCalledWith(JSON.stringify({ type: "subscribe", channels: ["sel"] }));
    });

    it("clears ask and plan pills with their stored cards", () => {
      const { result, emit, pill } = setup();
      emit(ev("agent.ask_user", { questions: [] }, "a"), ev("agent.exit_plan", { plan: "p" }, "a"));
      act(() => result.current.clearAskUserPill("a"));
      act(() => result.current.clearPlanPill("a"));
      expect(pill("ask")).toEqual([]);
      expect(pill("plan")).toEqual([]);
      expect(result.current.getState("a")).toMatchObject({ askUserQuestions: null, exitPlanRequest: null });
      act(() => result.current.clearAskUserPill("nostate"));
      act(() => result.current.clearPlanPill("nostate"));
      expect(result.current.getState("nostate")).toBeUndefined();
    });

    it("syncs a pill to an exact set, leaving an equal one as is", () => {
      const { result, pill } = setup();
      act(() => result.current.syncPill("ci", new Set(["a", "b"])));
      expect(pill("ci")).toEqual(["a", "b"]);
      const before = result.current;
      act(() => result.current.syncPill("ci", new Set(["b", "a"])));
      expect(result.current).toBe(before);
      act(() => result.current.syncPill("ci", new Set(["a", "c"])));
      expect(pill("ci")).toEqual(["a", "c"]);
      act(() => result.current.syncPill("ci", new Set(["a"])));
      expect(pill("ci")).toEqual(["a"]);
    });
  });
});
