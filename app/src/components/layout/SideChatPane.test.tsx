import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ContainerStatsEntry } from "../../api/channels";
import { useChatState } from "../../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import { useContainerStats } from "../../hooks/useContainerStats";
import { useExplain } from "../../hooks/useExplain";
import { useLearn } from "../../hooks/useLearn";
import { usePromptEdits } from "../../hooks/usePromptEdits";
import { sideChats } from "../../layouts/sideChats";
import { layoutExplainView, layoutExplanation, layoutLearnView } from "../../test/layout-fixtures";
import { SideChatPane, type SideChatView, sideChatViewSlotId } from "./SideChatPane";

vi.mock("../../layouts/sideChats", () => ({ sideChats: { open: vi.fn() } }));
vi.mock("../../hooks/useChatState", () => ({ useChatState: vi.fn() }));
vi.mock("../../hooks/useContainerStats", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/useContainerStats")>()),
  useContainerStats: vi.fn(),
}));
vi.mock("../../hooks/useExplain", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../hooks/useExplain")>()), useExplain: vi.fn() }));
vi.mock("../../hooks/useLearn", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../hooks/useLearn")>()), useLearn: vi.fn() }));
vi.mock("../../hooks/usePromptEdits", () => ({ usePromptEdits: vi.fn() }));
vi.mock("../panels/Terminal", () => ({ Terminal: ({ channelId }: { channelId: string }) => <div data-testid="terminal" data-channel={channelId} /> }));
vi.mock("../chat/ChatView", () => ({
  ChatView: (p: { channelId: string; chatState: { tag: string }; noAutoFocus?: boolean; wsOpens?: number; subscribeChatEvents?: unknown; onSelectThread?: (id: string) => void }) => (
    <div data-testid="chat-view" data-channel={p.channelId} data-state={p.chatState.tag} data-ws-opens={String(p.wsOpens)} data-subscribes={String(!!p.subscribeChatEvents)}>
      {p.channelId.startsWith("side") && <textarea aria-label="composer" />}
      <button type="button" onClick={() => p.onSelectThread?.("th-9")}>
        go to thread
      </button>
    </div>
  ),
}));

const openMock = vi.mocked(sideChats.open);
const chatStateMock = vi.mocked(useChatState);
const statsMock = vi.mocked(useContainerStats);
const explainMock = vi.mocked(useExplain);
const learnMock = vi.mocked(useLearn);
const editsMock = vi.mocked(usePromptEdits);

const agent: ContainerStatsEntry = { container_id: "abcdef1234567890", type: "agent", cpu_percent: 12.5, mem_usage: 1024, mem_limit: 2048 };

type Props = Partial<Parameters<typeof SideChatPane>[0]>;
function pane(props: Props = {}) {
  return <SideChatPane channelId="ch-1" leafId="leaf-1" mode="fork" {...props} />;
}

/** The header slots the side chat's badges and stats go in: the pane's and its view's. */
function slots() {
  const make = (id: string) => {
    const el = document.createElement("div");
    el.id = `pane-header-slot-${id}`;
    document.body.append(el);
    return el;
  };
  return { header: make("leaf-1"), view: make(sideChatViewSlotId("leaf-1")) };
}

/** Renders a pane whose side chat is side-1, and the latest view it reported. */
async function ready(props: Props = {}) {
  const onSideView = vi.fn<(leafId: string, view: SideChatView | null) => void>();
  const r = render(pane({ onSideView, ...props }));
  await screen.findByTestId("side-chat");
  const last = () => onSideView.mock.calls.at(-1)![1];
  return { ...r, onSideView, last };
}

beforeEach(() => {
  vi.clearAllMocks();
  openMock.mockResolvedValue("side-1");
  chatStateMock.mockReturnValue({ tag: "side-state" } as never);
  statsMock.mockReturnValue({});
  explainMock.mockReturnValue(layoutExplainView());
  learnMock.mockReturnValue(layoutLearnView());
  editsMock.mockReturnValue({ editChannelId: "", running: false });
});

afterEach(() => {
  for (const el of document.querySelectorAll('[id^="pane-header-slot-"]')) el.remove();
  vi.restoreAllMocks();
});

describe("sideChatViewSlotId", () => {
  it("names the view's header slot after the pane", () => {
    expect(sideChatViewSlotId("leaf-1")).toBe("side-chat-view-leaf-1");
  });
});

describe("SideChatPane", () => {
  it("starts the pane's side chat, then shows its chat", async () => {
    render(pane({ wsOpens: 3 }));
    expect(screen.getByTestId("side-chat-starting").textContent).toBe("Starting the side chat…");
    expect(openMock).toHaveBeenCalledWith("ch-1", "leaf-1", "fork");
    const chat = await screen.findByTestId("chat-view");
    expect(chat.dataset.channel).toBe("side-1");
    expect(chat.dataset.state).toBe("side-state");
    expect(chat.dataset.wsOpens).toBe("3");
    expect(chat.dataset.subscribes).toBe("false");
    expect(screen.getByTestId("side-chat").contains(chat)).toBe(true);
  });

  it.each([
    { name: "an Error's message", err: new Error("no daemon"), msg: "no daemon" },
    { name: "anything else as text", err: "refused", msg: "refused" },
  ])("says why the side chat couldn't start: $name", async ({ err, msg }) => {
    openMock.mockRejectedValue(err);
    render(pane());
    const box = await screen.findByTestId("side-chat-error");
    expect(within(box).getByText("Couldn't start the side chat")).toBeTruthy();
    expect(within(box).getByText(msg)).toBeTruthy();
  });

  it("starts again for a new mode, ignoring the old start's outcome", async () => {
    let first!: (id: string) => void;
    let second!: (id: string) => void;
    openMock.mockReturnValueOnce(new Promise((r) => (first = r))).mockReturnValueOnce(new Promise((r) => (second = r)));
    const { rerender } = render(pane());
    rerender(pane({ mode: "fresh" }));
    expect(openMock).toHaveBeenLastCalledWith("ch-1", "leaf-1", "fresh");
    await act(async () => first("side-old"));
    expect(screen.getByTestId("side-chat-starting")).toBeTruthy();
    await act(async () => second("side-new"));
    expect(screen.getByTestId("chat-view").dataset.channel).toBe("side-new");
  });

  it("ignores a failed start once the pane's gone", async () => {
    let fail!: (e: Error) => void;
    openMock.mockReturnValueOnce(new Promise((_, r) => (fail = r)));
    const { unmount } = render(pane());
    unmount();
    await act(async () => fail(new Error("late")));
    expect(screen.queryByTestId("side-chat-error")).toBeNull();
  });

  it("runs the side chat on its own subscription and restores its saved state", async () => {
    const unsubscribe = vi.fn();
    const subscribeChannelEvents = vi.fn().mockReturnValue(unsubscribe);
    const saved = { isRunning: true } as unknown as ActiveChatState;
    const getChatState = vi.fn().mockReturnValue(saved);
    const onChatStateUnmount = vi.fn();
    const onSelectThread = vi.fn();
    await ready({ subscribeChannelEvents, getChatState, onChatStateUnmount, onSelectThread, wsOpens: 2 });

    const [id, running, options] = chatStateMock.mock.calls.at(-1)!;
    expect(id).toBe("side-1");
    expect(running).toBe(true);
    expect(options!.initialState).toBe(saved);
    const listener: ChatEventListener = vi.fn();
    expect(options!.subscribeChatEvents!(listener)).toBe(unsubscribe);
    expect(subscribeChannelEvents).toHaveBeenCalledWith("side-1", listener);
    const state = {} as ActiveChatState;
    options!.onUnmount!(state);
    expect(onChatStateUnmount).toHaveBeenCalledWith("side-1", state);

    const subscribe = options!.subscribeChatEvents;
    expect(explainMock).toHaveBeenLastCalledWith("side-1", subscribe, 2, expect.any(Function));
    expect(learnMock).toHaveBeenLastCalledWith("side-1", subscribe, subscribeChannelEvents, 2, expect.any(Function));
    expect(editsMock).toHaveBeenLastCalledWith("side-1", subscribeChannelEvents, 2);
    expect(statsMock).toHaveBeenLastCalledWith("side-1");
    expect(screen.getByTestId("chat-view").dataset.subscribes).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "go to thread" }));
    expect(onSelectThread).toHaveBeenCalledWith("th-9");
  });

  it("gives the side chat a no-op subscription and starts it idle without the store", async () => {
    await ready();
    const [, running, options] = chatStateMock.mock.calls.at(-1)!;
    expect(running).toBe(false);
    expect(options!.initialState).toBeUndefined();
    expect(options!.subscribeChatEvents!(vi.fn())()).toBeUndefined();
    expect(options!.onUnmount!({} as ActiveChatState)).toBeUndefined();
    expect(explainMock).toHaveBeenLastCalledWith("side-1", undefined, undefined, expect.any(Function));
    expect(learnMock).toHaveBeenLastCalledWith("side-1", undefined, undefined, undefined, expect.any(Function));
  });

  it("reports its closed Explain view, then null once it goes, removing its hosts", async () => {
    const { last, onSideView, unmount } = await ready();
    const v = last()!;
    expect(v).toMatchObject({ open: false, view: "explain", running: false, openMode: "fork" });
    expect(v.chatHost.querySelector('[data-testid="chat-view"]')).toBeTruthy();
    const park = document.createElement("div");
    park.dataset.chatPark = "";
    document.body.append(park);
    park.append(v.paneHost);
    unmount();
    expect(onSideView).toHaveBeenLastCalledWith("leaf-1", null);
    expect(v.chatHost.isConnected).toBe(false);
    expect(park.isConnected).toBe(false);
  });

  it("opens each view from its badge in the pane's header", async () => {
    const { header } = slots();
    explainMock.mockReturnValue(layoutExplainView({ explanations: [layoutExplanation()] }));
    learnMock.mockReturnValue(layoutLearnView({ learnChannelId: "lc-1" }));
    editsMock.mockReturnValue({ editChannelId: "ed-1", running: true });
    statsMock.mockReturnValue({ agent });
    const { last } = await ready();
    expect(within(header).getByTestId("container-stats")).toBeTruthy();

    fireEvent.click(within(header).getByTestId("learn-badge"));
    expect(last()).toMatchObject({ open: true, view: "learn", running: false });
    fireEvent.click(within(header).getByTestId("edits-badge"));
    expect(last()).toMatchObject({ open: true, view: "edits", running: true });
    fireEvent.click(within(header).getByTestId("shell-badge"));
    expect(last()).toMatchObject({ open: true, view: "shell", running: false });
    fireEvent.click(within(header).getByTestId("explain-badge"));
    expect(last()).toMatchObject({ open: true, view: "explain", running: false });
    // Nowhere to show the view yet: no pane is mounted for it.
    expect(last()!.paneHost.childElementCount).toBe(0);
  });

  it("counts a pending explanation, or a running learn pass, as the view running", async () => {
    explainMock.mockReturnValue(layoutExplainView({ explanations: [layoutExplanation({ status: "running" })] }));
    learnMock.mockReturnValue(layoutLearnView({ learnChannelId: "lc-1", running: true }));
    const { header } = slots();
    const { last } = await ready();
    expect(last()).toMatchObject({ view: "explain", running: true });
    fireEvent.click(within(header).getByTestId("learn-badge"));
    expect(last()).toMatchObject({ view: "learn", running: true });
  });

  it("shows each view's pane in its host while docked, and its badge closes it", async () => {
    const { header } = slots();
    explainMock.mockReturnValue(layoutExplainView({ explanations: [layoutExplanation()] }));
    learnMock.mockReturnValue(layoutLearnView({ learnChannelId: "lc-1" }));
    editsMock.mockReturnValue({ editChannelId: "ed-1", running: false });
    statsMock.mockReturnValue({ agent });
    const { last } = await ready({ sideViewPlace: "dock" });
    const paneHost = last()!.paneHost;
    expect(within(paneHost).getByRole("button", { name: "Close the Explain view" })).toBeTruthy();
    // The chat stays in the pane while docked.
    expect(screen.getByTestId("side-chat").contains(last()!.chatHost)).toBe(true);

    fireEvent.click(within(header).getByTestId("explain-badge"));
    expect(last()).toMatchObject({ open: true, view: "explain" });
    fireEvent.click(within(header).getByTestId("explain-badge"));
    expect(last()).toMatchObject({ open: false, view: "explain" });

    fireEvent.click(within(header).getByTestId("learn-badge"));
    expect(within(paneHost).getByTestId("learn-pane")).toBeTruthy();
    fireEvent.click(within(header).getByTestId("learn-badge"));
    expect(last()!.open).toBe(false);

    fireEvent.click(within(header).getByTestId("edits-badge"));
    expect(within(paneHost).getByRole("button", { name: "Close the Edits view" })).toBeTruthy();
    fireEvent.click(within(header).getByTestId("edits-badge"));
    expect(last()!.open).toBe(false);

    fireEvent.click(within(header).getByTestId("shell-badge"));
    expect(within(paneHost).getByTestId("terminal").dataset.channel).toBe("side-1");
    fireEvent.click(within(header).getByTestId("shell-badge"));
    expect(last()!.open).toBe(false);
  });

  it("hands the chat to the view over the layout, with the badges and stats in the view's header", async () => {
    const { header, view } = slots();
    explainMock.mockReturnValue(layoutExplainView({ explanations: [layoutExplanation()] }));
    statsMock.mockReturnValue({ agent });
    const { last } = await ready({ sideViewPlace: "overlay" }).catch(async () => {
      await waitFor(() => expect(screen.getByTestId("side-chat")).toBeTruthy());
      return { last: () => undefined as SideChatView | undefined };
    });
    expect(screen.queryByTestId("chat-view")).toBeNull();
    expect(within(view).getByTestId("container-stats")).toBeTruthy();
    expect(within(header).getByTestId("container-stats")).toBeTruthy();
    const viewBadge = within(view).getByTestId("explain-badge");
    expect(viewBadge.getAttribute("aria-expanded")).toBe("true");
    expect(within(header).getByTestId("explain-badge").getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(viewBadge);
    expect(last()).toMatchObject({ open: true, view: "explain" });
    expect(within(header).getByTestId("explain-badge").getAttribute("aria-expanded")).toBe("true");
  });

  it("puts focus back in the side chat's composer when the view closes with focus in its pane", async () => {
    const { last } = await ready({ sideViewPlace: "dock" });
    const { chatHost, paneHost } = last()!;
    document.body.append(paneHost);
    const composer = within(chatHost).getByRole("textbox", { name: "composer" });
    within(paneHost).getByRole("button", { name: "Close the Explain view" }).focus();
    act(() => last()!.close());
    expect(document.activeElement).toBe(composer);
    paneHost.remove();
  });

  it("leaves focus alone when the view closes with focus elsewhere", async () => {
    const outside = document.createElement("button");
    document.body.append(outside);
    const { last } = await ready({ sideViewPlace: "dock" });
    outside.focus();
    act(() => last()!.close());
    expect(document.activeElement).toBe(outside);
    outside.remove();
  });

  it("closes without a composer to focus while the view holds no chat", async () => {
    openMock.mockResolvedValue("other-1");
    const { last } = await ready({ sideViewPlace: "overlay" });
    const { paneHost } = last()!;
    document.body.append(paneHost);
    const closeBtn = within(paneHost).getByRole("button", { name: "Close the Explain view" });
    closeBtn.focus();
    act(() => last()!.close());
    expect(last()!.open).toBe(false);
    expect(document.activeElement).toBe(closeBtn);
    paneHost.remove();
  });
});
