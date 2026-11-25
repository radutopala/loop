import { act, fireEvent, getDefaultNormalizer, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resolveAsk, resolvePlan } from "../../api/channels";
import type { AskUserQuestion, Message, TimelineItem } from "../../types";
import { browserOpenQueue } from "../../utils/browserOpen";
import { writeClipboard } from "../../utils/copyToClipboard";
import { PR_AUTHOR_ID } from "../../utils/prStatus";
import {
  AgentActivityIndicator,
  AskUserQuestionCard,
  CompactingMarker,
  CompletionSummary,
  ExitPlanCard,
  MessageBubble,
  renderTimelineItem,
  StreamingBubble,
  TaskChecklist,
  ThinkingBubble,
  ToolActivityIndicator,
  ToolRunBlock,
  TriggerQuote,
} from "./bubbles";
import { ChannelContext } from "./chatShared";

vi.mock("../../api/channels", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/channels")>()),
  resolveAsk: vi.fn(),
  resolvePlan: vi.fn(),
}));

vi.mock("../../utils/copyToClipboard", () => ({
  writeClipboard: vi.fn(),
}));

const resolveAskMock = vi.mocked(resolveAsk);
const resolvePlanMock = vi.mocked(resolvePlan);
const clipboardMock = vi.mocked(writeClipboard);

type Result = { text: string; is_error: boolean; truncated: boolean };

function msg(over: Partial<Message> = {}): Message {
  return {
    id: 7,
    channel_id: "ch-1",
    msg_id: "m-7",
    author_id: "u-1",
    author_name: "user",
    content: "hello there",
    is_bot: false,
    is_processed: true,
    created_at: new Date().toISOString(),
    ...over,
  };
}

function pending<T = void>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.clearAllMocks();
  clipboardMock.mockResolvedValue(undefined);
  resolveAskMock.mockResolvedValue(undefined);
  resolvePlanMock.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  window.getSelection()?.removeAllRanges();
});

describe("CompactingMarker", () => {
  it("shows the compacted-context marker", () => {
    render(<CompactingMarker />);
    expect(screen.getByText("Compacted context")).toBeTruthy();
  });
});

describe("ToolRunBlock", () => {
  const items: TimelineItem[] = [
    { kind: "thinking", position: 1, id: 1, text: "pondering" },
    { kind: "tool_use", position: 2, id: 2, tool_use_id: "t1", tool_name: "Bash", tool_input: "ls" },
    { kind: "tool_use", position: 3, id: 3, tool_use_id: "t2", tool_name: "Bash", tool_input: "pwd" },
    { kind: "tool_result", position: 4, id: 4, tool_use_id: "t2", text: "boom", is_error: true },
    { kind: "compacting", position: 5, id: 5 },
  ];

  function renderBlock(over: Partial<Parameters<typeof ToolRunBlock>[0]> = {}) {
    const props = { items, resultsByToolUseID: new Map<string, Result>(), skippedToolResultIDs: new Set<string>(), isActive: false, ...over };
    return render(<ToolRunBlock {...props} />);
  }

  it("summarizes unique tools, thoughts, compactions and errors", () => {
    renderBlock();
    expect(screen.getByText("5 steps")).toBeTruthy();
    expect(screen.getByText("· Bash · 1 thought · compacted")).toBeTruthy();
    expect(screen.getByText("1 error")).toBeTruthy();
    expect(screen.queryByText("pondering")).toBeNull();
  });

  it("pluralizes counts and folds tools past the fourth", () => {
    const many: TimelineItem[] = [
      ...["A", "B", "C", "D", "E", "F"].map((n, i): TimelineItem => ({ kind: "tool_use", position: i, id: i, tool_use_id: `u${i}`, tool_name: n, tool_input: "" })),
      { kind: "thinking", position: 10, id: 10, text: "a" },
      { kind: "thinking", position: 11, id: 11, text: "b" },
      { kind: "compacting", position: 12, id: 12 },
      { kind: "compacting", position: 13, id: 13 },
      { kind: "tool_result", position: 14, id: 14, tool_use_id: "u0", text: "x", is_error: true },
      { kind: "tool_result", position: 15, id: 15, tool_use_id: "u1", text: "y", is_error: true },
    ];
    renderBlock({ items: many });
    expect(screen.getByText("· A, B, C, D, +2 · 2 thoughts · 2 compactions")).toBeTruthy();
    expect(screen.getByText("2 errors")).toBeTruthy();
  });

  it("shows a single step without summary or errors", () => {
    renderBlock({ items: [{ kind: "tool_result", position: 1, id: 1, tool_use_id: "t", text: "ok" }] });
    expect(screen.getByText("1 step")).toBeTruthy();
    expect(screen.queryByText(/^·/)).toBeNull();
    expect(screen.queryByText(/error/)).toBeNull();
  });

  it("toggles its items on header click", async () => {
    const user = userEvent.setup();
    renderBlock();
    await user.click(screen.getByText("5 steps"));
    expect(screen.getByText("pondering")).toBeTruthy();
    await user.click(screen.getByText("5 steps"));
    expect(screen.queryByText("pondering")).toBeNull();
  });

  it("expands while active and follows the active flag across rerenders", () => {
    const props = { items, resultsByToolUseID: new Map<string, Result>(), skippedToolResultIDs: new Set<string>() };
    const { rerender } = render(<ToolRunBlock {...props} isActive />);
    expect(screen.getByText("pondering")).toBeTruthy();
    rerender(<ToolRunBlock {...props} isActive={false} />);
    expect(screen.queryByText("pondering")).toBeNull();
    rerender(<ToolRunBlock {...props} isActive />);
    expect(screen.getByText("pondering")).toBeTruthy();
  });
});

describe("renderTimelineItem", () => {
  const results = new Map<string, Result>([["skip", { text: "merged result", is_error: false, truncated: false }]]);
  const skipped = new Set(["skip"]);
  const renderItem = (it: TimelineItem) => render(<>{renderTimelineItem(it, results, skipped)}</>);

  it("renders thinking, with and without the truncated flag", () => {
    renderItem({ kind: "thinking", position: 1, id: 1, text: "deep", truncated: true });
    expect(screen.getByText("deep")).toBeTruthy();
    expect(screen.getByText("truncated")).toBeTruthy();
  });

  it("renders thinking without a truncated flag as not truncated", () => {
    renderItem({ kind: "thinking", position: 1, id: 1, text: "deep" });
    expect(screen.queryByText("truncated")).toBeNull();
  });

  it("merges a skipped tool result into its tool use", () => {
    renderItem({ kind: "tool_use", position: 1, id: 1, tool_use_id: "skip", tool_name: "Grep", tool_input: "foo" });
    expect(screen.getByText("Grep")).toBeTruthy();
    expect(screen.getByText("merged result")).toBeTruthy();
  });

  it.each(["other", ""])("renders a tool use without a merged result (id %j)", (id) => {
    renderItem({ kind: "tool_use", position: 1, id: 1, tool_use_id: id, tool_name: "Grep", tool_input: "foo" });
    expect(screen.getByText("Grep")).toBeTruthy();
    expect(screen.getByText("foo")).toBeTruthy();
    expect(screen.queryByText("merged result")).toBeNull();
  });

  it("skips a tool result already merged into its tool use", () => {
    const { container } = renderItem({ kind: "tool_result", position: 1, id: 1, tool_use_id: "skip", text: "dup" });
    expect(container.textContent).toBe("");
  });

  it.each(["other", ""])("renders a standalone tool result (id %j)", (id) => {
    renderItem({ kind: "tool_result", position: 1, id: 1, tool_use_id: id, text: "output" });
    expect(screen.getByText("result")).toBeTruthy();
    expect(screen.getByText("output")).toBeTruthy();
    expect(screen.queryByText("(truncated)")).toBeNull();
  });

  it("renders an errored, truncated tool result", () => {
    renderItem({ kind: "tool_result", position: 1, id: 1, tool_use_id: "x", text: "bad", is_error: true, truncated: true });
    expect(screen.getByText("(truncated)")).toBeTruthy();
  });

  it("renders a compacting marker", () => {
    renderItem({ kind: "compacting", position: 1, id: 1 });
    expect(screen.getByText("Compacted context")).toBeTruthy();
  });

  it("renders nothing for a message item", () => {
    const { container } = renderItem({ kind: "message", position: 1, id: 1, data: msg() });
    expect(container.textContent).toBe("");
  });
});

describe("MessageBubble", () => {
  function bubble(container: HTMLElement) {
    return container.querySelector("[data-msg-id]") as HTMLElement;
  }

  function selectText(node: Node) {
    const range = document.createRange();
    range.selectNodeContents(node);
    const sel = window.getSelection()!;
    sel.removeAllRanges();
    sel.addRange(range);
  }

  function menuItem(label: string) {
    return screen.getByRole("menuitem", { name: label });
  }

  function menuLabels() {
    return screen.getAllByRole("menuitem").map((b) => b.textContent);
  }

  function hasSeparator(label: string) {
    return menuItem(label).previousElementSibling !== null;
  }

  it("renders a user message with its time, db id and markers", () => {
    const { container } = render(<MessageBubble message={msg()} highlighted />);
    const el = bubble(container);
    expect(el.dataset.isUser).toBe("true");
    expect(el.dataset.highlighted).toBe("true");
    expect(el.dataset.msgUuid).toBe("m-7");
    expect(screen.getByText("hello there")).toBeTruthy();
    expect(screen.getByTestId("message-db-id").textContent).toBe("#7");
    expect(screen.queryByTestId("pr-message-badge")).toBeNull();
    expect(screen.queryByText("processing")).toBeNull();
  });

  it("renders a bot message with a copy button and no user markers", () => {
    const { container } = render(<MessageBubble message={msg({ is_bot: true, id: 0 })} />);
    const el = bubble(container);
    expect(el.dataset.isUser).toBeUndefined();
    expect(el.dataset.highlighted).toBeUndefined();
    expect(screen.queryByTestId("message-db-id")).toBeNull();
    expect(screen.getByText("hello there")).toBeTruthy();
  });

  it.each([
    [{ showQueued: true }, "queued"],
    [{ showQueued: true, queuePosition: "2/3" }, "queued 2/3"],
    [{ showQueued: true, adding: true }, "adding…"],
    [{ showProcessing: true }, "processing"],
  ])("shows the queue state %o", (props, text) => {
    render(<MessageBubble message={msg()} {...props} />);
    expect(screen.getByText(text)).toBeTruthy();
  });

  it("marks a message from the PR watcher", () => {
    render(<MessageBubble message={msg({ author_id: PR_AUTHOR_ID })} />);
    expect(screen.getByTestId("pr-message-badge").textContent).toBe("PR");
  });

  it("shows the countdown of a delayed message", () => {
    render(<MessageBubble message={msg({ not_before: Math.floor(Date.now() / 1000) + 120 })} />);
    expect(screen.getByTitle("Scheduled — runs when the countdown ends")).toBeTruthy();
  });

  it("shows where a message sent from another channel came from", () => {
    vi.stubGlobal(
      "ResizeObserver",
      class {
        observe() {}
        disconnect() {}
      },
    );
    render(<MessageBubble message={msg({ tags: { origin_channel: "abc", origin_channel_name: "other-thread", origin_message: "3" } })} />);
    const link = screen.getByTestId("message-origin");
    expect(link.textContent).toBe("from #other-thread");
    expect(link.getAttribute("href")).toBe("#abc/3");
  });

  it("offers +fork on a processed user message and forks it", async () => {
    const user = userEvent.setup();
    const onFork = vi.fn().mockResolvedValue(undefined);
    const message = msg();
    const { container } = render(<MessageBubble message={message} onFork={onFork} />);
    const btn = screen.getByTestId("fork-message-btn");
    expect(btn.style.opacity).toBe("0");
    expect(btn.style.marginTop).toBe("0px");
    fireEvent.mouseEnter(bubble(container));
    expect(btn.style.opacity).toBe("1");
    fireEvent.mouseLeave(bubble(container));
    expect(btn.style.opacity).toBe("0");
    await user.click(btn);
    expect(onFork).toHaveBeenCalledWith(message);
  });

  it("hides +fork on a queued user message", () => {
    render(<MessageBubble message={msg()} onFork={vi.fn()} showQueued />);
    expect(screen.queryByTestId("fork-message-btn")).toBeNull();
  });

  it("offers +fork on a forkable bot reply", () => {
    render(<MessageBubble message={msg({ is_bot: true, forkable: true })} onFork={vi.fn()} />);
    expect(screen.getByTestId("fork-message-btn").style.marginTop).toBe("6px");
  });

  it("renders a turn-ending bot reply's action row without fork", () => {
    render(<MessageBubble message={msg({ is_bot: true })} turnEnd />);
    expect(screen.queryByTestId("fork-message-btn")).toBeNull();
  });

  it("copies a link to the message from its db id, then resets the copied state", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    render(<MessageBubble message={msg()} />);
    const btn = screen.getByTestId("message-db-id");
    expect(btn.title).toMatch(/^Message id in the database/);
    await act(async () => {
      fireEvent.click(btn);
    });
    expect(clipboardMock).toHaveBeenCalledWith("loop://channel/ch-1/7");
    expect(btn.title).toBe("Link copied");
    act(() => {
      vi.advanceTimersByTime(1200);
    });
    expect(btn.title).toMatch(/^Message id in the database/);
  });

  it("ignores a blocked clipboard on the db id", async () => {
    clipboardMock.mockRejectedValue(new Error("blocked"));
    render(<MessageBubble message={msg()} />);
    const btn = screen.getByTestId("message-db-id");
    await act(async () => {
      fireEvent.click(btn);
    });
    expect(clipboardMock).toHaveBeenCalledOnce();
    expect(btn.title).toMatch(/^Message id in the database/);
  });

  it("leaves the native context menu alone without onQuote", () => {
    const { container } = render(<MessageBubble message={msg()} />);
    expect(fireEvent.contextMenu(bubble(container))).toBe(true);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("offers quote reply and copy link, and closes on Escape", async () => {
    const user = userEvent.setup();
    const onQuote = vi.fn();
    const message = msg();
    const { container } = render(<MessageBubble message={message} onQuote={onQuote} />);
    expect(fireEvent.contextMenu(bubble(container), { clientX: 10, clientY: 20 })).toBe(false);
    expect(menuLabels()).toEqual(["Quote reply", "Copy link to message"]);
    expect(hasSeparator("Quote reply")).toBe(false);
    await user.click(menuItem("Quote reply"));
    expect(onQuote).toHaveBeenCalledWith(message);

    fireEvent.contextMenu(bubble(container));
    await user.click(menuItem("Copy link to message"));
    expect(clipboardMock).toHaveBeenCalledWith("loop://channel/ch-1/7");

    fireEvent.contextMenu(bubble(container));
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("leaves out copy link to message for an unstored message", () => {
    const { container } = render(<MessageBubble message={msg({ id: 0 })} onQuote={vi.fn()} />);
    fireEvent.contextMenu(bubble(container));
    expect(menuLabels()).toEqual(["Quote reply"]);
  });

  it("works when the window has no selection", () => {
    vi.spyOn(window, "getSelection").mockReturnValue(null);
    const { container } = render(<MessageBubble message={msg()} onQuote={vi.fn()} />);
    fireEvent.contextMenu(bubble(container));
    expect(menuLabels()).toEqual(["Quote reply", "Copy link to message"]);
  });

  it("offers copy and quote of a selection inside the bubble", async () => {
    const user = userEvent.setup();
    const onQuote = vi.fn();
    const message = msg();
    const { container } = render(<MessageBubble message={message} onQuote={onQuote} />);
    selectText(screen.getByText("hello there"));
    fireEvent.contextMenu(bubble(container));
    expect(menuLabels()).toEqual(["Copy selection", "Quote selection", "Quote reply", "Copy link to message"]);
    expect(hasSeparator("Copy selection")).toBe(false);
    await user.click(menuItem("Quote selection"));
    expect(onQuote).toHaveBeenCalledWith({ ...message, content: "hello there" });

    selectText(screen.getByText("hello there"));
    fireEvent.contextMenu(bubble(container));
    // jsdom drops the selection when the menu takes focus; a browser keeps it.
    selectText(screen.getByText("hello there"));
    fireEvent.click(menuItem("Copy selection"));
    expect(clipboardMock).toHaveBeenCalledWith("hello there");
  });

  it("ignores a selection outside the bubble", () => {
    const outside = document.createElement("p");
    outside.textContent = "elsewhere";
    document.body.appendChild(outside);
    const { container } = render(<MessageBubble message={msg()} onQuote={vi.fn()} />);
    selectText(outside);
    fireEvent.contextMenu(bubble(container));
    expect(menuLabels()).toEqual(["Quote reply", "Copy link to message"]);
    outside.remove();
  });

  it("offers copy link on a non-web link and sets the message items apart", async () => {
    const user = userEvent.setup();
    const { container } = render(<MessageBubble message={msg({ content: "[mail](mailto:a@example.com)" })} onQuote={vi.fn()} />);
    fireEvent.contextMenu(screen.getByText("mail"));
    expect(menuLabels()).toEqual(["Copy link", "Quote reply", "Copy link to message"]);
    expect(hasSeparator("Quote reply")).toBe(true);
    await user.click(menuItem("Copy link"));
    expect(clipboardMock).toHaveBeenCalledWith("mailto:a@example.com");
    expect(bubble(container)).toBeTruthy();
  });

  it("separates the selection items from a link's items", () => {
    render(<MessageBubble message={msg({ content: "[mail](mailto:a@example.com)" })} onQuote={vi.fn()} />);
    selectText(screen.getByText("mail"));
    fireEvent.contextMenu(screen.getByText("mail"));
    expect(menuLabels()).toEqual(["Copy link", "Copy selection", "Quote selection", "Quote reply", "Copy link to message"]);
    expect(hasSeparator("Copy selection")).toBe(true);
    expect(hasSeparator("Quote reply")).toBe(false);
  });

  it("opens a web link in the host browser outside a channel", async () => {
    const user = userEvent.setup();
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    render(<MessageBubble message={msg({ content: "[site](https://example.com/x)" })} onQuote={vi.fn()} />);
    fireEvent.contextMenu(screen.getByText("site"));
    expect(menuLabels()).toEqual(["Copy link", "Open in Host browser", "Quote reply", "Copy link to message"]);
    await user.click(menuItem("Open in Host browser"));
    expect(open).toHaveBeenCalledWith("https://example.com/x", "_blank", "noopener,noreferrer");
  });

  it("opens a web link in the channel's Docker browser", async () => {
    const user = userEvent.setup();
    const request = vi.spyOn(browserOpenQueue, "request").mockImplementation(() => {});
    render(
      <ChannelContext.Provider value="ch-9">
        <MessageBubble message={msg({ content: "[site](https://example.com/x)" })} onQuote={vi.fn()} />
      </ChannelContext.Provider>,
    );
    fireEvent.contextMenu(screen.getByText("site"));
    expect(menuLabels()).toEqual(["Copy link", "Open in Docker browser", "Open in Host browser", "Quote reply", "Copy link to message"]);
    await user.click(menuItem("Open in Docker browser"));
    expect(request).toHaveBeenCalledWith(window, "ch-9", "docker", "https://example.com/x");
  });

  it.each(["Copy link", "Copy selection", "Copy link to message"])("swallows a clipboard failure from %s", async (label) => {
    const user = userEvent.setup();
    clipboardMock.mockRejectedValue(new Error("blocked"));
    render(<MessageBubble message={msg({ content: "[mail](mailto:a@example.com)" })} onQuote={vi.fn()} />);
    selectText(screen.getByText("mail"));
    fireEvent.contextMenu(screen.getByText("mail"));
    await user.click(menuItem(label));
    expect(clipboardMock).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).toBeNull();
  });
});

describe("StreamingBubble", () => {
  it("renders the streaming content", () => {
    render(<StreamingBubble content="partial **text**" />);
    expect(screen.getByText("streaming...")).toBeTruthy();
    expect(screen.getByText("text").tagName).toBe("STRONG");
  });
});

describe("TriggerQuote", () => {
  it("truncates long content and jumps on click", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(<TriggerQuote content={"x".repeat(130)} time="2026-01-01T10:30:00Z" onClick={onClick} />);
    const quote = screen.getByTestId("trigger-quote");
    expect(quote.title).toBe("Jump to message");
    expect(screen.getByText(`${"x".repeat(120)}…`)).toBeTruthy();
    expect(quote.querySelectorAll("span")).toHaveLength(2);
    await user.click(quote);
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("shows short content without time or click", () => {
    render(<TriggerQuote content="short" />);
    const quote = screen.getByTestId("trigger-quote");
    expect(quote.title).toBe("");
    expect(quote.style.cursor).toBe("default");
    expect(screen.getByText("short")).toBeTruthy();
    expect(quote.querySelectorAll("span")).toHaveLength(1);
  });
});

describe("AgentActivityIndicator", () => {
  it.each([
    [{ activity: "model", model: "opus" }, "opus"],
    [{ activity: "subagent_started", description: "explore" }, "Agent: explore"],
    [{ activity: "subagent_started" }, "Agent: "],
    [{ activity: "subagent_progress", description: "searching" }, "searching"],
    [{ activity: "compacting" }, "Compacting context..."],
    [{ activity: "thinking", description: "512" }, "Thinking… (512 tokens)"],
    [{ activity: "thinking", description: "0" }, "Thinking…"],
    [{ activity: "thinking" }, "Thinking…"],
    [{ activity: "rate_limited", description: "retry in 5s" }, "retry in 5s"],
    [{ activity: "rate_limited" }, "Rate limited — retrying…"],
    [{ activity: "image_build", description: "building layer" }, "building layer"],
    [{ activity: "image_build" }, "Waiting for the agent image build…"],
    [{ activity: "tool_progress", description: "Bash 3s" }, "Bash 3s"],
    [{ activity: "task_notification", description: "done" }, "Background task: done"],
    [{ activity: "api_retry", description: "attempt 2" }, "API retry: attempt 2"],
    [{ activity: "background_tasks", description: "2 tasks" }, "Waiting on 2 tasks"],
  ] as const)("labels %o", (activity, label) => {
    render(<AgentActivityIndicator activity={activity} />);
    expect(screen.getByText(label, { normalizer: getDefaultNormalizer({ trim: false }) })).toBeTruthy();
  });

  it.each([
    { activity: "model" },
    { activity: "subagent_progress" },
    { activity: "tool_progress" },
    { activity: "task_notification" },
    { activity: "api_retry" },
    { activity: "background_tasks" },
    { activity: "image_ready" },
  ] as const)("renders nothing for an unlabeled %o", (activity) => {
    const { container } = render(<AgentActivityIndicator activity={activity} />);
    expect(container.innerHTML).toBe("");
  });

  it("cuts a long label at 100 characters", () => {
    render(<AgentActivityIndicator activity={{ activity: "tool_progress", description: "y".repeat(150) }} />);
    expect(screen.getByText(`${"y".repeat(100)}...`)).toBeTruthy();
  });
});

describe("CompletionSummary", () => {
  it("joins model, duration, turns and stop reason", () => {
    render(<CompletionSummary info={{ model: "opus", duration_ms: 2500, num_turns: 3, stop_reason: "end_turn" }} />);
    expect(screen.getByText("opus · 2.5s · 3 turns · end_turn")).toBeTruthy();
  });

  it("uses the singular for one turn", () => {
    render(<CompletionSummary info={{ num_turns: 1 }} />);
    expect(screen.getByText("1 turn")).toBeTruthy();
  });

  it("renders nothing without info", () => {
    const { container } = render(<CompletionSummary info={{}} />);
    expect(container.innerHTML).toBe("");
  });
});

describe("ToolActivityIndicator", () => {
  it("expands and collapses a long command", async () => {
    const user = userEvent.setup();
    const input = "echo " + "a".repeat(100);
    render(<ToolActivityIndicator toolName="Bash" input={input} />);
    const preview = screen.getByTitle("Show full command");
    expect(preview.textContent).toBe(`${input.slice(0, 80)}...`);
    await user.click(preview);
    const full = screen.getByTitle("Collapse command");
    expect(full.textContent).toBe(input);
    expect(screen.queryByTitle("Show full command")).toBeNull();
    await user.click(full);
    expect(screen.getByTitle("Show full command")).toBeTruthy();
  });

  it("never truncates a file path tool's input", async () => {
    const user = userEvent.setup();
    const input = "/work/" + "d/".repeat(60) + "file.go";
    render(<ToolActivityIndicator toolName="Read" input={input} />);
    const shown = screen.getByText(input);
    expect(shown.title).toBe("");
    expect(shown.style.cursor).toBe("default");
    await user.click(shown);
    expect(screen.queryByTitle("Collapse command")).toBeNull();
  });

  it("shows only the tool name without input or result", () => {
    const { container } = render(<ToolActivityIndicator toolName="Task" />);
    expect(screen.getByText("Task")).toBeTruthy();
    expect(container.querySelectorAll("button")).toHaveLength(0);
  });

  it("shows a short result as is", async () => {
    const user = userEvent.setup();
    render(<ToolActivityIndicator toolName="Bash" input="ls" result={{ text: "a.go", is_error: false, truncated: false }} />);
    const result = screen.getByText("a.go");
    expect(result.style.cursor).toBe("default");
    await user.click(result);
    expect(result.style.whiteSpace).toBe("normal");
  });

  it("expands and collapses a long result", async () => {
    const user = userEvent.setup();
    const text = "line ".repeat(40);
    render(<ToolActivityIndicator toolName="Bash" input="cat x" result={{ text, is_error: true, truncated: true }} />);
    const result = screen.getByText(`${text.slice(0, 120)}...`);
    expect(result.style.cursor).toBe("pointer");
    expect(screen.getByText("(truncated)")).toBeTruthy();
    await user.click(result);
    expect(result.style.whiteSpace).toBe("pre-wrap");
    expect(result.firstChild?.textContent).toBe(text);
    await user.click(result);
    expect(result.style.whiteSpace).toBe("normal");
  });

  it("fades the copy buttons in on hover", () => {
    const { container } = render(<ToolActivityIndicator toolName="Bash" input="ls" result={{ text: "out", is_error: false, truncated: false }} />);
    const root = container.firstChild as HTMLElement;
    const buttons = () => [...container.querySelectorAll("button")].map((b) => b.style.opacity);
    expect(buttons()).toEqual(["0", "0"]);
    fireEvent.mouseEnter(root);
    expect(buttons()).toEqual(["1", "1"]);
    fireEvent.mouseLeave(root);
    expect(buttons()).toEqual(["0", "0"]);
  });
});

describe("ThinkingBubble", () => {
  it("toggles a long thought between preview and full text", async () => {
    const user = userEvent.setup();
    const text = "z".repeat(250);
    render(<ThinkingBubble text={text} truncated={false} />);
    expect(screen.getByText(`${"z".repeat(200)}...`)).toBeTruthy();
    await user.click(screen.getByText("Thinking"));
    expect(screen.getByText(text)).toBeTruthy();
    await user.click(screen.getByText("Thinking"));
    expect(screen.getByText(`${"z".repeat(200)}...`)).toBeTruthy();
    expect(screen.queryByText("truncated")).toBeNull();
  });

  it("renders without text", () => {
    render(<ThinkingBubble truncated />);
    expect(screen.getByText("Thinking")).toBeTruthy();
    expect(screen.getByText("truncated")).toBeTruthy();
  });
});

describe("TaskChecklist", () => {
  it("lists tasks by status with the completed count", () => {
    render(
      <TaskChecklist
        tasks={[
          { id: "1", subject: "Plan", status: "completed" },
          { id: "2", subject: "Build", activeForm: "Building", status: "in_progress" },
          { id: "3", subject: "Ship", status: "in_progress" },
          { id: "4", subject: "Test", status: "pending" },
        ]}
      />,
    );
    expect(screen.getByText("Tasks 1/4")).toBeTruthy();
    expect(screen.getByText("Plan").style.textDecoration).toBe("line-through");
    expect(screen.getByText("Building")).toBeTruthy();
    expect(screen.getByText("Ship").style.opacity).toBe("1");
    expect(screen.getByText("Test").style.opacity).toBe("0.6");
    expect(screen.getAllByText("☑")).toHaveLength(1);
    expect(screen.getAllByText("☐")).toHaveLength(3);
  });
});

describe("AskUserQuestionCard", () => {
  const single: AskUserQuestion = {
    question: "Which DB?",
    header: "Storage",
    options: [{ label: "SQLite", description: "Embedded", preview: "db.Open()" }, { label: "Postgres", description: "Server" }, { label: "Memory", preview: "map[string]any{}" }, { label: "None" }],
  };
  const multi: AskUserQuestion = { question: "Which features?", multiSelect: true, options: [{ label: "A" }, { label: "B" }] };

  function renderCard(props: Partial<Parameters<typeof AskUserQuestionCard>[0]> = {}) {
    const onSent = vi.fn();
    const user = userEvent.setup();
    render(<AskUserQuestionCard questions={[single]} channelId="ch-1" mode="agent" onSent={onSent} {...props} />);
    return { onSent, user };
  }

  const sendBtn = () => screen.getByRole("button", { name: /Send Answers|Sending/ }) as HTMLButtonElement;
  const option = (label: string) => screen.getByRole("button", { name: new RegExp(`${label}$`) });

  it("sends the chosen single-select answer", async () => {
    const { onSent, user } = renderCard();
    expect(screen.getByText("Storage")).toBeTruthy();
    expect(screen.queryByText("multi-select")).toBeNull();
    expect(sendBtn().disabled).toBe(true);
    await user.click(option("SQLite"));
    expect(option("SQLite").textContent).toBe("◉SQLite");
    await user.click(option("Postgres"));
    expect(option("SQLite").textContent).toBe("○SQLite");
    await user.click(sendBtn());
    expect(resolveAskMock).toHaveBeenCalledWith("ch-1", "answer", "Here are my answers:\n\nQ: Which DB?\nA: Postgres", "agent");
    await waitFor(() => expect(onSent).toHaveBeenCalledOnce());
    expect(sendBtn().textContent).toBe("Send Answers");
  });

  it("clears a single-select choice when clicked again", async () => {
    const { user } = renderCard();
    await user.click(option("SQLite"));
    await user.click(option("SQLite"));
    expect(option("SQLite").textContent).toBe("○SQLite");
    expect(sendBtn().disabled).toBe(true);
  });

  it("toggles multi-select options and sends them all", async () => {
    const { user } = renderCard({ questions: [multi, { question: "Bare?" }], mode: "plan" });
    expect(screen.getByText("multi-select")).toBeTruthy();
    await user.click(option("A"));
    await user.click(option("B"));
    expect(option("A").textContent).toBe("☑A");
    await user.click(option("A"));
    expect(option("A").textContent).toBe("☐A");
    expect(sendBtn().disabled).toBe(true);
    const others = screen.getAllByRole("button", { name: "Other..." });
    await user.click(others[1]!);
    await user.type(screen.getByPlaceholderText(/Type your answer/), "  free text  ");
    await user.click(sendBtn());
    expect(resolveAskMock).toHaveBeenCalledWith("ch-1", "answer", "Here are my answers:\n\nQ: Which features?\nA: B\n\nQ: Bare?\nA: free text", "plan");
  });

  it("sends a placeholder for an empty Other answer and works without onSent", async () => {
    const user = userEvent.setup();
    render(<AskUserQuestionCard questions={[single]} channelId="ch-1" mode="agent" />);
    await user.click(screen.getByRole("button", { name: "Other..." }));
    await user.click(sendBtn());
    expect(resolveAskMock).toHaveBeenCalledWith("ch-1", "answer", "Here are my answers:\n\nQ: Which DB?\nA: (no answer)", "agent");
    await waitFor(() => expect(sendBtn().textContent).toBe("Send Answers"));
  });

  it("sends from the Other box with Ctrl or Cmd+Enter only when every question is answered", async () => {
    const { user } = renderCard({ questions: [single, multi] });
    const others = screen.getAllByRole("button", { name: "Other..." });
    await user.click(others[0]!);
    const box = screen.getByPlaceholderText(/Type your answer/);
    fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
    expect(resolveAskMock).not.toHaveBeenCalled();
    await user.click(option("A"));
    fireEvent.keyDown(box, { key: "Enter" });
    fireEvent.keyDown(box, { key: "a", ctrlKey: true });
    expect(resolveAskMock).not.toHaveBeenCalled();
    const call = pending();
    resolveAskMock.mockReturnValue(call.promise);
    fireEvent.keyDown(box, { key: "Enter", metaKey: true });
    expect(resolveAskMock).toHaveBeenCalledOnce();
    expect(sendBtn().textContent).toBe("Sending...");
    fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
    expect(resolveAskMock).toHaveBeenCalledOnce();
    await act(async () => call.resolve());
    expect(sendBtn().textContent).toBe("Send Answers");
  });

  it("deselects Other and hides its box", async () => {
    const { user } = renderCard();
    const other = screen.getByRole("button", { name: "Other..." });
    await user.click(other);
    expect(screen.getByPlaceholderText(/Type your answer/)).toBeTruthy();
    await user.click(other);
    expect(screen.queryByPlaceholderText(/Type your answer/)).toBeNull();
  });

  it("keeps the card when sending fails", async () => {
    resolveAskMock.mockRejectedValue(new Error("down"));
    const { onSent, user } = renderCard();
    await user.click(option("None"));
    await user.click(sendBtn());
    await waitFor(() => expect(sendBtn().textContent).toBe("Send Answers"));
    expect(onSent).not.toHaveBeenCalled();
  });

  it("skips the question", async () => {
    const { onSent, user } = renderCard();
    await user.click(screen.getByRole("button", { name: "Skip" }));
    expect(resolveAskMock).toHaveBeenCalledWith("ch-1", "skip");
    await waitFor(() => expect(onSent).toHaveBeenCalledOnce());
  });

  it("skips without onSent", async () => {
    const user = userEvent.setup();
    render(<AskUserQuestionCard questions={[single]} channelId="ch-1" mode="agent" />);
    await user.click(screen.getByRole("button", { name: "Skip" }));
    expect(resolveAskMock).toHaveBeenCalledWith("ch-1", "skip");
    await waitFor(() => expect((screen.getByRole("button", { name: "Skip" }) as HTMLButtonElement).disabled).toBe(false));
  });

  it("keeps the card when skipping fails", async () => {
    resolveAskMock.mockRejectedValue(new Error("down"));
    const { onSent, user } = renderCard();
    await user.click(screen.getByRole("button", { name: "Skip" }));
    await waitFor(() => expect((screen.getByRole("button", { name: "Skip" }) as HTMLButtonElement).disabled).toBe(false));
    expect(onSent).not.toHaveBeenCalled();
  });

  it("shows the hovered option's description and preview, and hides them on leave", async () => {
    const { user } = renderCard();
    await user.hover(option("SQLite"));
    const details = screen.getByTestId("ask-option-details");
    expect(details.textContent).toBe("SQLiteEmbeddeddb.Open()");
    expect((details.querySelector("pre") as HTMLElement).style.marginTop).toBe("6px");

    await user.hover(option("Postgres"));
    expect(screen.getByTestId("ask-option-details").textContent).toBe("PostgresServer");

    await user.hover(option("Memory"));
    const memory = screen.getByTestId("ask-option-details");
    expect(memory.textContent).toBe("Memorymap[string]any{}");
    expect((memory.querySelector("pre") as HTMLElement).style.marginTop).toBe("4px");

    await user.hover(option("None"));
    expect(screen.queryByTestId("ask-option-details")).toBeNull();

    await user.hover(screen.getByRole("button", { name: "Other..." }));
    expect(screen.queryByTestId("ask-option-details")).toBeNull();

    await user.hover(option("SQLite"));
    fireEvent.mouseLeave(option("SQLite").parentElement!.parentElement!);
    expect(screen.queryByTestId("ask-option-details")).toBeNull();
  });

  it("shows details on keyboard focus and hides them on blur", () => {
    renderCard();
    fireEvent.focus(option("Postgres"));
    expect(screen.getByTestId("ask-option-details")).toBeTruthy();
    fireEvent.blur(option("Postgres"));
    expect(screen.queryByTestId("ask-option-details")).toBeNull();
    fireEvent.blur(option("Postgres"));
    expect(screen.queryByTestId("ask-option-details")).toBeNull();
  });
});

describe("ExitPlanCard", () => {
  const longPlan = { plan: ["1", "2", "3", "4", "5", "6"].join("\n") };

  function renderCard(props: Partial<Parameters<typeof ExitPlanCard>[0]> = {}) {
    const setMode = vi.fn();
    const onSent = vi.fn();
    const user = userEvent.setup();
    render(<ExitPlanCard plan={{ plan: "do it" }} channelId="ch-1" setMode={setMode} onSent={onSent} {...props} />);
    return { setMode, onSent, user };
  }

  const btn = (name: string) => screen.getByRole("button", { name }) as HTMLButtonElement;

  it("shows a short plan without the expand toggle", () => {
    renderCard();
    expect(screen.getByText("do it")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Show all/ })).toBeNull();
  });

  it("expands and collapses a long plan", async () => {
    const { user } = renderCard({ plan: longPlan });
    expect(screen.getByText(/^1\s+2\s+3\s+4\s+5$/)).toBeTruthy();
    await user.click(btn("Show all (6 lines)"));
    expect(screen.getByText(/^1\s+2\s+3\s+4\s+5\s+6$/)).toBeTruthy();
    await user.click(btn("Show less"));
    expect(btn("Show all (6 lines)")).toBeTruthy();
  });

  it("approves and switches to agent mode", async () => {
    const { setMode, onSent, user } = renderCard();
    const call = pending();
    resolvePlanMock.mockReturnValue(call.promise);
    await user.click(btn("Approve & Execute"));
    expect(btn("Discard").disabled).toBe(true);
    expect(setMode).not.toHaveBeenCalled();
    await act(async () => call.resolve());
    expect(resolvePlanMock).toHaveBeenCalledWith("ch-1", "approve");
    expect(setMode).toHaveBeenCalledWith("agent");
    expect(onSent).toHaveBeenCalledOnce();
    expect(btn("Discard").disabled).toBe(false);
  });

  it("approves without onSent", async () => {
    const setMode = vi.fn();
    const user = userEvent.setup();
    render(<ExitPlanCard plan={{ plan: "p" }} channelId="ch-1" setMode={setMode} />);
    await user.click(btn("Approve & Execute"));
    await waitFor(() => expect(setMode).toHaveBeenCalledWith("agent"));
  });

  it("keeps the mode when approving fails", async () => {
    resolvePlanMock.mockRejectedValue(new Error("down"));
    const { setMode, onSent, user } = renderCard();
    await user.click(btn("Approve & Execute"));
    await waitFor(() => expect(btn("Approve & Execute").disabled).toBe(false));
    expect(setMode).not.toHaveBeenCalled();
    expect(onSent).not.toHaveBeenCalled();
  });

  it("discards the plan and returns to plan mode", async () => {
    const { setMode, onSent, user } = renderCard();
    await user.click(btn("Discard"));
    expect(setMode).toHaveBeenCalledWith("plan");
    expect(resolvePlanMock).toHaveBeenCalledWith("ch-1", "reject");
    await waitFor(() => expect(onSent).toHaveBeenCalledOnce());
  });

  it("discards without onSent", async () => {
    const setMode = vi.fn();
    const user = userEvent.setup();
    render(<ExitPlanCard plan={{ plan: "p" }} channelId="ch-1" setMode={setMode} />);
    await user.click(btn("Discard"));
    expect(resolvePlanMock).toHaveBeenCalledWith("ch-1", "reject");
    await waitFor(() => expect(btn("Discard").disabled).toBe(false));
  });

  it("keeps the card when discarding fails", async () => {
    resolvePlanMock.mockRejectedValue(new Error("down"));
    const { onSent, user } = renderCard();
    await user.click(btn("Discard"));
    await waitFor(() => expect(btn("Discard").disabled).toBe(false));
    expect(onSent).not.toHaveBeenCalled();
  });

  it("requests changes with the trimmed prompt", async () => {
    const { setMode, onSent, user } = renderCard();
    await user.click(btn("Request Changes"));
    expect(btn("Send changes").disabled).toBe(true);
    await user.type(screen.getByPlaceholderText(/Describe the changes/), "  smaller steps ");
    await user.click(btn("Send changes"));
    expect(setMode).toHaveBeenCalledWith("plan");
    expect(resolvePlanMock).toHaveBeenCalledWith("ch-1", "deny", "smaller steps", "plan");
    await waitFor(() => expect(onSent).toHaveBeenCalledOnce());
  });

  it("requests changes without onSent", async () => {
    const user = userEvent.setup();
    render(<ExitPlanCard plan={{ plan: "p" }} channelId="ch-1" setMode={vi.fn()} />);
    await user.click(btn("Request Changes"));
    await user.type(screen.getByPlaceholderText(/Describe the changes/), "more");
    await user.click(btn("Send changes"));
    expect(resolvePlanMock).toHaveBeenCalledWith("ch-1", "deny", "more", "plan");
    await waitFor(() => expect(btn("Cancel").disabled).toBe(false));
  });

  it("keeps the changes box when the request fails", async () => {
    resolvePlanMock.mockRejectedValue(new Error("down"));
    const { onSent, user } = renderCard();
    await user.click(btn("Request Changes"));
    await user.type(screen.getByPlaceholderText(/Describe the changes/), "x");
    await user.click(btn("Send changes"));
    await waitFor(() => expect(btn("Send changes").disabled).toBe(false));
    expect(onSent).not.toHaveBeenCalled();
  });

  it("cancels and clears the changes box", async () => {
    const { user } = renderCard();
    await user.click(btn("Request Changes"));
    await user.type(screen.getByPlaceholderText(/Describe the changes/), "draft");
    await user.click(btn("Cancel"));
    expect(screen.queryByPlaceholderText(/Describe the changes/)).toBeNull();
    await user.click(btn("Request Changes"));
    expect((screen.getByPlaceholderText(/Describe the changes/) as HTMLTextAreaElement).value).toBe("");
    await user.click(btn("Request Changes"));
    expect(screen.queryByPlaceholderText(/Describe the changes/)).toBeNull();
  });
});
