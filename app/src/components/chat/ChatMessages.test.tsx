import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, type Mock, vi } from "vitest";
import { resolveAsk, resolvePlan } from "../../api/channels";
import { resolveGateApproval } from "../../api/loopApi";
import type { ChatState } from "../../hooks/useChatState";
import type { Message, TimelineItem } from "../../types";
import { ChatMessages, type ChatMessagesHandle, type ChatMessagesProps } from "./ChatMessages";
import { renderInputWithLinks } from "./chatShared";
import { formatMessageTimestamp } from "./timestamps";

// Pass-through spies that count renders: a bubble formats its timestamp and a
// tool call renders its input on every render.
vi.mock("./timestamps", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./timestamps")>();
  return { ...actual, formatMessageTimestamp: vi.fn(actual.formatMessageTimestamp) };
});
vi.mock("./chatShared", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./chatShared")>();
  return { ...actual, renderInputWithLinks: vi.fn(actual.renderInputWithLinks) };
});

vi.mock("../../api/channels", () => ({
  formatAskAnswers: vi.fn(() => ""),
  resolveAsk: vi.fn(),
  resolvePlan: vi.fn(),
}));

vi.mock("../../api/loopApi", () => ({
  deleteQueuedMessage: vi.fn(),
  reorderQueuedMessages: vi.fn(),
  resolveGateApproval: vi.fn(),
  sendCommand: vi.fn(),
  sendMessage: vi.fn(),
  steerQueuedMessage: vi.fn(),
}));

function msg(id: number, msgId: string, extra: Partial<Message> = {}): Message {
  return {
    id,
    channel_id: "ch-1",
    msg_id: msgId,
    author_id: "u-1",
    author_name: "user",
    content: `text of ${msgId}`,
    is_bot: false,
    is_processed: true,
    created_at: "2026-01-01T10:00:00Z",
    ...extra,
  };
}

const item = (m: Message): TimelineItem => ({ kind: "message", position: m.id, id: m.id, data: m, trigger_msg_id: m.trigger_msg_id });
const thinking = (id: number, text: string, trigger?: string): TimelineItem => ({ kind: "thinking", position: id, id, text, trigger_msg_id: trigger });
const toolUse = (id: number, name: string, toolUseId: string, trigger?: string): TimelineItem => ({
  kind: "tool_use",
  position: id,
  id,
  tool_use_id: toolUseId,
  tool_name: name,
  tool_input: `input of ${name}`,
  trigger_msg_id: trigger,
});
const toolResult = (id: number, text: string, toolUseId: string, trigger?: string): TimelineItem => ({
  kind: "tool_result",
  position: id,
  id,
  tool_use_id: toolUseId,
  text,
  trigger_msg_id: trigger,
});
const compacting = (id: number): TimelineItem => ({ kind: "compacting", position: id, id });

function chatState(overrides: Partial<ChatState> = {}): ChatState {
  const items = overrides.items ?? [];
  const liveTail = overrides.liveTail ?? [];
  return {
    items,
    liveTail,
    messages: [...items, ...liveTail].flatMap((it) => (it.kind === "message" ? [it.data] : [])),
    loading: false,
    loadMore: vi.fn(),
    hasMore: false,
    streamingContent: null,
    isRunning: false,
    toolActivity: null,
    agentActivity: null,
    backgroundTasks: null,
    askUserQuestions: null,
    exitPlanRequest: null,
    agentTasks: null,
    clearAskUser: vi.fn(),
    clearExitPlan: vi.fn(),
    mode: "agent",
    setMode: vi.fn(),
    completionInfo: null,
    triggerContent: null,
    gateApprovals: {},
    clearGateApproval: vi.fn(),
    processingMsgId: null,
    queuedMessages: [],
    ...overrides,
  };
}

interface FakeIntersection {
  callback: IntersectionObserverCallback;
  options?: IntersectionObserverInit;
  observed: Element[];
  disconnect: Mock<() => void>;
}

interface FakeResize {
  callback: ResizeObserverCallback;
  disconnect: Mock<() => void>;
}

// Stands in for the browser APIs jsdom leaves out (observers, the CSS Custom
// Highlight API) and takes over animation frames so tests run them on demand.
function installBrowser({ highlights = true }: { highlights?: boolean } = {}) {
  const frames: FrameRequestCallback[] = [];
  const intersections: FakeIntersection[] = [];
  const resizes: FakeResize[] = [];
  const registry = new Map<string, { ranges: Range[] }>();
  const cancelFrame = vi.fn();
  let frameId = 0;
  vi.stubGlobal(
    "requestAnimationFrame",
    vi.fn((cb: FrameRequestCallback) => {
      frames.push(cb);
      frameId += 1;
      return frameId;
    }),
  );
  vi.stubGlobal("cancelAnimationFrame", cancelFrame);
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      rec: FakeIntersection;
      constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
        this.rec = { callback, options, observed: [], disconnect: vi.fn<() => void>() };
        intersections.push(this.rec);
      }
      observe(el: Element) {
        this.rec.observed.push(el);
      }
      disconnect() {
        this.rec.disconnect();
      }
    },
  );
  vi.stubGlobal(
    "ResizeObserver",
    class {
      rec: FakeResize;
      constructor(callback: ResizeObserverCallback) {
        this.rec = { callback, disconnect: vi.fn<() => void>() };
        resizes.push(this.rec);
      }
      observe() {}
      disconnect() {
        this.rec.disconnect();
      }
    },
  );
  vi.stubGlobal("CSS", highlights ? { highlights: registry } : {});
  vi.stubGlobal(
    "Highlight",
    class {
      ranges: Range[];
      constructor(...ranges: Range[]) {
        this.ranges = ranges;
      }
    },
  );
  const flushFrames = () =>
    act(() => {
      for (const cb of frames.splice(0)) cb(0);
    });
  return { frames, flushFrames, intersections, resizes, registry, cancelFrame };
}

function rect(top: number, height: number): DOMRect {
  return { top, bottom: top + height, height, left: 0, right: 100, width: 100, x: 0, y: top, toJSON: () => ({}) } as DOMRect;
}

// Lays the chat out: the scroll container spans 0..100, and each message
// starts where tops says (10 when unlisted) and is heights tall (20 when
// unlisted). scrollHeight is 100 per rendered message, so it follows the DOM.
function fakeLayout(layout: { tops?: Record<string, number>; heights?: Record<string, number>; clientHeight?: number; containerHeight?: number } = {}) {
  const tops = layout.tops ?? {};
  const scrollTops = new WeakMap<Element, number>();
  vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
    const uuid = (this as HTMLElement).dataset?.msgUuid;
    if (uuid === undefined) return rect(0, layout.containerHeight ?? 100);
    return rect(tops[uuid] ?? 10, layout.heights?.[uuid] ?? 20);
  });
  vi.spyOn(Element.prototype, "scrollHeight", "get").mockImplementation(function (this: Element) {
    return this.querySelectorAll("[data-msg-id]").length * 100;
  });
  vi.spyOn(Element.prototype, "clientHeight", "get").mockImplementation(() => layout.clientHeight ?? 100);
  vi.spyOn(Element.prototype, "scrollTop", "get").mockImplementation(function (this: Element) {
    return scrollTops.get(this) ?? 0;
  });
  vi.spyOn(Element.prototype, "scrollTop", "set").mockImplementation(function (this: Element, v: number) {
    scrollTops.set(this, v);
  });
  return { tops };
}

function renderMessages(state: ChatState, props: Partial<ChatMessagesProps> = {}) {
  const ref = createRef<ChatMessagesHandle>();
  const view = render(<ChatMessages ref={ref} channelId="ch-1" chatState={state} {...props} />);
  // The scroll container is the provider's first element.
  const scroller = view.container.firstElementChild as HTMLDivElement;
  return { ...view, ref, scroller };
}

// textOrder asserts the texts appear in the rendered chat in this order.
function expectOrder(root: HTMLElement, texts: string[]) {
  const all = root.textContent ?? "";
  const at = texts.map((t) => all.indexOf(t));
  expect(at.every((i) => i >= 0)).toBe(true);
  expect([...at].sort((a, b) => a - b)).toEqual(at);
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("ChatMessages timeline", () => {
  it("groups each user message with the events its run produced", () => {
    installBrowser();
    const u1 = msg(2, "u1", { content: "first prompt" });
    const u2 = msg(9, "u2", { content: "queued prompt", is_processed: false });
    const items: TimelineItem[] = [
      thinking(1, "loose thought"),
      item(u1),
      thinking(3, "u1 thought", "u1"),
      item(msg(4, "b1", { is_bot: true, content: "bot reply", trigger_msg_id: "u1" })),
      toolUse(5, "Bash", "tu-1", "u1"),
      toolResult(6, "bash output", "tu-1", "u1"),
      item(u2),
      // Still u1's, but after the queued u2: it renders after u2 instead.
      thinking(10, "late thought", "u1"),
      thinking(11, "orphan thought", "gone"),
    ];
    const { container } = renderMessages(chatState({ items, queuedMessages: [u2] }));
    expectOrder(container, ["loose thought", "first prompt", "u1 thought", "bot reply", "Bash", "bash output", "queued prompt", "2 steps", "late thought", "orphan thought"]);
    // The tool's result folds into its pill: it isn't shown a second time.
    expect(screen.getAllByText("bash output")).toHaveLength(1);
    expect(screen.getByText(/2 thoughts/)).toBeTruthy();
    expect(screen.getByText("queued 1/1")).toBeTruthy();
  });

  it("lets a queued message before the trigger keep routing its events", () => {
    installBrowser();
    const q = msg(1, "q", { content: "early queued", is_processed: false });
    const items: TimelineItem[] = [
      item(q),
      item(msg(2, "u1", { content: "prompt" })),
      item(msg(3, "b1", { is_bot: true, content: "first reply", trigger_msg_id: "u1" })),
      thinking(4, "routed thought", "u1"),
    ];
    const { container } = renderMessages(chatState({ items, queuedMessages: [q] }));
    expectOrder(container, ["early queued", "prompt", "first reply", "routed thought"]);
  });

  it("drops a group left with only a result shown elsewhere", () => {
    installBrowser();
    const items: TimelineItem[] = [toolUse(1, "Read", "tu-9"), item(msg(2, "b1", { is_bot: true, content: "bot text" })), toolResult(3, "read output", "tu-9")];
    const { container } = renderMessages(chatState({ items }));
    expectOrder(container, ["Read", "read output", "bot text"]);
    expect(screen.getAllByText("read output")).toHaveLength(1);
  });

  it("renders the live tail after the loaded items", () => {
    installBrowser();
    const { container } = renderMessages(chatState({ items: [item(msg(1, "u1", { content: "loaded" }))], liveTail: [thinking(50, "live thought")] }));
    expectOrder(container, ["loaded", "live thought"]);
  });

  it("hides the latest compacted marker while compacting is under way", () => {
    installBrowser();
    const items = [compacting(1), item(msg(2, "u1")), compacting(3), thinking(4, "after")];
    const running = chatState({ items, isRunning: true, agentActivity: { activity: "compacting" } });
    const { rerender } = renderMessages(running);
    expect(screen.getAllByText("Compacted context")).toHaveLength(1);
    expect(screen.getByText("Compacting context...")).toBeTruthy();

    rerender(<ChatMessages channelId="ch-1" chatState={{ ...running, isRunning: false }} />);
    expect(screen.getAllByText("Compacted context")).toHaveLength(2);
    // The activity indicator shows only during a run.
    expect(screen.queryByText("Compacting context...")).toBeNull();
  });

  it("keeps every marker during a compacting run with none in the timeline", () => {
    installBrowser();
    renderMessages(chatState({ items: [thinking(1, "only thought")], isRunning: true, agentActivity: { activity: "compacting" } }));
    expect(screen.getByText("only thought")).toBeTruthy();
    expect(screen.queryByText("Compacted context")).toBeNull();
  });

  it("labels queued, adding and processing messages from the backend queue", () => {
    installBrowser();
    const p = msg(1, "p", { content: "running one", is_processed: false, is_running: true });
    const q1 = msg(2, "q1", { content: "waiting one", is_processed: false });
    const q2 = msg(3, "q2", { content: "added one", is_processed: false, adding: true });
    const items = [item(q2), item(p), item(q1)];
    const { container } = renderMessages(chatState({ items, isRunning: true, queuedMessages: [p, q1, q2] }));
    expect(screen.getByText("processing")).toBeTruthy();
    expect(screen.getByText("queued 1/1")).toBeTruthy();
    expect(screen.getByText("adding…")).toBeTruthy();
    // The message added to the running turn goes last.
    expectOrder(container.firstElementChild as HTMLElement, ["running one", "waiting one", "added one"]);
  });

  it("offers older messages and loads them on click", async () => {
    installBrowser();
    const state = chatState({ items: [item(msg(1, "u1"))], hasMore: true });
    const { rerender } = renderMessages(state);
    await userEvent.click(screen.getByRole("button", { name: "Load older messages" }));
    expect(state.loadMore).toHaveBeenCalledTimes(1);
    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, loading: true }} />);
    expect(screen.getByRole("button", { name: "Loading..." })).toBeTruthy();
  });

  it("shows the streaming reply, completion summary, task list and the bars above the queue", () => {
    installBrowser();
    const q = msg(2, "q", { content: "queued text", is_processed: false });
    const state = chatState({
      items: [item(msg(1, "u1"))],
      streamingContent: "partial answer",
      completionInfo: { model: "test-model", num_turns: 2 },
      agentTasks: { tasks: [{ id: "t1", subject: "write tests", status: "completed" }] },
      queuedMessages: [q],
    });
    const { rerender } = renderMessages(state, { aboveQueue: <div>status bar</div> });
    expect(screen.getByText("partial answer")).toBeTruthy();
    expect(screen.getByText("streaming...")).toBeTruthy();
    expect(screen.getByText("test-model · 2 turns")).toBeTruthy();
    expect(screen.getByText("Tasks 1/1")).toBeTruthy();
    expect(screen.getByText("status bar").parentElement?.className).toBe("loop-above-queue");

    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, isRunning: true, agentTasks: { tasks: [] } }} />);
    // The summary is the finished run's; an empty task list isn't shown.
    expect(screen.queryByText("test-model · 2 turns")).toBeNull();
    expect(screen.queryByText(/^Tasks/)).toBeNull();
  });
});

describe("ChatMessages cards", () => {
  it("clears the chat gate card once it's resolved and follows the latest message", async () => {
    const env = installBrowser();
    vi.mocked(resolveGateApproval).mockResolvedValue(undefined);
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const state = chatState({
      items: [item(msg(1, "u1"))],
      gateApprovals: { chat: { req_id: "req-1", kind: "network", target: "example.com:443" }, "terminal:a": { req_id: "req-2", kind: "network", target: "other:443" } },
    });
    renderMessages(state);
    expect(screen.getByText("example.com:443")).toBeTruthy();
    // The terminal's gate shows in its pane, not here.
    expect(screen.queryByText("other:443")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Allow once" }));
    await waitFor(() => expect(state.clearGateApproval).toHaveBeenCalledWith("chat"));
    env.flushFrames();
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: "smooth" });
  });

  it("clears the ask card when it's skipped", async () => {
    installBrowser();
    vi.mocked(resolveAsk).mockResolvedValue(undefined as never);
    const state = chatState({ askUserQuestions: { questions: [{ question: "Which one?", options: [{ label: "A" }] }] }, exitPlanRequest: { plan: "the plan" } });
    renderMessages(state);
    // A pending question hides the plan card until it's answered.
    expect(screen.queryByText("Approve & Execute")).toBeNull();
    await userEvent.click(within(screen.getByTestId("ask-user-card")).getByRole("button", { name: /skip/i }));
    await waitFor(() => expect(state.clearAskUser).toHaveBeenCalledTimes(1));
    expect(resolveAsk).toHaveBeenCalledWith("ch-1", "skip");
  });

  it("clears the plan card when it's approved", async () => {
    installBrowser();
    vi.mocked(resolvePlan).mockResolvedValue(undefined as never);
    const state = chatState({ exitPlanRequest: { plan: "the plan" } });
    renderMessages(state);
    await userEvent.click(screen.getByRole("button", { name: /Approve & Execute/ }));
    await waitFor(() => expect(state.clearExitPlan).toHaveBeenCalledTimes(1));
    expect(resolvePlan).toHaveBeenCalledWith("ch-1", "approve");
    expect(state.setMode).toHaveBeenCalledWith("agent");
  });
});

describe("ChatMessages scrolling", () => {
  it("pins to the bottom on mount and on growth, and scrollToBottom smooth-scrolls there", () => {
    const env = installBrowser();
    fakeLayout();
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const state = chatState({ items: [item(msg(1, "u1")), item(msg(2, "u2"))] });
    const { scroller, ref, rerender } = renderMessages(state);
    expect(scroller.scrollTop).toBe(200);

    rerender(<ChatMessages ref={ref} channelId="ch-1" chatState={{ ...state, items: [...state.items, item(msg(3, "u3"))] }} />);
    expect(scroller.scrollTop).toBe(300);

    act(() => ref.current!.scrollToBottom());
    env.flushFrames();
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: "smooth" });
  });

  it("stops following when scrolled up, offers a jump to the latest, and loads more near the top", () => {
    installBrowser();
    fakeLayout();
    const state = chatState({ items: [item(msg(1, "u1")), item(msg(2, "u2")), item(msg(3, "u3"))], hasMore: true });
    const { scroller, rerender } = renderMessages(state);

    scroller.scrollTop = 150;
    fireEvent.scroll(scroller);
    expect(screen.getByTestId("chat-jump-latest")).toBeTruthy();
    expect(state.loadMore).not.toHaveBeenCalled();

    // Not following: a new item leaves the view where it is.
    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, streamingContent: "more" }} />);
    expect(scroller.scrollTop).toBe(150);

    scroller.scrollTop = 50;
    fireEvent.scroll(scroller);
    expect(state.loadMore).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByTestId("chat-jump-latest"));
    expect(scroller.scrollTop).toBe(300);
    fireEvent.scroll(scroller);
    expect(screen.queryByTestId("chat-jump-latest")).toBeNull();
  });

  it("doesn't load more near the top while a page is loading", () => {
    installBrowser();
    fakeLayout();
    const state = chatState({ items: [item(msg(1, "u1")), item(msg(2, "u2"))], hasMore: true, loading: true });
    const { scroller } = renderMessages(state);
    scroller.scrollTop = 0;
    fireEvent.scroll(scroller);
    expect(state.loadMore).not.toHaveBeenCalled();
  });

  it("keeps the view on what's being read when an older page goes in above", () => {
    installBrowser();
    fakeLayout();
    const m2 = item(msg(2, "u2"));
    const m3 = item(msg(3, "u3"));
    const state = chatState({ items: [m2, m3] });
    const { scroller, rerender } = renderMessages(state);
    scroller.scrollTop = 50;
    fireEvent.scroll(scroller);

    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, items: [item(msg(1, "u1")), m2, m3] }} />);
    // 150 from the bottom before, 150 from the bottom after.
    expect(scroller.scrollTop).toBe(150);

    // A different channel's items aren't a page: the view stays put.
    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, items: [item(msg(7, "x7")), item(msg(8, "x8"))] }} />);
    expect(scroller.scrollTop).toBe(150);
  });
});

describe("ChatMessages scroll to message", () => {
  it("reveals a loaded message, outlines it and reports it found", () => {
    installBrowser();
    fakeLayout();
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const onScrollComplete = vi.fn();
    const { container } = renderMessages(chatState({ items: [item(msg(1, "u1")), item(msg(2, "u2"))] }), { scrollToMessageId: 1, onScrollComplete });
    expect(onScrollComplete).toHaveBeenCalledWith(true);
    expect(scrollSpy).toHaveBeenCalledWith({ block: "center" });
    expect(scrollSpy.mock.contexts[0]).toBe(container.querySelector('[data-msg-id="1"]'));
    expect(container.querySelector('[data-msg-id="1"]')?.getAttribute("data-highlighted")).toBe("true");
  });

  it("shows a message taller than the view from its top", () => {
    installBrowser();
    fakeLayout({ heights: { u1: 500 } });
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    renderMessages(chatState({ items: [item(msg(1, "u1"))] }), { scrollToMessageId: 1 });
    expect(scrollSpy).toHaveBeenCalledWith({ block: "start" });
  });

  it("centres the find term's occurrence, scaled by the zoom, and marks it", () => {
    const env = installBrowser();
    fakeLayout({ clientHeight: 50 });
    const makeRange = document.createRange.bind(document);
    vi.spyOn(document, "createRange").mockImplementation(() => Object.assign(makeRange(), { getBoundingClientRect: () => rect(300, 20) }));
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const state = chatState({ items: [item(msg(1, "u1", { content: "find the needle here" }))] });
    const { scroller, rerender } = renderMessages(state, { scrollToMessageId: 1, findTerm: "needle" });
    // Pinned to 100 on mount, then (300 - 0 - (100 - 20) / 2) / 2 further.
    expect(scroller.scrollTop).toBe(100 + 130);
    expect(scrollSpy).not.toHaveBeenCalled();
    const marked = env.registry.get("loop-find")!.ranges[0]!;
    expect(marked.toString()).toBe("needle");

    // A new term drops the old mark.
    rerender(<ChatMessages channelId="ch-1" chatState={state} scrollToMessageId={1} findTerm="other" />);
    expect(env.registry.has("loop-find")).toBe(false);
  });

  it("takes the zoom as 1 when the view has no size", () => {
    installBrowser({ highlights: false });
    const makeRange = document.createRange.bind(document);
    vi.spyOn(document, "createRange").mockImplementation(() => Object.assign(makeRange(), { getBoundingClientRect: () => rect(30, 10) }));
    const setTop = vi.spyOn(Element.prototype, "scrollTop", "set");
    const onScrollComplete = vi.fn();
    renderMessages(chatState({ items: [item(msg(1, "u1", { content: "a needle" }))] }), { scrollToMessageId: 1, findTerm: "needle", onScrollComplete });
    // jsdom's rects are all zero: 30 - 0 - (0 - 10) / 2.
    expect(setTop).toHaveBeenLastCalledWith(35);
    expect(onScrollComplete).toHaveBeenCalledWith(true);
  });

  it("scrolls to the message itself when the term isn't in its text", () => {
    const env = installBrowser();
    fakeLayout();
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    renderMessages(chatState({ items: [item(msg(1, "u1", { content: "plain" }))] }), { scrollToMessageId: 1, findTerm: "absent" });
    expect(scrollSpy).toHaveBeenCalledWith({ block: "center" });
    expect(env.registry.size).toBe(0);
  });

  it("pages older messages in until the message is loaded", () => {
    installBrowser();
    const state = chatState({ items: [item(msg(5, "u5"))], hasMore: true });
    const onScrollComplete = vi.fn();
    renderMessages(state, { scrollToMessageId: 2, onScrollComplete });
    expect(state.loadMore).toHaveBeenCalledTimes(1);
    expect(onScrollComplete).not.toHaveBeenCalled();
  });

  it.each([
    ["older than everything with nothing more to load", { items: [item(msg(5, "u5"))], hasMore: false }, 2],
    ["newer than the oldest loaded item", { items: [item(msg(5, "u5"))], hasMore: true }, 9],
  ])("reports a message %s as not found", (_name, overrides, target) => {
    installBrowser();
    const state = chatState(overrides);
    const onScrollComplete = vi.fn();
    renderMessages(state, { scrollToMessageId: target, onScrollComplete });
    expect(onScrollComplete).toHaveBeenCalledWith(false);
    expect(state.loadMore).not.toHaveBeenCalled();
  });

  it.each([
    ["before the first page", { items: [] }],
    ["while a page loads", { items: [item(msg(5, "u5"))], loading: true, hasMore: true }],
  ])("waits %s", (_name, overrides) => {
    installBrowser();
    const state = chatState(overrides);
    const onScrollComplete = vi.fn();
    renderMessages(state, { scrollToMessageId: 2, onScrollComplete });
    expect(onScrollComplete).not.toHaveBeenCalled();
    expect(state.loadMore).not.toHaveBeenCalled();
  });

  it("works without a completion callback", () => {
    installBrowser();
    const state = chatState({ items: [item(msg(5, "u5"))] });
    const { rerender, container } = renderMessages(state, { scrollToMessageId: 5 });
    expect(container.querySelector('[data-msg-id="5"]')?.getAttribute("data-highlighted")).toBe("true");
    rerender(<ChatMessages channelId="ch-1" chatState={state} scrollToMessageId={2} />);
    expect(state.loadMore).not.toHaveBeenCalled();
  });

  it("drops the outline once the message has been in view for a while", () => {
    const env = installBrowser();
    const { container, unmount } = renderMessages(chatState({ items: [item(msg(1, "u1"))] }), { scrollToMessageId: 1 });
    const bubble = container.querySelector('[data-msg-id="1"]')!;
    const observer = env.intersections.at(-1)!;
    expect(observer.observed).toEqual([bubble]);
    expect(observer.options?.root).toBe(container.firstElementChild);

    vi.useFakeTimers();
    const fire = (entries: Partial<IntersectionObserverEntry>[]) => act(() => observer.callback(entries as IntersectionObserverEntry[], {} as IntersectionObserver));
    // Only the newest change counts: here the message left the view again.
    fire([{ isIntersecting: true }, { isIntersecting: false }]);
    fire([]);
    act(() => vi.advanceTimersByTime(10_000));
    expect(bubble.getAttribute("data-highlighted")).toBe("true");

    fire([{ isIntersecting: true }]);
    act(() => vi.advanceTimersByTime(4_999));
    expect(bubble.getAttribute("data-highlighted")).toBe("true");
    act(() => vi.advanceTimersByTime(1));
    expect(bubble.getAttribute("data-highlighted")).toBeNull();
    expect(observer.disconnect).toHaveBeenCalled();

    unmount();
  });
});

describe("ChatMessages quote banner", () => {
  it("quotes the latest user message above the view when none is in view", async () => {
    installBrowser();
    fakeLayout({ tops: { u1: -300, u2: -200, u3: -100, b1: 10, u4: 400 } });
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const items = [
      item(msg(1, "u1", { content: "older prompt" })),
      item(msg(2, "u2", { content: "latest prompt" })),
      item(msg(3, "u3", { content: "" })),
      item(msg(4, "b1", { is_bot: true, content: "reply" })),
      item(msg(5, "u4", { content: "below prompt" })),
    ];
    const { container } = renderMessages(chatState({ items }));
    const quote = screen.getByTestId("trigger-quote");
    expect(quote.textContent).toContain("latest prompt");
    // Pinned to the top: it's before the message column.
    expect(container.firstElementChild!.children[1]!.contains(quote)).toBe(true);

    await userEvent.click(quote);
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: "smooth", block: "center" });
    expect(scrollSpy.mock.contexts.at(-1)).toBe(container.querySelector('[data-msg-uuid="u2"]'));
  });

  it("shows no banner while a user message is in view", () => {
    installBrowser();
    fakeLayout({ tops: { u1: -100, u2: 50 } });
    renderMessages(chatState({ items: [item(msg(1, "u1")), item(msg(2, "u2"))] }));
    expect(screen.queryByTestId("trigger-quote")).toBeNull();
  });

  it("quotes the running message at the bottom once it's out of view", async () => {
    installBrowser();
    fakeLayout({ tops: { u1: -100 } });
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const state = chatState({ items: [item(msg(1, "u1", { content: "the run's prompt" })), item(msg(2, "b1", { is_bot: true }))], isRunning: true, processingMsgId: "u1" });
    const { container } = renderMessages(state);
    const quote = screen.getByTestId("trigger-quote");
    expect(quote.textContent).toContain("the run's prompt");
    expect(quote.closest("div[style*='bottom: 0']")).toBeTruthy();
    await userEvent.click(quote);
    expect(scrollSpy.mock.contexts.at(-1)).toBe(container.querySelector('[data-msg-uuid="u1"]'));
  });

  it("prefers the run's trigger content, and falls back to the queued row for an unloaded message", async () => {
    installBrowser();
    fakeLayout();
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const queued = msg(40, "q40", { content: "queued trigger", is_running: true, is_processed: false });
    const state = chatState({ items: [item(msg(1, "b1", { is_bot: true }))], isRunning: true, queuedMessages: [queued] });
    const { rerender } = renderMessages(state);
    expect(screen.getByTestId("trigger-quote").textContent).toContain("queued trigger");
    // The message isn't loaded: a click finds nothing to scroll to.
    await userEvent.click(screen.getByTestId("trigger-quote"));
    expect(scrollSpy).not.toHaveBeenCalled();

    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, triggerContent: "from the event" }} />);
    expect(screen.getByTestId("trigger-quote").textContent).toContain("from the event");
  });

  it("shows no banner for a run whose trigger has no text", () => {
    installBrowser();
    renderMessages(chatState({ items: [item(msg(1, "b1", { is_bot: true }))], isRunning: true, processingMsgId: "unknown" }));
    expect(screen.queryByTestId("trigger-quote")).toBeNull();
  });

  it("recomputes what's in view once per frame on scroll and resize, and stops on unmount", () => {
    const env = installBrowser();
    const layout = fakeLayout({ tops: { u1: 10 } });
    const items = [item(msg(1, "u1", { content: "the prompt" })), item(msg(2, "", { content: "no id" })), item(msg(3, "b1", { is_bot: true }))];
    const { scroller, unmount } = renderMessages(chatState({ items }));
    expect(screen.queryByTestId("trigger-quote")).toBeNull();

    // Unchanged: nothing re-renders.
    fireEvent.scroll(scroller);
    fireEvent.scroll(scroller);
    expect(env.frames).toHaveLength(1);
    env.flushFrames();
    expect(screen.queryByTestId("trigger-quote")).toBeNull();

    layout.tops.u1 = -50;
    act(() => env.resizes[0]!.callback([], {} as ResizeObserver));
    env.flushFrames();
    expect(screen.getByTestId("trigger-quote").textContent).toContain("the prompt");

    fireEvent.scroll(scroller);
    unmount();
    expect(env.cancelFrame).toHaveBeenCalledWith(3);
    expect(env.resizes[0]!.disconnect).toHaveBeenCalled();
  });

  it("doesn't cancel a frame on unmount when none is pending", () => {
    const env = installBrowser();
    const { unmount } = renderMessages(chatState({ items: [item(msg(1, "u1"))] }));
    unmount();
    expect(env.cancelFrame).not.toHaveBeenCalled();
  });
});

describe("ChatMessages re-rendering", () => {
  const bubbleRenders = (createdAt: string) => vi.mocked(formatMessageTimestamp).mock.calls.filter(([v]) => v === createdAt).length;
  const toolRenders = (input: string) => vi.mocked(renderInputWithLinks).mock.calls.filter(([v]) => v === input).length;

  it("leaves past bubbles and tool runs alone on streaming tokens, activity and later tool events", async () => {
    installBrowser();
    const user = userEvent.setup();
    const u1 = msg(1, "u1", { created_at: "2026-01-01T09:00:00Z" });
    const items = [item(u1), toolUse(2, "Grep", "g1", "u1"), thinking(3, "ponder", "u1"), item(msg(4, "b4", { is_bot: true, trigger_msg_id: "u1" }))];
    const state = chatState({ items, isRunning: true });
    const { rerender } = renderMessages(state);
    // The run is no longer the trailing group, so it starts collapsed.
    await user.click(screen.getByText("2 steps"));
    const bubbles = bubbleRenders(u1.created_at);
    const tools = toolRenders("input of Grep");
    expect(bubbles).toBeGreaterThan(0);
    expect(tools).toBeGreaterThan(0);

    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, streamingContent: "tok" }} />);
    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, streamingContent: "tok tok", agentActivity: { activity: "model" } }} />);
    const liveTail = [toolUse(5, "Glob", "g2")];
    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, liveTail }} />);
    rerender(<ChatMessages channelId="ch-1" chatState={{ ...state, liveTail: [...liveTail, toolResult(6, "found", "g2")] }} />);

    expect(screen.getByText("Glob")).toBeTruthy();
    expect(bubbleRenders(u1.created_at)).toBe(bubbles);
    expect(toolRenders("input of Grep")).toBe(tools);
  });
});
