import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchLearnProposalPreview, type LearnProposal } from "../../api/learn";
import { useChatState } from "../../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import type { LearnView } from "../../hooks/useLearn";
import { layoutLearnView, layoutProposal } from "../../test/layout-fixtures";
import { LearnPane } from "./LearnPane";

vi.mock("../../api/learn", () => ({ fetchLearnProposalPreview: vi.fn() }));
vi.mock("../../hooks/useChatState", () => ({ useChatState: vi.fn() }));
vi.mock("../chat/ChatView", () => ({
  ChatView: ({ channelId, chatState, noAutoFocus, readOnlyNote }: { channelId: string; chatState: { tag: string }; noAutoFocus?: boolean; readOnlyNote?: string }) => (
    <div data-testid="chat-view" data-channel={channelId} data-state={chatState.tag} data-no-auto-focus={String(noAutoFocus)} data-note={readOnlyNote ?? ""}>
      <button type="button">in chat</button>
    </div>
  ),
}));

const previewMock = vi.mocked(fetchLearnProposalPreview);
const chatStateMock = vi.mocked(useChatState);

const proposal = layoutProposal;
const view = layoutLearnView;

type PaneProps = Partial<Parameters<typeof LearnPane>[0]>;
function pane(learn: LearnView, props: PaneProps = {}) {
  return <LearnPane learn={learn} worktree={false} onClose={vi.fn()} {...props} />;
}

const cards = () => screen.getAllByTestId("learn-proposal");

beforeEach(() => {
  vi.clearAllMocks();
  chatStateMock.mockReturnValue({ tag: "thread-state" } as never);
  previewMock.mockResolvedValue({});
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("LearnPane", () => {
  it("says there's no learn pass yet, and what the last runs proposed", () => {
    render(pane(view()));
    expect(screen.getByText("proposals from the last runs")).toBeTruthy();
    expect(screen.getByText(/No learn pass yet/)).toBeTruthy();
    expect(screen.queryByTestId("learn-apply-all")).toBeNull();
    expect(screen.queryByTestId("learn-dismiss-all")).toBeNull();
    expect(screen.queryByTestId("learn-proposal")).toBeNull();
    expect(screen.queryByTestId("chat-view")).toBeNull();
  });

  it("closes from its button, which lights up under the mouse", () => {
    const onClose = vi.fn();
    render(pane(view(), { onClose }));
    const btn = screen.getByRole("button", { name: "Close the Learn view" });
    fireEvent.mouseEnter(btn);
    expect(btn.style.backgroundColor).toBe("rgb(54, 54, 54)");
    expect(btn.style.color).toBe("rgb(236, 236, 236)");
    fireEvent.mouseLeave(btn);
    expect(btn.style.backgroundColor).toBe("transparent");
    fireEvent.click(btn);
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("shows the learn thread's chat while a pass runs, wired to the store", () => {
    const unsubscribe = vi.fn();
    const subscribeChannelEvents = vi.fn().mockReturnValue(unsubscribe);
    const saved = { messages: [] } as unknown as ActiveChatState;
    const getChatState = vi.fn().mockReturnValue(saved);
    const onChatStateUnmount = vi.fn();
    render(pane(view({ learnChannelId: "lc-1", running: true }), { uiChannelId: "ws-1", subscribeChannelEvents, getChatState, onChatStateUnmount }));
    expect(screen.getByText("reviewing the last run…")).toBeTruthy();
    const chat = screen.getByTestId("chat-view");
    expect(chat.dataset.channel).toBe("lc-1");
    expect(chat.dataset.state).toBe("thread-state");
    expect(chat.dataset.noAutoFocus).toBe("true");
    expect(chat.dataset.note).toBe("");

    const [threadId, running, options] = chatStateMock.mock.calls.at(-1)!;
    expect(threadId).toBe("lc-1");
    expect(running).toBe(true);
    expect(getChatState).toHaveBeenCalledWith("lc-1");
    expect(options!.initialState).toBe(saved);
    const listener: ChatEventListener = vi.fn();
    expect(options!.subscribeChatEvents!(listener)).toBe(unsubscribe);
    expect(subscribeChannelEvents).toHaveBeenCalledWith("lc-1", listener);
    const state = { messages: [1] } as unknown as ActiveChatState;
    options!.onUnmount!(state);
    expect(onChatStateUnmount).toHaveBeenCalledWith("lc-1", state);
  });

  it("gives the thread's chat a no-op subscription and unmount without the store", () => {
    render(pane(view({ learnChannelId: "lc-1" })));
    const options = chatStateMock.mock.calls.at(-1)![2]!;
    expect(options.initialState).toBeUndefined();
    const unsubscribe = options.subscribeChatEvents!(vi.fn());
    expect(unsubscribe()).toBeUndefined();
    expect(options.onUnmount!({} as ActiveChatState)).toBeUndefined();
  });

  it("applies or dismisses all from the header, unless one's going", () => {
    const learn = view({ proposals: [proposal({ id: 1 }), proposal({ id: 2, status: "failed" })] });
    const { rerender } = render(pane(learn));
    fireEvent.click(screen.getByTestId("learn-apply-all"));
    expect(learn.applyAll).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByTestId("learn-dismiss-all"));
    expect(learn.dismissAll).toHaveBeenCalledOnce();
    rerender(pane({ ...learn, bulk: "apply" as LearnView["bulk"] }));
    expect((screen.getByTestId("learn-apply-all") as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId("learn-dismiss-all") as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers only Dismiss all with nothing pending", () => {
    render(pane(view({ proposals: [proposal({ status: "failed" })] })));
    expect(screen.queryByTestId("learn-apply-all")).toBeNull();
    expect(screen.getByTestId("learn-dismiss-all")).toBeTruthy();
  });

  it("shows an open proposal's kind, detail and rationale, and applies or dismisses it", () => {
    const learn = view({ proposals: [proposal({ id: 7, rationale: "it helps" })] });
    render(pane(learn));
    const card = cards()[0]!;
    expect(card.dataset.status).toBe("pending");
    expect(within(card).getByText("description")).toBeTruthy();
    expect(within(card).getByText("Describe it")).toBeTruthy();
    expect(within(card).getByText("it helps")).toBeTruthy();
    expect(card.style.opacity).toBe("1");
    fireEvent.click(within(card).getByRole("button", { name: "Apply “Describe it”" }));
    expect(learn.apply).toHaveBeenCalledWith(7);
    fireEvent.click(within(card).getByRole("button", { name: "Dismiss “Describe it”" }));
    expect(learn.dismiss).toHaveBeenCalledWith(7);
    expect(previewMock).not.toHaveBeenCalled();
  });

  it("offers a failed proposal's retry with its error, and disables a busy one's buttons", () => {
    render(pane(view({ proposals: [proposal({ id: 3, status: "failed", error: "it broke" })], busy: new Set([3]), errors: new Map([[3, "no answer"]]) })));
    const card = cards()[0]!;
    const retry = within(card).getByRole("button", { name: "Retry “Describe it”" }) as HTMLButtonElement;
    expect(retry.textContent).toBe("Retry");
    expect(retry.disabled).toBe(true);
    expect((within(card).getByTestId("learn-dismiss") as HTMLButtonElement).disabled).toBe(true);
    expect(within(card).getByText("it broke")).toBeTruthy();
    expect(within(card).getByTestId("learn-request-error").textContent).toBe("no answer");
  });

  it("shows a settled proposal's status dimmed, with a withdrawn one's reason", () => {
    render(
      pane(
        view({
          proposals: [proposal({ id: 1, status: "withdrawn", withdrawn_reason: "already done" }), proposal({ id: 2, status: "withdrawn" }), proposal({ id: 3, status: "applying" })],
        }),
      ),
    );
    const [withdrawn, bare, applying] = cards();
    expect(within(withdrawn!).getByText("withdrawn")).toBeTruthy();
    expect(withdrawn!.style.opacity).toBe("0.55");
    expect(within(withdrawn!).getByTestId("learn-withdrawn-reason").textContent).toBe("Withdrawn by a later learn pass: already done");
    expect(within(bare!).queryByTestId("learn-withdrawn-reason")).toBeNull();
    expect(within(applying!).getByText("applying")).toBeTruthy();
    expect(applying!.style.opacity).toBe("1");
    expect(screen.queryByTestId("learn-apply")).toBeNull();
  });

  it("leaves out a failed proposal's error line when it has none", () => {
    render(pane(view({ proposals: [proposal({ status: "failed" })] })));
    expect(cards()[0]!.textContent).not.toContain("undefined");
    expect(screen.queryByTestId("learn-request-error")).toBeNull();
  });

  it("warns that a worktree's rename leaves its branch", () => {
    const learn = view({ proposals: [proposal({ kind: "rename", payload: '{"name":"new"}' })] });
    const { rerender } = render(pane(learn));
    expect(screen.queryByText(/Renames the thread only/)).toBeNull();
    rerender(pane(learn, { worktree: true }));
    expect(screen.getByText("Renames the thread only; its branch and worktree folder keep their names.")).toBeTruthy();
  });

  it("shows why the turn's Learn request failed", () => {
    const { rerender } = render(pane(view({ focus: { messageId: "m-1", seq: 1 }, turnErrors: new Map([["m-1", "can't learn"]]) })));
    expect(screen.getByTestId("learn-turn-error").textContent).toBe("can't learn");
    rerender(pane(view({ focus: { messageId: "m-2", seq: 1 }, turnErrors: new Map([["m-1", "can't learn"]]) })));
    expect(screen.queryByTestId("learn-turn-error")).toBeNull();
  });

  describe("config preview", () => {
    const shortcut = (over: Partial<LearnProposal> = {}) => proposal({ id: 5, kind: "prompt_shortcut", payload: '{"name":"fix","prompt":"fix it"}', ...over });

    it("shows the diff applying would make", async () => {
      previewMock.mockResolvedValue({ path: ".loop/config.json", diff: "@@ -1 +1 @@\n-a\n+b" });
      render(pane(view({ proposals: [shortcut()] })));
      const diff = await screen.findByTestId("learn-preview-diff");
      expect(diff.textContent).toContain("+b");
      expect(previewMock).toHaveBeenCalledWith(5);
    });

    it("says when applying would change nothing", async () => {
      previewMock.mockResolvedValue({ path: ".loop/config.json", diff: "" });
      render(pane(view({ proposals: [shortcut()] })));
      expect((await screen.findByTestId("learn-preview-unchanged")).textContent).toBe("Already in .loop/config.json; applying changes nothing.");
    });

    it("says why applying would fail", async () => {
      previewMock.mockResolvedValue({ error: "bad config" });
      render(pane(view({ proposals: [shortcut()] })));
      expect((await screen.findByTestId("learn-preview-error")).textContent).toBe("Applying would fail: bad config");
    });

    it("shows nothing for a preview without a path", async () => {
      previewMock.mockResolvedValue({});
      render(pane(view({ proposals: [shortcut()] })));
      await waitFor(() => expect(previewMock).toHaveBeenCalled());
      await act(async () => {});
      expect(screen.queryByTestId("learn-preview-diff")).toBeNull();
      expect(screen.queryByTestId("learn-preview-unchanged")).toBeNull();
      expect(screen.queryByTestId("learn-preview-error")).toBeNull();
    });

    it("drops the preview and logs when it can't be fetched", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      previewMock.mockResolvedValueOnce({ path: "p", diff: "+x" });
      const learn = view({ proposals: [shortcut()] });
      const { rerender } = render(pane(learn));
      await screen.findByTestId("learn-preview-diff");
      previewMock.mockRejectedValueOnce(new Error("down"));
      rerender(pane(view({ proposals: [shortcut({ status: "failed" })] })));
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] previewing learn proposal:", expect.any(Error)));
      expect(screen.queryByTestId("learn-preview-diff")).toBeNull();
    });

    it("fetches again once another file edit is applied", async () => {
      render(pane(view({ proposals: [shortcut()] })));
      await waitFor(() => expect(previewMock).toHaveBeenCalledTimes(1));
      const { rerender } = render(pane(view({ proposals: [shortcut(), proposal({ id: 6, kind: "memory", status: "applied" })] })));
      rerender(pane(view({ proposals: [shortcut(), proposal({ id: 6, kind: "memory", status: "applied" }), proposal({ id: 8, kind: "mount", status: "applied" })] })));
      await waitFor(() => expect(previewMock.mock.calls.filter(([id]) => id === 5).length).toBeGreaterThanOrEqual(3));
    });

    it("ignores a preview, or its failure, that lands after the card closed", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      let resolve!: (v: object) => void;
      let reject!: (e: Error) => void;
      previewMock.mockReturnValueOnce(new Promise((r) => (resolve = r))).mockReturnValueOnce(new Promise((_, r) => (reject = r)));
      const first = render(pane(view({ proposals: [shortcut()] })));
      first.unmount();
      const second = render(pane(view({ proposals: [shortcut()] })));
      second.unmount();
      await act(async () => {
        resolve({ path: "p", diff: "+x" });
        reject(new Error("late"));
      });
      expect(warn).toHaveBeenCalledWith("[loop] previewing learn proposal:", expect.any(Error));
    });
  });

  describe("asked-for turn", () => {
    it("brings its first card into view and outlines its cards for a while", () => {
      vi.useFakeTimers();
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const proposals = [proposal({ id: 1, message_id: "m-0" }), proposal({ id: 2, message_id: "m-1" }), proposal({ id: 3, message_id: "m-1" })];
      render(pane(view({ proposals, focus: { messageId: "m-1", seq: 1 } })));
      const [other, first, second] = cards();
      expect(scroll).toHaveBeenCalledOnce();
      expect(scroll.mock.contexts[0]).toBe(first);
      expect(scroll).toHaveBeenCalledWith({ behavior: "smooth", block: "start" });
      expect(first!.dataset.highlighted).toBe("true");
      expect(second!.dataset.highlighted).toBe("true");
      expect(other!.dataset.highlighted).toBeUndefined();
      act(() => vi.advanceTimersByTime(4000));
      expect(first!.dataset.highlighted).toBeUndefined();
    });

    it("waits for its proposals to come in", () => {
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const learn = view({ focus: { messageId: "m-1", seq: 1 }, proposals: [proposal({ id: 1 })] });
      const { rerender } = render(pane(learn));
      expect(scroll).not.toHaveBeenCalled();
      expect(cards()[0]!.dataset.highlighted).toBeUndefined();
      rerender(pane({ ...learn, proposals: [proposal({ id: 1 }), proposal({ id: 2, message_id: "m-1" })] }));
      expect(scroll).toHaveBeenCalledOnce();
    });
  });

  describe("focus", () => {
    const applyOf = (id: number) => within(screen.getByTestId("learn-pane").querySelector<HTMLElement>(`[data-proposal-id="${id}"]`)!).getByTestId("learn-apply");
    const three = () => [proposal({ id: 1 }), proposal({ id: 2 }), proposal({ id: 3 })];
    const settle = (ps: LearnProposal[], id: number) => ps.map((p) => (p.id === id ? { ...p, status: "applied" as const } : p));

    it("moves to the next open card's Apply once a card settles", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps })));
      applyOf(2).focus();
      rerender(pane(view({ proposals: ps })));
      expect(document.activeElement).toBe(applyOf(2));
      rerender(pane(view({ proposals: settle(ps, 2) })));
      expect(document.activeElement).toBe(applyOf(3));
    });

    it("moves to the nearest card before it when it was the last", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps })));
      within(screen.getByTestId("learn-pane").querySelector<HTMLElement>('[data-proposal-id="3"]')!).getByTestId("learn-dismiss").focus();
      rerender(pane(view({ proposals: settle(ps, 3) })));
      expect(document.activeElement).toBe(applyOf(2));
    });

    it("moves from an emptied header button to the first open Apply, else the close button", () => {
      const ps = [proposal({ id: 1 }), proposal({ id: 2, status: "failed" })];
      const { rerender } = render(pane(view({ proposals: ps })));
      screen.getByTestId("learn-apply-all").focus();
      rerender(pane(view({ proposals: settle(ps, 1) })));
      expect(document.activeElement).toBe(applyOf(2));

      screen.getByTestId("learn-dismiss-all").focus();
      rerender(pane(view({ proposals: settle(settle(ps, 1), 2) })));
      expect(document.activeElement).toBe(screen.getByTestId("learn-close"));
    });

    it("comes back to a button that was only disabled meanwhile", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps })));
      const apply = applyOf(1);
      apply.focus();
      // A button disabled in a browser drops focus to the page.
      apply.blur();
      rerender(pane(view({ proposals: ps, busy: new Set([1]) })));
      expect(document.activeElement).toBe(document.body);
      rerender(pane(view({ proposals: ps })));
      expect(document.activeElement).toBe(apply);
    });

    it("takes focus back when nothing has it", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps })));
      const apply = applyOf(1);
      apply.focus();
      apply.blur();
      const focus = vi.spyOn(apply, "focus");
      vi.spyOn(document, "activeElement", "get").mockReturnValue(null);
      rerender(pane(view({ proposals: ps })));
      expect(focus).toHaveBeenCalledOnce();
    });

    it("leaves focus that moved elsewhere on its own", () => {
      const ps = three();
      const outside = document.createElement("button");
      document.body.append(outside);
      const { rerender } = render(pane(view({ proposals: ps })));
      applyOf(2).focus();
      outside.focus();
      rerender(pane(view({ proposals: settle(ps, 2) })));
      expect(document.activeElement).toBe(outside);
      outside.remove();
    });

    it("leaves focus that moved elsewhere unseen, its focusin stopped on the way", () => {
      const ps = three();
      const outside = document.createElement("button");
      outside.addEventListener("focusin", (e) => e.stopPropagation());
      document.body.append(outside);
      const { rerender } = render(pane(view({ proposals: ps })));
      applyOf(2).focus();
      outside.focus();
      rerender(pane(view({ proposals: settle(ps, 2) })));
      expect(document.activeElement).toBe(outside);
      outside.remove();
    });

    it("forgets the button after a click elsewhere, not on it", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps })));
      const apply = applyOf(2);
      apply.focus();
      fireEvent.pointerDown(apply);
      rerender(pane(view({ proposals: settle(ps, 2) })));
      expect(document.activeElement).toBe(applyOf(3));

      applyOf(3).focus();
      fireEvent.pointerDown(document.body);
      rerender(pane(view({ proposals: settle(settle(ps, 2), 3) })));
      expect(document.activeElement).not.toBe(applyOf(1));
    });

    it("forgets the button after a click with nothing focused", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps })));
      applyOf(2).focus();
      applyOf(2).blur();
      fireEvent.pointerDown(screen.getByTestId("learn-pane"));
      rerender(pane(view({ proposals: settle(ps, 2) })));
      expect(document.activeElement).toBe(document.body);
    });

    it("forgets the button after a click on no node", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps })));
      applyOf(2).focus();
      document.dispatchEvent(new PointerEvent("pointerdown"));
      applyOf(2).blur();
      rerender(pane(view({ proposals: settle(ps, 2) })));
      expect(document.activeElement).toBe(document.body);
    });

    it("doesn't follow focus in the learn thread's chat, or outside the pane", () => {
      const ps = three();
      const { rerender } = render(pane(view({ proposals: ps, learnChannelId: "lc-1" })));
      applyOf(2).focus();
      const inChat = screen.getByRole("button", { name: "in chat" });
      inChat.focus();
      inChat.blur();
      rerender(pane(view({ proposals: settle(ps, 2), learnChannelId: "lc-1" })));
      expect(document.activeElement).toBe(document.body);

      applyOf(1).focus();
      document.dispatchEvent(new FocusEvent("focusin"));
      applyOf(1).blur();
      rerender(pane(view({ proposals: settle(settle(ps, 2), 1), learnChannelId: "lc-1" })));
      expect(document.activeElement).toBe(document.body);
    });
  });
});
