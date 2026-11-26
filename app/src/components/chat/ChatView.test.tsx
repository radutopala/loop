import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPromptSuggestion, forkAtMessage } from "../../api/channels";
import { fetchLive, type LiveStatus, warmLive } from "../../api/live";
import { holdQueuedMessage, releaseQueuedHold } from "../../api/loopApi";
import { fetchMonitors } from "../../api/monitors";
import { searchChannelMessages } from "../../api/search";
import type { ChatState } from "../../hooks/useChatState";
import type { ChatEventListener } from "../../hooks/useChatStateStore";
import { darkColors } from "../../theme";
import type { Message, TimelineItem } from "../../types";
import { ChatInput, type ChatInputProps } from "./ChatInput";
import { ChatView } from "./ChatView";
import { draftText } from "./chatDrafts";

vi.mock("../../api/channels", () => ({
  fetchPromptSuggestion: vi.fn(),
  forkAtMessage: vi.fn(),
  formatAskAnswers: vi.fn(() => ""),
  resolveAsk: vi.fn(),
  resolvePlan: vi.fn(),
}));

vi.mock("../../api/live", () => ({
  fetchLive: vi.fn(),
  stopLiveTask: vi.fn(),
  warmLive: vi.fn(),
}));

vi.mock("../../api/loopApi", () => ({
  deleteQueuedMessage: vi.fn(),
  holdQueuedMessage: vi.fn(),
  reclaimDockerSpace: vi.fn(),
  releaseQueuedHold: vi.fn(),
  reorderQueuedMessages: vi.fn(),
  resolveGateApproval: vi.fn(),
  sendCommand: vi.fn(),
  sendMessage: vi.fn(),
  steerQueuedMessage: vi.fn(),
  updateQueuedMessage: vi.fn(),
}));

vi.mock("../../api/monitors", () => ({
  cancelMonitor: vi.fn(),
  fetchMonitors: vi.fn(),
}));

vi.mock("../../api/search", () => ({
  searchChannelMessages: vi.fn(),
}));

// The composer is its own heavily tested unit with many API calls of its
// own; a stub records the props ChatView wires into it.
vi.mock("./ChatInput", () => ({
  ChatInput: vi.fn(() => <div data-testid="composer" />),
}));

// The icon animates along its SVG path's length, which jsdom can't measure;
// a stub shows the color and animation it was asked for.
vi.mock("../LoopInfinityIcon", () => ({
  LoopInfinityIcon: ({ color, animated }: { color?: string; animated?: boolean }) => <span data-testid="infinity" data-color={color} data-animated={animated ? "true" : "false"} />,
}));

const inputMock = vi.mocked(ChatInput);
const lastInput = (): ChatInputProps => inputMock.mock.lastCall![0];

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

const item = (m: Message): TimelineItem => ({ kind: "message", position: m.id, id: m.id, data: m });

function chatState(overrides: Partial<ChatState> = {}): ChatState {
  const items = overrides.items ?? [];
  return {
    items,
    liveTail: [],
    messages: items.flatMap((it) => (it.kind === "message" ? [it.data] : [])),
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

const withMessages = (overrides: Partial<ChatState> = {}) => chatState({ items: [item(msg(1, "u1", { content: "hello there" }))], ...overrides });

// A subscription that never delivers events: enough for the live status,
// monitors and suggestion to be followed.
const subscribe = vi.fn((_listener: ChatEventListener) => () => {});

// Lets the frames ChatMessages asks for run on demand, and stands in for
// the observers jsdom leaves out.
function installBrowser() {
  const frames: FrameRequestCallback[] = [];
  vi.stubGlobal(
    "requestAnimationFrame",
    vi.fn((cb: FrameRequestCallback) => frames.push(cb)),
  );
  vi.stubGlobal("cancelAnimationFrame", vi.fn());
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal("CSS", { highlights: new Map() });
  return {
    flushFrames: () =>
      act(() => {
        for (const cb of frames.splice(0)) cb(0);
      }),
  };
}

function live(status: Partial<LiveStatus>): LiveStatus {
  return { running: false, busy: false, ...status };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchLive).mockResolvedValue(live({ running: false, enabled: true }));
  vi.mocked(fetchMonitors).mockResolvedValue([]);
  vi.mocked(fetchPromptSuggestion).mockResolvedValue("");
  vi.mocked(warmLive).mockResolvedValue(true);
  vi.mocked(releaseQueuedHold).mockResolvedValue(undefined as never);
  vi.mocked(searchChannelMessages).mockResolvedValue([]);
});

afterEach(() => {
  // Unmount before the stubbed observers go: a render still on its way
  // would otherwise build one that no longer exists.
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("ChatView states", () => {
  it("shows the welcome screen without a channel", () => {
    installBrowser();
    render(<ChatView channelId={null} chatState={chatState()} />);
    expect(screen.getByAltText("Loop")).toBeTruthy();
    expect(screen.queryByTestId("composer")).toBeNull();
    expect(screen.queryByTestId("live-icon")).toBeNull();
  });

  it("shows the welcome screen and a bare composer in an empty channel", () => {
    installBrowser();
    render(<ChatView channelId="ch-empty" chatState={chatState({ isRunning: true })} />);
    expect(screen.getByAltText("Loop")).toBeTruthy();
    expect(screen.queryByTestId("chat-find-toggle")).toBeNull();
    const props = lastInput();
    expect(props.channelId).toBe("ch-empty");
    // Nothing to be running or editing with no messages.
    expect(props.isRunning).toBeUndefined();
    expect(props.editingQueued).toBeUndefined();
    expect(props.onSaveEdit).toBeUndefined();
    expect(props.onCancelEdit).toBeUndefined();
    // An empty chat isn't busy.
    expect(screen.getByTestId("live-icon").getAttribute("data-state")).toBe("off");

    // Find isn't offered with nothing to find.
    fireEvent.keyDown(screen.getByTestId("composer"), { key: "f", metaKey: true });
    expect(screen.queryByTestId("chat-find-bar")).toBeNull();
  });

  it("shows the status bars under the welcome screen in an empty channel", async () => {
    installBrowser();
    vi.mocked(fetchLive).mockResolvedValue(live({ running: false, reason: "crashed hard" }));
    render(<ChatView channelId="ch-empty-bar" chatState={chatState()} subscribeChatEvents={subscribe} />);
    expect(await screen.findByTestId("live-bar")).toBeTruthy();
    expect(fetchMonitors).toHaveBeenCalledWith("ch-empty-bar");
  });

  it("shows a loading layer over an empty list while the first page loads", () => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={chatState({ loading: true })} />);
    expect(screen.getByTestId("chat-loading").textContent).toContain("Loading...");
    expect(screen.queryByAltText("Loop")).toBeNull();
  });

  it("renders the messages, the composer and the footer", () => {
    installBrowser();
    const state = withMessages({
      isRunning: true,
      gateApprovals: { chat: { req_id: "req-9", kind: "network", target: "example.com:443" } },
      exitPlanRequest: { plan: "plan" },
      askUserQuestions: { questions: [] },
    });
    render(<ChatView channelId="ch-1" chatState={state} roots={[]} noAutoFocus trustPending />);
    expect(screen.getByText("hello there")).toBeTruthy();
    expect(screen.queryByTestId("chat-loading")).toBeNull();
    const props = lastInput();
    expect(props).toMatchObject({
      channelId: "ch-1",
      noAutoFocus: true,
      roots: [],
      isRunning: true,
      messages: state.messages,
      mode: "agent",
      setMode: state.setMode,
      pendingGateReqId: "req-9",
      hasPendingExitPlan: true,
      hasPendingAskUser: true,
      trustPending: true,
      suggestion: null,
      quotedMessage: null,
      editingQueued: null,
    });
    expect(screen.getByTestId("live-icon").getAttribute("data-state")).toBe("working");
    expect(screen.getByTestId("live-icon").getAttribute("title")).toBe("Working");
    expect(screen.getByTestId("infinity").getAttribute("data-animated")).toBe("true");
    expect(screen.getByTestId("infinity").getAttribute("data-color")).toBe(darkColors.textDim);
    // Without the channel's events its turns aren't known to run live.
    expect(screen.getByText("Running non-interactively in an isolated Docker container")).toBeTruthy();
  });

  it("passes no pending gate and no plan or question when there are none", () => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    expect(lastInput()).toMatchObject({ pendingGateReqId: null, hasPendingExitPlan: false, hasPendingAskUser: false });
  });

  it("shows the read-only note in place of the composer and the live bar", () => {
    installBrowser();
    render(
      <ChatView
        channelId="ch-1"
        chatState={withMessages({ isRunning: true, backgroundTasks: [{ id: "t1", started_at: "2026-01-01T00:00:00Z", description: "bg job" }] })}
        readOnlyNote="Nobody writes here"
      />,
    );
    expect(screen.getByTestId("chat-read-only").textContent).toBe("Nobody writes here");
    expect(screen.queryByTestId("composer")).toBeNull();
    // The background tasks aren't shown in a read-only chat.
    expect(screen.queryByText("bg job")).toBeNull();
  });

  it("shows a running one-shot run's background tasks", () => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={withMessages({ isRunning: true, backgroundTasks: [{ id: "t1", started_at: "2026-01-01T00:00:00Z", description: "bg job" }] })} />);
    expect(screen.getByText(/bg job/)).toBeTruthy();
  });
});

describe("ChatView live session", () => {
  it("follows the live session: idle icon, live footer and warming on typing", async () => {
    installBrowser();
    vi.mocked(fetchLive).mockResolvedValue(live({ running: true, enabled: true }));
    render(<ChatView channelId="ch-live-idle" chatState={withMessages()} subscribeChatEvents={subscribe} wsOpens={1} />);
    await waitFor(() => expect(screen.getByTestId("live-icon").getAttribute("data-state")).toBe("idle"));
    expect(screen.getByTestId("infinity").getAttribute("data-color")).toBe(darkColors.active);
    expect(screen.getByTestId("infinity").getAttribute("data-animated")).toBe("false");
    expect(screen.getByText("Running interactively in an isolated Docker container")).toBeTruthy();
    expect(fetchMonitors).toHaveBeenCalledWith("ch-live-idle");
  });

  it("warms the live session when typing starts in a channel that follows it", async () => {
    installBrowser();
    vi.mocked(fetchLive).mockResolvedValue(live({ running: false, enabled: true }));
    render(<ChatView channelId="ch-live-warm" chatState={withMessages()} subscribeChatEvents={subscribe} />);
    await waitFor(() => expect(fetchLive).toHaveBeenCalledWith("ch-live-warm"));
    await act(async () => {});
    act(() => lastInput().onTyping!());
    expect(warmLive).toHaveBeenCalledWith("ch-live-warm");
  });

  it("says the turns run one-shot when the channel's live mode is off", async () => {
    installBrowser();
    vi.mocked(fetchLive).mockResolvedValue(live({ running: false, enabled: false }));
    render(<ChatView channelId="ch-live-off" chatState={withMessages()} subscribeChatEvents={subscribe} />);
    expect(await screen.findByText("Running non-interactively in an isolated Docker container")).toBeTruthy();
  });

  it("shows a running live session's background tasks and opens Settings from a full-disk ending", async () => {
    installBrowser();
    vi.mocked(fetchLive).mockResolvedValue(live({ running: true, background_tasks: [{ id: "t1", started_at: "2026-01-01T00:00:00Z", description: "live job" }] }));
    const { rerender } = render(<ChatView channelId="ch-live-bg" chatState={withMessages()} subscribeChatEvents={subscribe} />);
    expect(await screen.findByText(/live job/)).toBeTruthy();

    vi.mocked(fetchLive).mockResolvedValue(live({ running: false, reason: "write /x: no space left on device" }));
    const onFreeDockerSpace = vi.fn();
    rerender(<ChatView channelId="ch-live-disk" chatState={withMessages()} subscribeChatEvents={subscribe} onFreeDockerSpace={onFreeDockerSpace} />);
    await userEvent.click(await screen.findByTestId("live-bar-reclaim-settings"));
    expect(onFreeDockerSpace).toHaveBeenCalledTimes(1);
  });
});

describe("ChatView composer wiring", () => {
  it("scrolls to the latest message once a message is sent", () => {
    const env = installBrowser();
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    act(() => lastInput().onSent!());
    env.flushFrames();
    expect(scrollSpy).toHaveBeenCalledWith({ behavior: "smooth" });
  });

  it("dismisses only the chat's gate card", () => {
    installBrowser();
    const state = withMessages();
    render(<ChatView channelId="ch-1" chatState={state} />);
    act(() => lastInput().onDismissGate!());
    expect(state.clearGateApproval).toHaveBeenCalledWith("chat");
  });

  it("quotes a message into the composer and clears it again", async () => {
    installBrowser();
    const state = withMessages();
    render(<ChatView channelId="ch-1" chatState={state} />);
    fireEvent.contextMenu(screen.getByText("hello there"));
    await userEvent.click(await screen.findByText("Quote reply"));
    expect(lastInput().quotedMessage).toEqual(state.messages[0]);
    act(() => lastInput().onClearQuote!());
    expect(lastInput().quotedMessage).toBeNull();
  });

  it("opens a queued message in the composer for editing", async () => {
    installBrowser();
    vi.mocked(holdQueuedMessage).mockResolvedValue(true);
    const queued = msg(2, "q2", { content: "queued text", is_processed: false });
    render(<ChatView channelId="ch-1" chatState={withMessages({ queuedMessages: [queued], items: [item(msg(1, "u1")), item(queued)] })} />);
    await userEvent.click(screen.getByTestId("queued-toggle"));
    await userEvent.click(screen.getByTitle("Edit — it won't start while you edit"));
    await waitFor(() => expect(lastInput().editingQueued).toEqual(queued));
    expect(holdQueuedMessage).toHaveBeenCalledWith("ch-1", "q2");
    expect(lastInput().onSaveEdit).toBeTypeOf("function");
    expect(lastInput().onCancelEdit).toBeTypeOf("function");
  });
});

describe("ChatView fork", () => {
  const forkable = () => withMessages({ items: [item(msg(1, "u1", { content: "hello there" })), item(msg(2, "b1", { is_bot: true, content: "a reply", forkable: true }))] });

  it("forks at a message, drafts its prompt into the new thread and opens it", async () => {
    installBrowser();
    vi.mocked(forkAtMessage).mockResolvedValue({ thread_id: "th-new", prompt: "hello there" });
    const onSelectThread = vi.fn();
    render(<ChatView channelId="ch-1" chatState={forkable()} onSelectThread={onSelectThread} />);
    fireEvent.mouseEnter(screen.getByText("hello there"));
    const [userFork] = screen.getAllByTitle("Fork the conversation at this message into a new thread");
    await userEvent.click(userFork!);
    await waitFor(() => expect(onSelectThread).toHaveBeenCalledWith("th-new"));
    expect(forkAtMessage).toHaveBeenCalledWith("ch-1", "u1");
    expect(draftText.get("th-new")).toBe("hello there");
  });

  it("opens the new thread without a draft when the fork has no prompt", async () => {
    installBrowser();
    vi.mocked(forkAtMessage).mockResolvedValue({ thread_id: "th-bot" });
    const onSelectThread = vi.fn();
    render(<ChatView channelId="ch-1" chatState={forkable()} onSelectThread={onSelectThread} />);
    const forks = screen.getAllByTitle("Fork the conversation at this message into a new thread");
    await userEvent.click(forks.at(-1)!);
    await waitFor(() => expect(onSelectThread).toHaveBeenCalledWith("th-bot"));
    expect(forkAtMessage).toHaveBeenCalledWith("ch-1", "b1");
    expect(draftText.get("th-bot")).toBeUndefined();
  });

  it("offers no fork without a way to open the thread", () => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={forkable()} />);
    expect(screen.queryByTitle("Fork the conversation at this message into a new thread")).toBeNull();
  });
});

describe("ChatView find", () => {
  it.each([
    ["MacIntel", "Find in chat (⌘F)"],
    ["Win32", "Find in chat (Ctrl+F)"],
  ])("titles the find toggle for %s", (platform, title) => {
    installBrowser();
    vi.spyOn(navigator, "platform", "get").mockReturnValue(platform);
    render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    expect(screen.getByTestId("chat-find-toggle").getAttribute("title")).toBe(title);
  });

  it("highlights the find toggle on hover", () => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    const toggle = screen.getByTestId("chat-find-toggle");
    const before = toggle.style.color;
    fireEvent.mouseEnter(toggle);
    expect(toggle.style.color).not.toBe(before);
    fireEvent.mouseLeave(toggle);
    expect(toggle.style.color).toBe(before);
  });

  it("opens from the toggle and closes from the bar", async () => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    await userEvent.click(screen.getByTestId("chat-find-toggle"));
    expect(screen.getByTestId("chat-find-bar")).toBeTruthy();
    expect(screen.queryByTestId("chat-find-toggle")).toBeNull();
    await userEvent.click(screen.getByTestId("chat-find-close"));
    expect(screen.queryByTestId("chat-find-bar")).toBeNull();
    expect(screen.getByTestId("chat-find-toggle")).toBeTruthy();
  });

  it.each([
    ["Cmd+F", { key: "f", metaKey: true }],
    ["Ctrl+F", { key: "f", ctrlKey: true }],
  ])("opens with %s", (_name, keys) => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    const notCancelled = fireEvent.keyDown(screen.getByTestId("composer"), keys);
    expect(notCancelled).toBe(false);
    expect(screen.getByTestId("chat-find-bar")).toBeTruthy();
  });

  it.each([
    ["F alone", { key: "f" }],
    ["Cmd with another key", { key: "g", metaKey: true }],
  ])("ignores %s", (_name, keys) => {
    installBrowser();
    render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    fireEvent.keyDown(screen.getByTestId("composer"), keys);
    expect(screen.queryByTestId("chat-find-bar")).toBeNull();
  });

  it("jumps to a match, marking the term, without telling the caller", async () => {
    installBrowser();
    vi.mocked(searchChannelMessages).mockResolvedValue([1]);
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const onScrollComplete = vi.fn();
    const { container } = render(<ChatView channelId="ch-1" chatState={withMessages()} onScrollComplete={onScrollComplete} />);
    await userEvent.click(screen.getByTestId("chat-find-toggle"));
    await userEvent.type(screen.getByTestId("chat-find-input"), "zzz");
    await waitFor(() => expect(searchChannelMessages).toHaveBeenCalledWith("ch-1", "zzz", expect.any(AbortSignal)));
    await waitFor(() => expect(container.querySelector('[data-msg-id="1"]')?.getAttribute("data-highlighted")).toBe("true"));
    expect(scrollSpy).toHaveBeenCalled();
    // The find target is cleared once shown; the caller's link scroll isn't involved.
    expect(onScrollComplete).not.toHaveBeenCalled();
  });

  it("reports a message link's scroll to the caller", () => {
    installBrowser();
    const onScrollComplete = vi.fn();
    render(<ChatView channelId="ch-1" chatState={withMessages()} scrollToMessageId={1} onScrollComplete={onScrollComplete} />);
    expect(onScrollComplete).toHaveBeenCalledWith(true);
  });

  it("handles a message link without a completion callback", () => {
    installBrowser();
    const { container } = render(<ChatView channelId="ch-1" chatState={withMessages()} scrollToMessageId={1} />);
    expect(container.querySelector('[data-msg-id="1"]')?.getAttribute("data-highlighted")).toBe("true");
  });

  it("closes when the channel changes", async () => {
    installBrowser();
    const { rerender } = render(<ChatView channelId="ch-1" chatState={withMessages()} />);
    await userEvent.click(screen.getByTestId("chat-find-toggle"));
    expect(screen.getByTestId("chat-find-bar")).toBeTruthy();
    rerender(<ChatView channelId="ch-2" chatState={withMessages()} />);
    expect(screen.queryByTestId("chat-find-bar")).toBeNull();
  });
});
