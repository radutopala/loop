import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchPluginAgent } from "../api/plugins";
import { useChatState } from "../hooks/useChatState";
import type { ChatEventListener } from "../hooks/useChatStateStore";
import type { WSEvent } from "../types";
import { agentOpenKey, PLUGIN_AGENT_OPENED, PluginAgentBadge, PluginAgentThread, startedRunThread, usePluginAgent, usePluginAgentOpen } from "./PluginAgent";

vi.mock("../api/plugins", () => ({ fetchPluginAgent: vi.fn() }));
vi.mock("../hooks/useChatState", () => ({ useChatState: vi.fn(() => ({ fake: "chat state" })) }));
vi.mock("../components/chat/ChatView", () => ({
  ChatView: (props: { channelId: string; readOnlyNote: string; noAutoFocus: boolean; chatState: unknown; roots?: unknown[] }) => (
    <div data-testid="chat-view" data-channel={props.channelId} data-autofocus={String(!props.noAutoFocus)} data-roots={JSON.stringify(props.roots ?? null)}>
      {props.readOnlyNote}
      {JSON.stringify(props.chatState)}
    </div>
  ),
}));

// The animated icon measures its SVG path, which jsdom doesn't lay out.
vi.mock("../components/LoopInfinityIcon", () => ({
  LoopInfinityIcon: ({ animated, color }: { animated?: boolean; color?: string }) => <span data-testid="agent-icon" data-animated={String(!!animated)} data-color={color ?? ""} />,
}));

const fetchMock = vi.mocked(fetchPluginAgent);
const chatStateMock = vi.mocked(useChatState);

function event(type: string, data: unknown): WSEvent {
  return { type, data } as WSEvent;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
  localStorage.clear();
});

describe("startedRunThread", () => {
  it.each([
    { name: "the plugin's run", ev: event("plugin.run_started", { plugin_id: "cogtest", run_id: "r1", thread_id: "plugin:cogtest-abc123" }), want: "plugin:cogtest-abc123" },
    { name: "another plugin's run", ev: event("plugin.run_started", { plugin_id: "echo", thread_id: "plugin:echo-abc123" }), want: null },
    { name: "a run without a thread", ev: event("plugin.run_started", { plugin_id: "cogtest" }), want: null },
    { name: "an event without data", ev: event("plugin.run_started", undefined), want: null },
    { name: "another event", ev: event("agent.status", { plugin_id: "cogtest", thread_id: "plugin:cogtest-abc123" }), want: null },
  ])("answers $name", ({ ev, want }) => {
    expect(startedRunThread(ev, "cogtest")).toBe(want);
  });
});

describe("agentOpenKey", () => {
  it("keys a pane's agent view by plugin, channel and leaf", () => {
    expect(agentOpenKey("cogtest", "ch1", "leaf-2")).toBe("plugin-agent-open:cogtest:ch1:leaf-2");
  });
});

describe("usePluginAgent", () => {
  beforeEach(() => vi.clearAllMocks());

  it("looks up nothing without a plugin", () => {
    const { result } = renderHook(() => usePluginAgent("", "ch1"));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current).toMatchObject({ threadId: null, running: false });
  });

  it("takes the thread and status the lookup finds", async () => {
    fetchMock.mockResolvedValue({ thread_id: "plugin:t1", running: true });
    const { result } = renderHook(() => usePluginAgent("cogtest", "ch1"));
    await waitFor(() => expect(result.current.threadId).toBe("plugin:t1"));
    expect(result.current.running).toBe(true);
    expect(fetchMock).toHaveBeenCalledWith("cogtest", "ch1");
  });

  it("keeps no thread when the plugin hasn't run one", async () => {
    fetchMock.mockResolvedValue({ running: false });
    const { result } = renderHook(() => usePluginAgent("cogtest", "ch1"));
    await act(async () => {});
    expect(result.current.threadId).toBeNull();
  });

  it("logs a failed lookup", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error("down"));
    renderHook(() => usePluginAgent("cogtest", "ch1"));
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] looking up the plugin's agent:", expect.any(Error)));
  });

  it("keeps a run that started while the lookup was in flight", async () => {
    let answer!: (a: { thread_id?: string; running: boolean }) => void;
    fetchMock.mockReturnValue(new Promise((r) => (answer = r)));
    const { result } = renderHook(() => usePluginAgent("cogtest", "ch1"));
    act(() => result.current.onEvent(event("plugin.run_started", { plugin_id: "cogtest", thread_id: "plugin:new" })));
    await act(async () => answer({ thread_id: "plugin:old", running: false }));
    expect(result.current).toMatchObject({ threadId: "plugin:new", running: true });
  });

  it("drops the lookup of a channel it left", async () => {
    let answer!: (a: { thread_id?: string; running: boolean }) => void;
    fetchMock.mockReturnValueOnce(new Promise((r) => (answer = r))).mockResolvedValue({ running: false });
    const { result, rerender } = renderHook(({ ch }) => usePluginAgent("cogtest", ch), { initialProps: { ch: "a" } });
    rerender({ ch: "b" });
    await act(async () => answer({ thread_id: "plugin:a", running: true }));
    expect(result.current.threadId).toBeNull();
  });

  it("ignores other events and follows the thread's status once subscribed", async () => {
    fetchMock.mockResolvedValue({ running: false });
    const listeners: ChatEventListener[] = [];
    const unsubscribe = vi.fn();
    const subscribe = vi.fn((_: string, l: ChatEventListener) => {
      listeners.push(l);
      return unsubscribe;
    });
    const { result, unmount } = renderHook(() => usePluginAgent("cogtest", "ch1", subscribe));
    await act(async () => {});
    act(() => result.current.onEvent(event("plugin.event", { plugin_id: "cogtest" })));
    expect(subscribe).not.toHaveBeenCalled();
    act(() => result.current.onEvent(event("plugin.run_started", { plugin_id: "cogtest", thread_id: "plugin:t2" })));
    expect(subscribe).toHaveBeenCalledWith("plugin:t2", expect.any(Function));
    act(() => listeners[0]!(event("agent.status", { status: "idle" })));
    expect(result.current.running).toBe(false);
    act(() => listeners[0]!(event("agent.status", { status: "running" })));
    expect(result.current.running).toBe(true);
    act(() => listeners[0]!(event("agent.message", {})));
    expect(result.current.running).toBe(true);
    unmount();
    expect(unsubscribe).toHaveBeenCalled();
  });
});

describe("usePluginAgentOpen", () => {
  const shownHost = () => ({ offsetParent: document.body }) as unknown as HTMLElement;

  it("starts from what's kept and keeps what it's set to", () => {
    localStorage.setItem("k1", "true");
    const { result, rerender } = renderHook(({ key }) => usePluginAgentOpen(key, "leaf", null), { initialProps: { key: "k1" } });
    expect(result.current[0]).toBe(true);
    act(() => result.current[1](false));
    expect(result.current[0]).toBe(false);
    expect(localStorage.getItem("k1")).toBe("false");
    localStorage.setItem("k2", "true");
    rerender({ key: "k2" });
    expect(result.current[0]).toBe(true);
  });

  it("opening one pane's view closes the others' and tells the window", () => {
    const seen = vi.fn();
    window.addEventListener(PLUGIN_AGENT_OPENED, seen);
    const a = renderHook(() => usePluginAgentOpen("ka", "a", null));
    const b = renderHook(() => usePluginAgentOpen("kb", "b", null));
    act(() => b.result.current[1](true));
    act(() => a.result.current[1](true));
    expect(a.result.current[0]).toBe(true);
    expect(b.result.current[0]).toBe(false);
    expect((seen.mock.calls.at(-1)![0] as CustomEvent<string>).detail).toBe("a");
    window.removeEventListener(PLUGIN_AGENT_OPENED, seen);
  });

  it("Escape closes an open view over a shown layout", () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => usePluginAgentOpen("ke", "leaf", shownHost()));
    act(() => result.current[1](true));
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter" }));
      vi.runAllTimers();
    });
    expect(result.current[0]).toBe(true);
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
      vi.runAllTimers();
    });
    expect(result.current[0]).toBe(false);
  });

  it("leaves Escape another listener took", () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => usePluginAgentOpen("kt", "leaf", shownHost()));
    act(() => result.current[1](true));
    const take = (e: KeyboardEvent) => e.preventDefault();
    window.addEventListener("keydown", take);
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", cancelable: true }));
      vi.runAllTimers();
    });
    window.removeEventListener("keydown", take);
    expect(result.current[0]).toBe(true);
  });

  it("ignores Escape while the layout is hidden, and clears a pending close on unmount", () => {
    vi.useFakeTimers();
    const hidden = { offsetParent: null } as unknown as HTMLElement;
    const h = renderHook(() => usePluginAgentOpen("kh", "leaf", hidden));
    act(() => h.result.current[1](true));
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
      vi.runAllTimers();
    });
    expect(h.result.current[0]).toBe(true);
    h.unmount();

    const clear = vi.spyOn(globalThis, "clearTimeout");
    const s = renderHook(() => usePluginAgentOpen("ks", "leaf", shownHost()));
    act(() => s.result.current[1](true));
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    clear.mockClear();
    s.unmount();
    expect(clear).toHaveBeenCalled();
    expect(localStorage.getItem("ks")).toBe("true");
  });
});

describe("PluginAgentBadge", () => {
  function slot(leafId: string) {
    const el = document.createElement("div");
    el.id = `pane-header-slot-${leafId}`;
    document.body.appendChild(el);
    return el;
  }

  it("renders nothing without the pane header's slot", () => {
    render(<PluginAgentBadge leafId="none" running={false} open={false} onToggle={() => {}} />);
    expect(screen.queryByTestId("plugin-agent-badge")).toBeNull();
  });

  it.each([
    { open: false, running: false, title: "Open the plugin's agent chat" },
    { open: true, running: true, title: "Close the plugin's agent chat" },
  ])("shows open=$open running=$running in the slot and toggles", async ({ open, running, title }) => {
    const el = slot(`l-${open}`);
    const onToggle = vi.fn();
    render(<PluginAgentBadge leafId={`l-${open}`} running={running} open={open} onToggle={onToggle} />);
    const badge = screen.getByTestId("plugin-agent-badge");
    expect(el.contains(badge)).toBe(true);
    expect(badge.getAttribute("title")).toBe(title);
    expect(badge.getAttribute("aria-expanded")).toBe(String(open));
    expect(screen.getByTestId("agent-icon").getAttribute("data-animated")).toBe(String(running));
    expect(screen.getByTestId("agent-icon").getAttribute("data-color") !== "").toBe(!running);
    await userEvent.setup().click(badge);
    expect(onToggle).toHaveBeenCalledOnce();
    el.remove();
  });
});

describe("PluginAgentThread", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows the thread read-only with the store's state, subscribing through the store", () => {
    const unsubscribe = vi.fn();
    const subscribe = vi.fn(() => unsubscribe);
    const saved = { messages: [] } as never;
    const getChatState = vi.fn(() => saved);
    const onChatStateUnmount = vi.fn();
    render(<PluginAgentThread threadId="plugin:t" running roots={[]} subscribeChannelEvents={subscribe} getChatState={getChatState} onChatStateUnmount={onChatStateUnmount} />);
    const view = screen.getByTestId("chat-view");
    expect(view.getAttribute("data-channel")).toBe("plugin:t");
    expect(view.getAttribute("data-autofocus")).toBe("false");
    expect(view.textContent).toContain("The plugin's agent runs: only the plugin starts them.");
    expect(view.textContent).toContain("chat state");
    const [channel, running, opts] = chatStateMock.mock.calls.at(-1)!;
    expect(channel).toBe("plugin:t");
    expect(running).toBe(true);
    expect(opts!.initialState).toBe(saved);
    expect(getChatState).toHaveBeenCalledWith("plugin:t");
    const listener = vi.fn();
    expect(opts!.subscribeChatEvents!(listener)).toBe(unsubscribe);
    expect(subscribe).toHaveBeenCalledWith("plugin:t", listener);
    opts!.onUnmount!(saved);
    expect(onChatStateUnmount).toHaveBeenCalledWith("plugin:t", saved);
  });

  it("works without the store", () => {
    render(<PluginAgentThread threadId="plugin:t" running={false} />);
    const opts = chatStateMock.mock.calls.at(-1)![2]!;
    expect(opts.initialState).toBeUndefined();
    expect(() => opts.subscribeChatEvents!(() => {})()).not.toThrow();
    expect(() => opts.onUnmount!({} as never)).not.toThrow();
  });
});
