import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchAgentConfig, fetchComposerHistory, pasteImage, resolveAsk, resolvePlan } from "../../api/channels";
import { fetchShortcuts, type PromptShortcut } from "../../api/configApi";
import { fetchDictationStatus } from "../../api/dictation";
import { type FileSearchResult, type RootEntry, searchFiles } from "../../api/files";
import { resolveGateApproval, sendCommand, sendMessage } from "../../api/loopApi";
import { editPrompt } from "../../api/promptEdit";
import { useDictation } from "../../hooks/useDictation";
import type { Message } from "../../types";
import { ChatInput, type ChatInputProps } from "./ChatInput";
import type { PromptEditRequest } from "./dictationController";

vi.mock("../../api/channels", () => ({
  fetchAgentConfig: vi.fn(),
  fetchComposerHistory: vi.fn(),
  pasteImage: vi.fn(),
  resolveAsk: vi.fn(),
  resolvePlan: vi.fn(),
  updateAgentConfig: vi.fn(),
}));

vi.mock("../../api/configApi", () => ({
  GLOBAL_CONFIG_SAVED_EVENT: "loop:global-config-saved",
  fetchShortcuts: vi.fn(),
}));

vi.mock("../../api/dictation", () => ({
  fetchDictationStatus: vi.fn(),
  openDictationSocket: vi.fn(),
  startDictationModelDownload: vi.fn(),
}));

vi.mock("../../api/files", () => ({
  searchFiles: vi.fn(),
}));

vi.mock("../../api/loopApi", () => ({
  resolveGateApproval: vi.fn(),
  sendCommand: vi.fn(),
  sendMessage: vi.fn(),
}));

vi.mock("../../api/promptEdit", () => ({
  editPrompt: vi.fn(),
}));

// The real hook runs (its daemon calls are mocked above); the spy lets a test
// reach the callbacks the composer hands it, standing in for a dictation.
vi.mock("../../hooks/useDictation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../hooks/useDictation")>();
  return { useDictation: vi.fn(actual.useDictation) };
});

const askMock = vi.mocked(resolveAsk);
const planMock = vi.mocked(resolvePlan);
const gateMock = vi.mocked(resolveGateApproval);
const commandMock = vi.mocked(sendCommand);
const messageMock = vi.mocked(sendMessage);
const shortcutsMock = vi.mocked(fetchShortcuts);

function msg(content: string, extra: Partial<Message> = {}): Message {
  return {
    id: 1,
    channel_id: "ch-1",
    msg_id: "m-1",
    author_id: "u-1",
    author_name: "user",
    content,
    is_bot: false,
    is_processed: true,
    created_at: "2026-01-01T00:00:00Z",
    ...extra,
  };
}

function props(overrides: Partial<ChatInputProps> = {}): ChatInputProps {
  return { channelId: "ch-1", messages: [], mode: "agent", setMode: vi.fn(), onSent: vi.fn(), onDismissGate: vi.fn(), ...overrides };
}

function renderInput(overrides: Partial<ChatInputProps> = {}) {
  const user = userEvent.setup();
  const p = props(overrides);
  const view = render(<ChatInput {...p} />);
  const box = screen.getByRole("textbox") as HTMLTextAreaElement;
  return { ...view, box, props: p, user };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  vi.mocked(fetchComposerHistory).mockResolvedValue([]);
  vi.mocked(fetchAgentConfig).mockResolvedValue({ model: "", effort: "", default_model: "", default_effort: "", models: [] } as never);
  vi.mocked(fetchDictationStatus).mockRejectedValue(new Error("offline"));
  shortcutsMock.mockResolvedValue([]);
  askMock.mockResolvedValue(undefined);
  planMock.mockResolvedValue(undefined);
  gateMock.mockResolvedValue(undefined);
  commandMock.mockResolvedValue(undefined);
  messageMock.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(useDictation).mockReset();
});

describe("ChatInput send routing", () => {
  describe("plain send", () => {
    it("sends the trimmed text on Enter, clears the composer and fires onSent", async () => {
      const { box, props: p, user } = renderInput();
      await user.type(box, "  hello there  {Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledExactlyOnceWith("ch-1", "hello there", "agent", "queue");
      expect(box.value).toBe("");
      expect(askMock).not.toHaveBeenCalled();
      expect(planMock).not.toHaveBeenCalled();
      expect(gateMock).not.toHaveBeenCalled();
    });

    it("sends from the send button too", async () => {
      const { box, props: p, user } = renderInput();
      await user.type(box, "clicked");
      await user.click(screen.getByTitle(/^Send \(queue\)/));
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "clicked", "agent", "queue");
    });

    it("inserts a newline on Shift+Enter instead of sending", async () => {
      const { box, user } = renderInput();
      await user.type(box, "line one{Shift>}{Enter}{/Shift}line two");
      expect(box.value).toBe("line one\nline two");
      expect(messageMock).not.toHaveBeenCalled();
    });

    it.each([
      ["empty", ""],
      ["whitespace", "   "],
    ])("doesn't send an %s message", async (_, typed) => {
      const { box, props: p, user } = renderInput();
      if (typed) await user.type(box, typed);
      await user.type(box, "{Enter}");
      expect(messageMock).not.toHaveBeenCalled();
      expect(p.onSent).not.toHaveBeenCalled();
      expect(screen.getByTitle(/^Send \(queue\)/)).toHaveProperty("disabled", true);
    });

    it.each(["agent", "plan"] as const)("sends with the composer's %s mode", async (mode) => {
      const { box, props: p, user } = renderInput({ mode });
      await user.type(box, "do it{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "do it", mode, "queue");
    });

    it("prefixes a quoted message and clears the quote", async () => {
      const onClearQuote = vi.fn();
      const { box, props: p, user } = renderInput({ quotedMessage: msg("first\nsecond", { is_bot: true }), onClearQuote });
      await user.type(box, "why?{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "> first\n> second\n\nwhy?", "agent", "queue");
      expect(onClearQuote).toHaveBeenCalledOnce();
    });

    it("runs a /loop command through sendCommand", async () => {
      const { box, props: p, user } = renderInput();
      // Escape closes the command picker so Enter sends rather than picks.
      await user.type(box, "/loop status{Escape}{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(commandMock).toHaveBeenCalledExactlyOnceWith("ch-1", "status");
      expect(messageMock).not.toHaveBeenCalled();
    });
  });

  describe("parked channel", () => {
    it("auto-denies a pending gate, steers the text and dismisses the card", async () => {
      const { box, props: p, user } = renderInput({ pendingGateReqId: "req-9" });
      await user.type(box, "use the mirror{Enter}");
      await waitFor(() => expect(p.onDismissGate).toHaveBeenCalledOnce());
      expect(gateMock).toHaveBeenCalledExactlyOnceWith("req-9", "deny");
      expect(messageMock).toHaveBeenCalledExactlyOnceWith("ch-1", "use the mirror", "agent", "steer");
      expect(gateMock.mock.invocationCallOrder[0]).toBeLessThan(messageMock.mock.invocationCallOrder[0]!);
      expect(commandMock).not.toHaveBeenCalled();
      expect(p.onSent).toHaveBeenCalledOnce();
    });

    it("denies a pending plan with the text as the prompt", async () => {
      const { box, props: p, user } = renderInput({ hasPendingExitPlan: true, mode: "plan" });
      await user.type(box, "smaller steps{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(planMock).toHaveBeenCalledExactlyOnceWith("ch-1", "deny", "smaller steps", "plan");
      expect(messageMock).not.toHaveBeenCalled();
      expect(p.onDismissGate).not.toHaveBeenCalled();
    });

    it("answers a pending question with the text", async () => {
      const { box, props: p, user } = renderInput({ hasPendingAskUser: true });
      await user.type(box, "option B{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(askMock).toHaveBeenCalledExactlyOnceWith("ch-1", "answer", "option B", "agent");
      expect(messageMock).not.toHaveBeenCalled();
      expect(p.onDismissGate).not.toHaveBeenCalled();
    });

    it.each([
      ["ask over plan and gate", { hasPendingAskUser: true, hasPendingExitPlan: true, pendingGateReqId: "req-1" }, "ask"],
      ["plan over gate", { hasPendingExitPlan: true, pendingGateReqId: "req-1" }, "plan"],
    ] as const)("resolves %s", async (_, parked, want) => {
      const { box, props: p, user } = renderInput({ ...parked, isRunning: true });
      await user.type(box, "x{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(askMock).toHaveBeenCalledTimes(want === "ask" ? 1 : 0);
      expect(planMock).toHaveBeenCalledTimes(want === "plan" ? 1 : 0);
      expect(gateMock).not.toHaveBeenCalled();
      expect(messageMock).not.toHaveBeenCalled();
    });

    it("hides Continue while a card is parked", () => {
      renderInput({ messages: [msg("hi")], hasPendingAskUser: true });
      expect(screen.queryByRole("button", { name: "Continue" })).toBeNull();
    });
  });

  describe("while running", () => {
    it("queues by default and shows Stop instead of Send", async () => {
      const { box, props: p, user } = renderInput({ isRunning: true });
      expect(screen.queryByTitle(/^Send \(/)).toBeNull();
      await user.type(box, "later{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "later", "agent", "queue");
    });

    it.each([
      ["add", "add"],
      ["steer", "steer"],
    ] as const)("delivers with the chosen %s send mode and remembers it", async (key, delivery) => {
      const { box, props: p, user } = renderInput({ isRunning: true });
      await user.click(screen.getByTestId("send-mode-chip"));
      await user.click(screen.getByTestId(`send-mode-${key}`));
      expect(screen.getByTestId("send-mode-chip").textContent).toContain(key.toUpperCase());
      expect(localStorage.getItem("loop-send-mode")).toBe(JSON.stringify(key));
      await user.type(box, "now{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "now", "agent", delivery);
    });

    it.each([
      ["Meta+Enter steers", "{Meta>}{Enter}{/Meta}", "queue", "steer"],
      ["Control+Enter steers", "{Control>}{Enter}{/Control}", "queue", "steer"],
      ["Alt+Enter adds", "{Alt>}{Enter}{/Alt}", "queue", "add"],
      ["Meta+Enter queues when steer is the mode", "{Meta>}{Enter}{/Meta}", "steer", "queue"],
      ["Alt+Enter queues when add is the mode", "{Alt>}{Enter}{/Alt}", "add", "queue"],
    ])("%s for one send", async (_, keys, stored, delivery) => {
      localStorage.setItem("loop-send-mode", JSON.stringify(stored));
      const { box, props: p, user } = renderInput({ isRunning: true });
      await user.type(box, "hey");
      await user.keyboard(keys);
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "hey", "agent", delivery);
    });

    it("reads the old 'interrupt' send mode as steer", async () => {
      localStorage.setItem("loop-send-mode", JSON.stringify("interrupt"));
      const { box, props: p, user } = renderInput({ isRunning: true });
      await user.type(box, "stop that{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "stop that", "agent", "steer");
    });

    it("ignores the send mode when nothing is running", async () => {
      localStorage.setItem("loop-send-mode", JSON.stringify("steer"));
      const { box, props: p, user } = renderInput();
      await user.type(box, "go{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "go", "agent", "queue");
    });

    it("stops the run, dismisses the gate and then sends as idle", async () => {
      localStorage.setItem("loop-send-mode", JSON.stringify("steer"));
      const { box, props: p, user } = renderInput({ isRunning: true });
      await user.click(screen.getByTitle("Stop"));
      expect(commandMock).toHaveBeenCalledExactlyOnceWith("ch-1", "stop");
      expect(p.onDismissGate).toHaveBeenCalledOnce();
      expect(screen.getByTitle(/^Send \(steer\)/)).toBeTruthy();
      await user.type(box, "after{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "after", "agent", "queue");
    });
  });

  describe("other senders", () => {
    it("Continue sends 'continue' and keeps the typed draft", async () => {
      const { box, props: p, user } = renderInput({ messages: [msg("done")] });
      await user.type(box, "draft");
      await user.click(screen.getByRole("button", { name: "Continue" }));
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "continue", "agent", "queue");
      expect(box.value).toBe("draft");
    });

    it("a picked prompt shortcut goes through the same parked-channel routing", async () => {
      const sc: PromptShortcut = { name: "review", description: "Review it", prompt: "Review the diff", scope: "global" };
      shortcutsMock.mockResolvedValue([sc]);
      const { box, props: p, user } = renderInput({ hasPendingAskUser: true });
      await screen.findByTitle("Prompt shortcuts");
      await user.type(box, "#rev{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(askMock).toHaveBeenCalledWith("ch-1", "answer", "#review\nReview the diff", "agent");
      expect(messageMock).not.toHaveBeenCalled();
    });
  });

  describe("editing a queued message", () => {
    const queued = msg("queued text", { msg_id: "q-1", is_processed: false });

    async function startEditing(onSaveEdit: ChatInputProps["onSaveEdit"], onCancelEdit = vi.fn()) {
      const user = userEvent.setup();
      const p = props({ onSaveEdit, onCancelEdit });
      const view = render(<ChatInput {...p} />);
      const box = screen.getByRole("textbox") as HTMLTextAreaElement;
      await user.type(box, "my draft");
      view.rerender(<ChatInput {...p} editingQueued={queued} />);
      await waitFor(() => expect(box.value).toBe("queued text"));
      expect(screen.getByTestId("queued-edit-banner")).toBeTruthy();
      return { box, p, user, onCancelEdit };
    }

    it("saves the edit on Enter instead of sending, then restores the parked draft", async () => {
      const onSaveEdit = vi.fn().mockResolvedValue("saved");
      const { box, p, user } = await startEditing(onSaveEdit);
      await user.type(box, " v2{Enter}");
      await waitFor(() => expect(box.value).toBe("my draft"));
      expect(onSaveEdit).toHaveBeenCalledExactlyOnceWith("queued text v2");
      expect(messageMock).not.toHaveBeenCalled();
      expect(p.onSent).not.toHaveBeenCalled();
    });

    it("keeps the edited text when the message already started", async () => {
      const onSaveEdit = vi.fn().mockResolvedValue("started");
      const { box, user } = await startEditing(onSaveEdit);
      await user.type(box, "!");
      await user.click(screen.getByTestId("queued-edit-save"));
      await waitFor(() => expect(onSaveEdit).toHaveBeenCalledWith("queued text!"));
      await act(async () => {});
      expect(box.value).toBe("queued text!");
      expect(messageMock).not.toHaveBeenCalled();
    });

    it.each([
      ["the Cancel button", async (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByTestId("queued-edit-cancel"))],
      ["Escape", async (user: ReturnType<typeof userEvent.setup>) => user.keyboard("{Escape}")],
    ])("cancels with %s and restores the parked draft", async (_, cancel) => {
      const onSaveEdit = vi.fn();
      const { box, user, onCancelEdit } = await startEditing(onSaveEdit);
      box.focus();
      await cancel(user);
      expect(onCancelEdit).toHaveBeenCalledOnce();
      expect(box.value).toBe("my draft");
      expect(onSaveEdit).not.toHaveBeenCalled();
      expect(messageMock).not.toHaveBeenCalled();
    });
  });
});

const draftsOf = () => JSON.parse(localStorage.getItem("loop-chat-drafts") ?? "{}") as Record<string, string>;

// deferred returns a promise and the functions that settle it.
function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// nextFrame lets pending requestAnimationFrame callbacks run.
const nextFrame = () => act(() => new Promise<void>((r) => requestAnimationFrame(() => r())));

describe("ChatInput composer", () => {
  describe("run state", () => {
    it("drops the optimistic stop once the run state changes", async () => {
      const p = props({ isRunning: true });
      const user = userEvent.setup();
      const { rerender } = render(<ChatInput {...p} />);
      await user.click(screen.getByTitle("Stop"));
      expect(screen.queryByTitle("Stop")).toBeNull();
      rerender(<ChatInput {...p} isRunning={false} />);
      expect(screen.queryByTitle("Stop")).toBeNull();
      rerender(<ChatInput {...p} isRunning />);
      expect(screen.getByTitle("Stop")).toBeTruthy();
      rerender(<ChatInput {...p} isRunning={false} />);
      expect(screen.getByTitle(/^Send \(queue\)/)).toBeTruthy();
    });
  });

  describe("dictation", () => {
    const hookArgs = () => {
      const args = vi.mocked(useDictation).mock.lastCall!;
      return { setText: args[1], send: args[2]!, edit: args[3]! };
    };

    it("writes dictated text into the composer and its draft", () => {
      const { box } = renderInput();
      act(() => hookArgs().setText("dictated words"));
      expect(box.value).toBe("dictated words");
      expect(draftsOf()["ch-1"]).toBe("dictated words");
      act(() => hookArgs().setText(""));
      expect(box.value).toBe("");
      expect(draftsOf()["ch-1"]).toBeUndefined();
    });

    it("sends the dictated text when the dictation ends with a send phrase", async () => {
      const { props: p } = renderInput();
      act(() => hookArgs().setText("ship it"));
      act(() => hookArgs().send());
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "ship it", "agent", "queue");
    });

    it("edits the draft through the channel's prompt edit", async () => {
      vi.mocked(editPrompt).mockResolvedValue("rewritten" as never);
      renderInput();
      const req = { text: "draft", instruction: "shorter" } as unknown as PromptEditRequest;
      const signal = new AbortController().signal;
      await hookArgs().edit(req, signal);
      expect(editPrompt).toHaveBeenCalledWith("ch-1", req, signal);
    });

    it("leaves the text to the dictation while it is busy", async () => {
      const real = vi.mocked(useDictation).getMockImplementation()!;
      vi.mocked(useDictation).mockImplementation((...args) => ({ ...real(...args), busy: true }));
      const { box } = renderInput();
      expect(box.readOnly).toBe(true);
      act(() => hookArgs().setText("spoken"));
      fireEvent.keyDown(box, { key: "Enter" });
      expect(messageMock).not.toHaveBeenCalled();
    });
  });

  describe("history", () => {
    const up = (box: HTMLTextAreaElement) => {
      box.setSelectionRange(0, 0);
      fireEvent.keyDown(box, { key: "ArrowUp" });
    };
    const down = (box: HTMLTextAreaElement) => {
      box.setSelectionRange(box.value.length, box.value.length);
      fireEvent.keyDown(box, { key: "ArrowDown" });
    };

    it("walks the fetched history and back to the draft", async () => {
      vi.mocked(fetchComposerHistory).mockResolvedValue(["older", "newest"]);
      const { box, user } = renderInput({ messages: [msg("from the page")] });
      await act(async () => {});
      await user.type(box, "draft");
      up(box);
      expect(box.value).toBe("newest");
      up(box);
      expect(box.value).toBe("older");
      up(box);
      expect(box.value).toBe("older");
      await nextFrame();
      expect(box.selectionStart).toBe(0);
      down(box);
      expect(box.value).toBe("newest");
      down(box);
      expect(box.value).toBe("draft");
      expect(draftsOf()["ch-1"]).toBe("draft");
      await nextFrame();
      expect(box.selectionStart).toBe(5);
    });

    it("falls back to the page's own messages when the fetch fails, in StrictMode too", async () => {
      vi.mocked(fetchComposerHistory).mockRejectedValue(new Error("offline"));
      const p = props({ messages: [msg("page message"), msg("bot reply", { is_bot: true })] });
      render(
        <StrictMode>
          <ChatInput {...p} />
        </StrictMode>,
      );
      const box = screen.getByRole("textbox") as HTMLTextAreaElement;
      await act(async () => {});
      up(box);
      expect(box.value).toBe("page message");
      down(box);
      expect(box.value).toBe("");
      expect(draftsOf()["ch-1"]).toBeUndefined();
    });

    it("drops a late history for a channel it left", async () => {
      const d = deferred<string[]>();
      vi.mocked(fetchComposerHistory).mockReturnValueOnce(d.promise).mockResolvedValue([]);
      const p = props();
      const { rerender } = render(<ChatInput {...p} />);
      rerender(<ChatInput {...p} channelId="ch-2" />);
      await act(async () => d.resolve(["from ch-1"]));
      const box = screen.getByRole("textbox") as HTMLTextAreaElement;
      up(box);
      expect(box.value).toBe("");
    });

    it("keeps the caret keys when there's no history or the caret isn't at the edge", async () => {
      const { box, user } = renderInput();
      await act(async () => {});
      up(box);
      expect(box.value).toBe("");
      await user.type(box, "abc{Enter}");
      await waitFor(() => expect(box.value).toBe(""));
      await user.type(box, "xy");
      box.setSelectionRange(1, 1);
      fireEvent.keyDown(box, { key: "ArrowUp" });
      expect(box.value).toBe("xy");
      down(box);
      expect(box.value).toBe("xy");
    });

    it("pushes a bare /loop to history without running a command", async () => {
      const { box, props: p, user } = renderInput();
      await user.type(box, "/loop{Escape}{Enter}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(commandMock).not.toHaveBeenCalled();
      expect(box.value).toBe("");
      up(box);
      expect(box.value).toBe("/loop");
    });
  });

  describe("compose events", () => {
    const compose = (detail: unknown) => act(() => void window.dispatchEvent(new CustomEvent("loop:chat-compose", { detail })));

    it("appends the composed text, focuses and puts the caret at the end", async () => {
      const { box, user } = renderInput({ noAutoFocus: true });
      compose({ channelId: "ch-1", text: "> quoted" });
      expect(box.value).toBe("> quoted");
      await nextFrame();
      expect(document.activeElement).toBe(box);
      expect(box.selectionStart).toBe(box.value.length);
      await user.type(box, "\n\n");
      compose({ channelId: "ch-1", text: "more" });
      expect(box.value).toBe("> quoted\n\nmore");
      expect(draftsOf()["ch-1"]).toBe("> quoted\n\nmore");
    });

    it.each([
      ["another channel's", { channelId: "ch-2", text: "x" }],
      ["an empty", { channelId: "ch-1", text: "" }],
      ["a detail-less", null],
    ])("ignores %s compose event", (_, detail) => {
      const { box } = renderInput();
      compose(detail);
      expect(box.value).toBe("");
    });

    it("doesn't touch an unmounted composer", async () => {
      const { unmount } = renderInput();
      compose({ channelId: "ch-1", text: "late" });
      unmount();
      await nextFrame();
      expect(screen.queryByRole("textbox")).toBeNull();
    });
  });

  describe("layout", () => {
    it("grows to the window cap and scrolls past it, measuring by font size without a line height", async () => {
      const real = window.getComputedStyle;
      vi.spyOn(window, "getComputedStyle").mockImplementation((el) =>
        el instanceof HTMLTextAreaElement ? ({ lineHeight: "normal", fontSize: "10px", paddingTop: "2px", paddingBottom: "2px" } as CSSStyleDeclaration) : real(el),
      );
      vi.spyOn(Element.prototype, "scrollHeight", "get").mockReturnValue(5000);
      const { box, user } = renderInput();
      await user.type(box, "tall");
      expect(box.style.height).toBe(`${Math.min(Math.round(window.innerHeight * 0.4), 360)}px`);
      expect(box.style.overflowY).toBe("auto");
    });

    it("stays at three lines with little text", () => {
      const { box } = renderInput();
      expect(box.style.overflowY).toBe("hidden");
    });

    it("focuses on mount unless told not to", () => {
      const { unmount } = renderInput();
      expect(document.activeElement).toBe(screen.getByRole("textbox"));
      unmount();
      renderInput({ noAutoFocus: true });
      expect(document.activeElement).not.toBe(screen.getByRole("textbox"));
    });
  });

  describe("banners", () => {
    it("shows the trust banner and its Review button only with a handler", async () => {
      const onReviewTrust = vi.fn();
      const { rerender, props: p, user } = renderInput({ trustPending: true, onReviewTrust });
      await user.click(screen.getByTestId("project-trust-review"));
      expect(onReviewTrust).toHaveBeenCalledOnce();
      rerender(<ChatInput {...p} onReviewTrust={undefined} />);
      expect(screen.getByTestId("project-trust-banner")).toBeTruthy();
      expect(screen.queryByTestId("project-trust-review")).toBeNull();
    });

    it("shows an edit notice, dismisses it, and dismisses it on send", async () => {
      const onDismissEditNotice = vi.fn();
      const { box, user } = renderInput({ editNotice: "It already started", onDismissEditNotice });
      expect(screen.getByTestId("queued-edit-notice").textContent).toContain("It already started");
      await user.click(screen.getByTitle("Dismiss"));
      expect(onDismissEditNotice).toHaveBeenCalledOnce();
      await user.type(box, "again{Enter}");
      await waitFor(() => expect(onDismissEditNotice).toHaveBeenCalledTimes(2));
    });

    it("hides the edit notice and the quote while editing", () => {
      renderInput({ editNotice: "note", quotedMessage: msg("quoted"), editingQueued: msg("q", { msg_id: "q-1" }) });
      expect(screen.queryByTestId("queued-edit-notice")).toBeNull();
      expect(screen.queryByTitle("Remove quote")).toBeNull();
    });

    it("clips a long quote and removes it", async () => {
      const onClearQuote = vi.fn();
      const long = "q".repeat(130);
      const { user } = renderInput({ quotedMessage: msg(long), onClearQuote });
      expect(screen.getByText(`${"q".repeat(120)}…`)).toBeTruthy();
      await user.click(screen.getByTitle("Remove quote"));
      expect(onClearQuote).toHaveBeenCalledOnce();
    });
  });

  describe("mode and send menu", () => {
    it("switches between agent and plan", async () => {
      const { props: p, user } = renderInput();
      await user.click(screen.getByRole("button", { name: "Plan" }));
      await user.click(screen.getByRole("button", { name: "Agent" }));
      expect(vi.mocked(p.setMode).mock.calls).toEqual([["plan"], ["agent"]]);
    });

    it("highlights hovered modes and closes on an outside press", async () => {
      const { user } = renderInput();
      await user.click(screen.getByTestId("send-mode-chip"));
      const queue = screen.getByTestId("send-mode-queue");
      const add = screen.getByTestId("send-mode-add");
      fireEvent.mouseEnter(add);
      expect(add.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
      fireEvent.mouseLeave(add);
      expect(add.style.backgroundColor).toBe("transparent");
      const selected = queue.style.backgroundColor;
      fireEvent.mouseEnter(queue);
      expect(queue.style.backgroundColor).toBe(selected);
      fireEvent.mouseLeave(queue);
      expect(queue.style.backgroundColor).toBe(selected);
      expect(selected).not.toBe("transparent");
      fireEvent.mouseDown(queue.parentElement!.previousElementSibling!);
      expect(screen.queryByTestId("send-mode-queue")).toBeNull();
    });

    it("cycles the send mode on a lone Control tap", async () => {
      const { box, user } = renderInput();
      box.focus();
      await user.keyboard("{Control}");
      expect(screen.getByTestId("send-mode-chip").textContent).toContain("ADD");
      expect(localStorage.getItem("loop-send-mode")).toBe(JSON.stringify("add"));
    });

    it("uses Meta for the tap and the hints on a Mac", async () => {
      vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
      const { box, user } = renderInput();
      expect(box.placeholder).toContain("⌘Enter to steer");
      box.focus();
      await user.keyboard("{Control}");
      expect(screen.getByTestId("send-mode-chip").textContent).toContain("QUEUE");
      await user.keyboard("{Meta}");
      expect(screen.getByTestId("send-mode-chip").textContent).toContain("ADD");
    });
  });

  describe("suggestion", () => {
    it("shows the suggestion in the empty composer and takes it on Tab", async () => {
      const { box, user } = renderInput({ suggestion: "run the tests" });
      expect(box.placeholder).toBe("run the tests  ⇥ Tab");
      await user.keyboard("{Tab}");
      expect(box.value).toBe("run the tests");
      expect(draftsOf()["ch-1"]).toBe("run the tests");
    });

    it("leaves Tab alone once something is typed", async () => {
      const { box, user } = renderInput({ suggestion: "run the tests" });
      await user.type(box, "x");
      fireEvent.keyDown(box, { key: "Tab" });
      expect(box.value).toBe("x");
    });
  });

  describe("command picker", () => {
    const names = () => screen.queryAllByText(/^\/[a-z_-]+$/, { ignore: "textarea" }).map((e) => e.textContent);

    it("lists every /loop command for a /loop prefix and filters by the subcommand", async () => {
      const { box, user } = renderInput();
      await user.type(box, "/lo");
      expect(names()).toContain("/tasks");
      expect(names()).toHaveLength(20);
      await user.type(box, "op work");
      expect(names()).toEqual(["/workflows", "/workflow-run", "/workflow-runs", "/workflow-cancel", "/workflow-retry", "/workflow-delete"]);
      expect(screen.getByText("<name>")).toBeTruthy();
    });

    it.each([
      ["a non-/loop slash", "/x"],
      ["a longer word", "/loopy"],
      ["a subcommand with args", "/loop task 5"],
      ["no matching subcommand", "/loop zz"],
      ["plain text", "hello"],
    ])("hides for %s", async (_, typed) => {
      const { box, user } = renderInput();
      await user.type(box, typed);
      expect(names()).toEqual([]);
    });

    it("moves with the arrows, scrolls the pick into view and takes it on Tab", async () => {
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const { box, user } = renderInput();
      await user.type(box, "/loop ta");
      expect(names()).toEqual(["/tasks", "/task"]);
      await user.keyboard("{ArrowDown}{ArrowDown}{ArrowUp}{ArrowDown}");
      expect(scroll).toHaveBeenCalledWith({ block: "nearest" });
      await user.keyboard("{Tab}");
      expect(box.value).toBe("/loop task ");
      expect(draftsOf()["ch-1"]).toBe("/loop task ");
      await nextFrame();
      expect(box.selectionStart).toBe("/loop task ".length);
    });

    it("takes the first pick on Enter and closes on Escape", async () => {
      const { box, user } = renderInput();
      await user.type(box, "/loop sta{Enter}");
      expect(box.value).toBe("/loop status ");
      expect(messageMock).not.toHaveBeenCalled();
      await user.clear(box);
      await user.type(box, "/loop sta{Escape}");
      expect(names()).toEqual([]);
    });

    it("takes a command clicked with the mouse", async () => {
      const { box, user, unmount } = renderInput();
      await user.type(box, "/loop ");
      const item = screen.getByText("/readme").parentElement!;
      fireEvent.mouseEnter(item);
      fireEvent.mouseDown(item);
      expect(box.value).toBe("/loop readme ");
      unmount();
      await nextFrame();
    });
  });

  describe("shortcut picker", () => {
    const shortcuts: PromptShortcut[] = [
      { name: "review", description: "Review it", prompt: "Review the diff", scope: "global" },
      { name: "refactor", description: "Refactor it", prompt: "Refactor this", scope: "global" },
    ];
    const listed = () => screen.queryAllByText(/^#[a-z]+$/, { ignore: "textarea" }).map((e) => e.textContent);

    async function renderWithShortcuts(overrides: Partial<ChatInputProps> = {}) {
      shortcutsMock.mockResolvedValue(shortcuts);
      const view = renderInput(overrides);
      await screen.findByTitle("Prompt shortcuts");
      return view;
    }

    it("hides the # button and its hint without shortcuts, or when they fail to load", async () => {
      shortcutsMock.mockRejectedValue(new Error("down"));
      const { box } = renderInput();
      await act(async () => {});
      expect(screen.queryByTitle("Prompt shortcuts")).toBeNull();
      expect(box.placeholder).not.toContain("# for shortcuts");
    });

    it("types # from the button and takes it back out when closed unused", async () => {
      const { box, user } = await renderWithShortcuts();
      expect(box.placeholder).toContain("# for shortcuts");
      await user.click(screen.getByTitle("Prompt shortcuts"));
      expect(box.value).toBe("#");
      expect(listed()).toEqual(["#review", "#refactor"]);
      await user.click(screen.getByTitle("Prompt shortcuts"));
      expect(box.value).toBe("");
      expect(draftsOf()["ch-1"]).toBeUndefined();
      expect(listed()).toEqual([]);
    });

    it("opens over a typed draft and keeps it when closed", async () => {
      const { box, user } = await renderWithShortcuts();
      await user.type(box, "hello");
      await user.click(screen.getByTitle("Prompt shortcuts"));
      expect(listed()).toEqual(["#review", "#refactor"]);
      await user.click(screen.getByTitle("Prompt shortcuts"));
      expect(box.value).toBe("hello");
    });

    it("filters by a typed # draft when opened from the button", async () => {
      const { box, user } = await renderWithShortcuts();
      await user.type(box, "#zz");
      expect(listed()).toEqual([]);
      await user.clear(box);
      await user.type(box, "#refa");
      await user.keyboard("{Escape}");
      expect(listed()).toEqual([]);
      expect(box.value).toBe("#refa");
      await user.click(screen.getByTitle("Prompt shortcuts"));
      expect(listed()).toEqual(["#refactor"]);
    });

    it("moves with the arrows and sends the pick on Tab", async () => {
      const { box, props: p, user } = await renderWithShortcuts();
      await user.type(box, "#");
      await user.keyboard("{ArrowDown}{ArrowDown}{ArrowUp}{ArrowDown}{Tab}");
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "#refactor\nRefactor this", "agent", "queue");
      expect(box.value).toBe("");
    });

    it("sends a shortcut clicked with the mouse", async () => {
      const { box, props: p, user } = await renderWithShortcuts();
      await user.type(box, "#");
      const item = screen.getByText("#refactor").parentElement!;
      fireEvent.mouseEnter(item);
      fireEvent.mouseDown(item);
      await waitFor(() => expect(p.onSent).toHaveBeenCalledOnce());
      expect(messageMock).toHaveBeenCalledWith("ch-1", "#refactor\nRefactor this", "agent", "queue");
    });
  });

  describe("@ mentions without roots", () => {
    const mention = () => screen.queryByText("@LoopBot");

    it.each([
      ["Tab", "{Tab}"],
      ["Enter", "{Enter}"],
    ])("completes @LoopBot on %s", async (_, key) => {
      const { box, user } = renderInput();
      await user.type(box, "hey @Lo");
      expect(mention()).toBeTruthy();
      await user.keyboard(key);
      expect(box.value).toBe("hey @LoopBot ");
      expect(messageMock).not.toHaveBeenCalled();
      await nextFrame();
      expect(box.selectionStart).toBe("hey @LoopBot ".length);
    });

    it("completes on a click and closes on Escape", async () => {
      const { box, user, unmount } = renderInput();
      await user.type(box, "line\n@");
      fireEvent.mouseDown(mention()!.parentElement!.parentElement!);
      expect(box.value).toBe("line\n@LoopBot ");
      await nextFrame();
      expect(box.selectionStart).toBe(box.value.length);
      await user.type(box, "@L{Escape}", { initialSelectionStart: box.value.length });
      expect(mention()).toBeNull();
      expect(box.value).toBe("line\n@LoopBot @L");
      await user.type(box, "{Backspace}{Backspace}@L");
      fireEvent.mouseDown(mention()!.parentElement!.parentElement!);
      unmount();
      await nextFrame();
    });

    it.each([
      ["a non-matching name", "@zz"],
      ["an @ inside a word", "a@L"],
      ["a finished mention", "@Lo b"],
    ])("doesn't offer it for %s", async (_, typed) => {
      const { box, user } = renderInput();
      await user.type(box, typed);
      expect(mention()).toBeNull();
    });
  });

  describe("@ file picker", () => {
    const roots: RootEntry[] = [
      { index: 0, name: "main", path: "/w/main/" },
      { index: 1, name: "lib", path: "/w/lib" },
    ];
    const results: FileSearchResult[] = [
      { root_index: 0, rel_path: "src/a.ts", name: "a.ts" },
      { root_index: 1, rel_path: "b.go", name: "b.go" },
      { root_index: 7, rel_path: "c.md", name: "c.md" },
    ];

    it("lists LoopBot and the matching files, labelled by root", async () => {
      vi.mocked(searchFiles).mockResolvedValue(results);
      const { box, user } = renderInput({ roots });
      await user.type(box, "@l");
      await screen.findByText("@LoopBot");
      expect(searchFiles).toHaveBeenLastCalledWith("ch-1", "l", 30);
      expect(screen.getByText("@src/a.ts")).toBeTruthy();
      expect(screen.getByText("@lib/b.go")).toBeTruthy();
      expect(screen.getByText("@c.md")).toBeTruthy();
      expect(screen.getByText("@LoopBot mention")).toBeTruthy();
    });

    it.each([
      [0, "@l", "@LoopBot "],
      [1, "@l", "@/w/main/src/a.ts "],
      [2, "@l", "@/w/lib/b.go "],
      [3, "@l", "@c.md "],
    ])("inserts pick %i as its absolute path", async (idx, typed, want) => {
      vi.mocked(searchFiles).mockResolvedValue(results);
      const { box, user } = renderInput({ roots });
      await user.type(box, typed);
      await screen.findByText("@LoopBot");
      for (let i = 0; i < idx; i++) await user.keyboard("{ArrowDown}");
      await user.keyboard("{Tab}");
      expect(box.value).toBe(want);
      expect(draftsOf()["ch-1"]).toBe(want);
      await nextFrame();
      expect(box.selectionStart).toBe(want.length);
    });

    it("moves with the arrows, takes a pick on Enter, and on a click", async () => {
      vi.mocked(searchFiles).mockResolvedValue(results);
      const { box, user, unmount } = renderInput({ roots });
      await user.type(box, "see @a");
      await screen.findByText("@src/a.ts");
      // A search for the bare "@" may land first; the one for "a" has no LoopBot.
      await waitFor(() => expect(screen.queryByText("@LoopBot")).toBeNull());
      await user.keyboard("{Shift}{ArrowDown}{ArrowDown}{ArrowDown}{ArrowUp}{Enter}");
      expect(box.value).toBe("see @/w/lib/b.go ");
      await nextFrame();
      await user.type(box, "@a", { initialSelectionStart: box.value.length });
      const item = (await screen.findByText("@src/a.ts")).parentElement!;
      fireEvent.mouseEnter(item);
      fireEvent.mouseDown(item);
      expect(box.value).toBe("see @/w/lib/b.go @/w/main/src/a.ts ");
      unmount();
      await nextFrame();
    });

    it("closes on Escape and stays closed without results", async () => {
      vi.mocked(searchFiles).mockResolvedValueOnce(results).mockResolvedValue([]);
      const { box, user } = renderInput({ roots });
      await user.type(box, "@a");
      await screen.findByText("@src/a.ts");
      await user.keyboard("{Escape}");
      expect(screen.queryByText("@src/a.ts")).toBeNull();
      await user.type(box, "{Backspace}x");
      await waitFor(() => expect(searchFiles).toHaveBeenLastCalledWith("ch-1", "x", 30));
      await act(async () => {});
      expect(screen.queryByText("@src/a.ts")).toBeNull();
    });

    it("drops a search answered after a newer one started", async () => {
      const first = deferred<FileSearchResult[]>();
      vi.mocked(searchFiles)
        .mockReturnValueOnce(first.promise)
        .mockResolvedValue([{ root_index: 0, rel_path: "ab.ts", name: "ab.ts" }]);
      const { box, user } = renderInput({ roots });
      await user.type(box, "@a");
      await waitFor(() => expect(searchFiles).toHaveBeenCalledOnce());
      await user.type(box, "b");
      await screen.findByText("@ab.ts");
      await act(async () => first.resolve(results));
      expect(screen.queryByText("@src/a.ts")).toBeNull();
      expect(screen.getByText("@ab.ts")).toBeTruthy();
    });

    it("cancels a pending search on unmount", async () => {
      const clear = vi.spyOn(globalThis, "clearTimeout");
      const { box, user, unmount } = renderInput({ roots });
      await user.type(box, "@a");
      unmount();
      expect(clear).toHaveBeenCalled();
      await new Promise((r) => setTimeout(r, 150));
      expect(searchFiles).not.toHaveBeenCalled();
    });
  });

  describe("pasting", () => {
    const imageClipboard = (file: File) => ({ items: [{ kind: "file", type: file.type, getAsFile: () => file }] });

    it("uploads a pasted image and inserts its path at the caret", async () => {
      vi.mocked(pasteImage).mockResolvedValue(".loop/pastes/shot.png");
      const { box, user } = renderInput();
      await user.type(box, "see  here");
      box.setSelectionRange(4, 4);
      fireEvent.paste(box, { clipboardData: imageClipboard(new File(["png"], "shot.png", { type: "image/png" })) });
      await waitFor(() => expect(box.value).toBe("see .loop/pastes/shot.png here"));
      expect(pasteImage).toHaveBeenCalledWith("ch-1", btoa("png"), "image/png");
      expect(draftsOf()["ch-1"]).toBe("see .loop/pastes/shot.png here");
      await nextFrame();
      expect(box.selectionStart).toBe(4 + ".loop/pastes/shot.png".length);
    });

    it("leaves a text paste to the browser", () => {
      const { box } = renderInput();
      const ev = fireEvent.paste(box, { clipboardData: { items: [{ kind: "string", type: "text/plain" }] } });
      expect(ev).toBe(true);
      expect(pasteImage).not.toHaveBeenCalled();
    });

    it("logs a failed upload and keeps the text", async () => {
      const err = new Error("too big");
      vi.mocked(pasteImage).mockRejectedValue(err);
      const log = vi.spyOn(console, "error").mockImplementation(() => {});
      const { box, user } = renderInput();
      await user.type(box, "keep");
      fireEvent.paste(box, { clipboardData: imageClipboard(new File(["x"], "x.png", { type: "image/png" })) });
      await waitFor(() => expect(log).toHaveBeenCalledWith("[paste-image] upload failed", err));
      expect(box.value).toBe("keep");
    });

    it("doesn't touch a composer unmounted during the upload", async () => {
      const d = deferred<string>();
      vi.mocked(pasteImage).mockReturnValue(d.promise);
      const { box, unmount } = renderInput();
      fireEvent.paste(box, { clipboardData: imageClipboard(new File(["x"], "x.png", { type: "image/png" })) });
      await waitFor(() => expect(pasteImage).toHaveBeenCalled());
      unmount();
      await act(async () => d.resolve("late.png"));
      expect(draftsOf()["ch-1"]).toBeUndefined();
    });

    it("skips the caret fix when unmounted before the next frame", async () => {
      vi.mocked(pasteImage).mockResolvedValue("p.png");
      const { box, unmount } = renderInput();
      fireEvent.paste(box, { clipboardData: imageClipboard(new File(["x"], "x.png", { type: "image/png" })) });
      await waitFor(() => expect(draftsOf()["ch-1"]).toBe("p.png"));
      unmount();
      await nextFrame();
    });
  });

  describe("drafts and edits", () => {
    it("deletes the draft when the composer is cleared", async () => {
      const { box, user } = renderInput();
      await user.type(box, "a");
      expect(draftsOf()["ch-1"]).toBe("a");
      await user.type(box, "{Backspace}");
      expect(draftsOf()["ch-1"]).toBeUndefined();
    });

    it("restores an empty composer after cancelling an edit", async () => {
      const onCancelEdit = vi.fn();
      const p = props({ onCancelEdit });
      const user = userEvent.setup();
      const { rerender } = render(<ChatInput {...p} />);
      rerender(<ChatInput {...p} editingQueued={msg("queued", { msg_id: "q-1" })} />);
      const box = screen.getByRole("textbox") as HTMLTextAreaElement;
      await waitFor(() => expect(box.value).toBe("queued"));
      await user.click(screen.getByTestId("queued-edit-cancel"));
      expect(box.value).toBe("");
      expect(draftsOf()["ch-1"]).toBeUndefined();
      expect(onCancelEdit).toHaveBeenCalledOnce();
    });

    it("skips focusing an edit unmounted before the next frame", async () => {
      const p = props();
      const { rerender, unmount } = render(<ChatInput {...p} />);
      rerender(<ChatInput {...p} editingQueued={msg("queued", { msg_id: "q-1" })} />);
      unmount();
      await nextFrame();
      expect(screen.queryByRole("textbox")).toBeNull();
    });
  });
});
