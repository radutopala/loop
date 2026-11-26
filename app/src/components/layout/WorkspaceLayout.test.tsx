import { act, createEvent, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { closeTerminalSessionById, killAgentContainer } from "../../api/loopApi";
import { CanvasLayout } from "../../canvas/CanvasLayout";
import type { CanvasNode } from "../../canvas/types";
import { useAgentRegistry } from "../../hooks/useAgentRegistry";
import { useChatState } from "../../hooks/useChatState";
import { useComposerSwitches } from "../../hooks/useComposerSwitches";
import { useContainerStats } from "../../hooks/useContainerStats";
import { useEditorState } from "../../hooks/useEditorState";
import { useExplain } from "../../hooks/useExplain";
import { useLearn } from "../../hooks/useLearn";
import { usePromptEdits } from "../../hooks/usePromptEdits";
import { takeStoredSession } from "../../hooks/useSessionPersistence";
import { ensureDefaultLayouts, loadChannelLayouts, saveLayout, saveLayoutType } from "../../layouts/persistence";
import { sideChats } from "../../layouts/sideChats";
import { NotificationButton, NotificationDrawer } from "../../notifications/NotificationDrawer";
import { PluginPanel } from "../../plugins/PluginPanel";
import { useInstalledPlugins } from "../../plugins/PluginPanelsContext";
import { EmptyLayoutPicker } from "../../splitPane/AddPanelButton";
import { PaneLeafHeader } from "../../splitPane/PaneLeafHeader";
import { SplitPaneLayout } from "../../splitPane/SplitPaneLayout";
import { collectLeaves } from "../../splitPane/treeOps";
import { ThemeProvider } from "../../ThemeContext";
import type { Channel } from "../../types";
import type { LeafNode, PaneNode } from "../../types/panels";
import { getSideReader, getWorkspace, registerChatInput, registerTerminalInput } from "../../uiBridge/registry";
import { ChatView } from "../chat/ChatView";
import { EditorPanel } from "../panels/EditorPanel";
import { FileTreePanel } from "../panels/FileTreePanel";
import { GitPanel } from "../panels/GitPanel";
import { MemoryPanel } from "../panels/MemoryPanel";
import { QualityPanel } from "../panels/QualityPanel";
import { ReviewPanel } from "../panels/ReviewPanel";
import { getCloseForInstance, Terminal } from "../panels/Terminal";
import { ChannelHeaderInfo } from "./ChannelHeaderInfo";
import { HeaderBranchPicker } from "./HeaderBranchPicker";
import { SideChatPane, type SideChatView } from "./SideChatPane";
import { WorkspaceLayout, type WorkspaceLayoutRef } from "./WorkspaceLayout";

const stubs = vi.hoisted(() => ({
  /** A component stub that renders an empty element named by testId. */
  box: (testId: (p: any) => string) => (p: any) => <div data-testid={testId(p)} />,
  /** A chat badge stub: a button showing its state that toggles its view. */
  badge: (name: string) => (p: any) => (
    <button type="button" aria-pressed={!!p.open} onClick={p.onToggle}>
      {`${name} ${p.leafId}`}
    </button>
  ),
}));

vi.mock("../../api/loopApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/loopApi")>()),
  closeTerminalSessionById: vi.fn(),
  killAgentContainer: vi.fn(),
}));
vi.mock("../../hooks/useSessionPersistence", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/useSessionPersistence")>()),
  takeStoredSession: vi.fn(),
}));
vi.mock("../../layouts/sideChats", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../layouts/sideChats")>()),
  sideChats: { open: vi.fn(), close: vi.fn(), sweep: vi.fn() },
}));
vi.mock("../../hooks/useChatState", () => ({ useChatState: vi.fn() }));
vi.mock("../../hooks/useAgentRegistry", () => ({ useAgentRegistry: vi.fn() }));
vi.mock("../../hooks/useContainerStats", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/useContainerStats")>()),
  useContainerStats: vi.fn(),
}));
vi.mock("../../hooks/useEditorState", () => ({ useEditorState: vi.fn() }));
vi.mock("../../hooks/useExplain", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/useExplain")>()),
  useExplain: vi.fn(),
}));
vi.mock("../../hooks/useLearn", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/useLearn")>()),
  useLearn: vi.fn(),
}));
vi.mock("../../hooks/usePromptEdits", () => ({ usePromptEdits: vi.fn() }));
vi.mock("../../hooks/useComposerSwitches", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/useComposerSwitches")>()),
  useComposerSwitches: vi.fn(),
}));
vi.mock("../../plugins/PluginPanelsContext", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../plugins/PluginPanelsContext")>()),
  useInstalledPlugins: vi.fn(),
}));
vi.mock("../../plugins/PluginPanel", () => ({ PluginPanel: vi.fn(stubs.box((p) => `plugin-panel-${p.leafId}`)) }));
vi.mock("../../notifications/NotificationDrawer", () => ({
  NotificationButton: vi.fn((p: any) => (
    <button type="button" onClick={p.onToggle}>
      Notifications
    </button>
  )),
  NotificationDrawer: vi.fn((p: any) =>
    p.open ? (
      <button type="button" onClick={p.onClose}>
        Close notifications
      </button>
    ) : null,
  ),
}));
vi.mock("../../canvas/CanvasLayout", async () => {
  const { useRef } = await import("react");
  return {
    CanvasLayout: vi.fn((p: any) => {
      // The canvas's tile maximized, as the real control keeps it.
      const maximized = useRef<string | null>(null);
      p.controlRef.current = {
        maximized: () => maximized.current,
        maximize: (c: CanvasNode, id: string | null) => {
          maximized.current = id;
          return { ...c, viewport: { ...c.viewport, zoom: id ? 2 : 1 } };
        },
      };
      return (
        <div data-testid="canvas">
          {p.canvas.tiles.map((t: any) => (
            <div key={t.id} data-testid={`tile-${t.id}`}>
              {p.renderLeaf({ type: "leaf", id: t.id, panel: t.panel, flex: 1, openMode: t.openMode, plugin: t.plugin })}
            </div>
          ))}
          {p.learnDock && (
            <div ref={p.learnDock.ref} data-testid="learn-dock" data-tile={p.learnDock.tileId ?? ""} data-view={p.learnDock.view} data-running={String(p.learnDock.running)}>
              {p.learnDock.pane}
            </div>
          )}
        </div>
      );
    }),
  };
});
vi.mock("../../splitPane/SplitPaneLayout", async (importOriginal) => {
  const leaves = (n: any): any[] => (n.type === "leaf" ? [n] : n.children.flatMap(leaves));
  return {
    ...(await importOriginal<typeof import("../../splitPane/SplitPaneLayout")>()),
    SplitPaneLayout: vi.fn((p: any) => (
      <div data-testid="split">
        {leaves(p.tree)
          .filter((l) => !p.minimizedLeaves.has(l.id))
          .map((l) => (
            <div key={l.id} data-testid={`leaf-${l.id}`}>
              {p.renderLeaf(l)}
            </div>
          ))}
      </div>
    )),
  };
});
vi.mock("../../splitPane/PaneLeafHeader", () => ({ PaneLeafHeader: vi.fn(stubs.box((p) => `header-${p.leafId}`)) }));
vi.mock("../../splitPane/AddPanelButton", () => ({ EmptyLayoutPicker: vi.fn(stubs.box(() => "empty-picker")) }));
vi.mock("../chat/ChatView", async () => {
  const { useContext } = await import("react");
  const { ComponentFocusContext } = await import("../chat/ChatComponent");
  return {
    ChatView: vi.fn(() => {
      const show = useContext(ComponentFocusContext);
      return (
        <div data-testid="chat-view">
          <textarea aria-label="Composer" />
          <button type="button" onClick={() => show?.({ template: "t", title: "A chart", doc: "" })}>
            Show component
          </button>
        </div>
      );
    }),
  };
});
vi.mock("../chat/ChatComponent", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../chat/ChatComponent")>()),
  ChatComponentFull: (p: any) => (
    <div data-testid="component-full">
      {p.component.title}
      <button type="button" onClick={p.onClose}>
        Close component
      </button>
    </div>
  ),
}));
vi.mock("../chat/ExplainBadge", () => ({ ExplainBadge: stubs.badge("Explain") }));
vi.mock("../chat/LearnBadge", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../chat/LearnBadge")>()),
  LearnBadge: stubs.badge("Learn"),
}));
vi.mock("../chat/PromptEditsBadge", () => ({ PromptEditsBadge: stubs.badge("Edits") }));
vi.mock("../chat/ShellBadge", () => ({ ShellBadge: stubs.badge("Shell") }));
vi.mock("../panels/Terminal", () => ({ Terminal: vi.fn(stubs.box((p) => `terminal-${p.instanceId}`)), getCloseForInstance: vi.fn() }));
vi.mock("../panels/AuditPanel", () => ({ AuditPanel: stubs.box(() => "audit-panel") }));
vi.mock("../panels/BrowserPanel", () => ({ BrowserPanel: stubs.box((p) => `browser-${p.fixedMode}`) }));
vi.mock("../panels/EditorPanel", () => ({ EditorPanel: vi.fn(stubs.box(() => "editor-panel")) }));
vi.mock("../panels/FileTreePanel", () => ({ FileTreePanel: vi.fn(stubs.box(() => "file-tree-panel")) }));
vi.mock("../panels/GitPanel", () => ({ GitPanel: vi.fn(stubs.box(() => "git-panel")) }));
vi.mock("../panels/KanbanPanel", () => ({ KanbanPanel: stubs.box(() => "kanban-panel") }));
vi.mock("../panels/MemoryPanel", () => ({ MemoryPanel: vi.fn(stubs.box(() => "memory-panel")) }));
vi.mock("../panels/NotesPanel", () => ({ NotesPanel: stubs.box(() => "notes-panel") }));
vi.mock("../panels/PlaygroundPanel", () => ({ PlaygroundPanel: stubs.box((p) => `playground-${p.instanceId}`) }));
vi.mock("../panels/QualityPanel", () => ({ QualityPanel: vi.fn(stubs.box(() => "quality-panel")) }));
vi.mock("../panels/ReviewPanel", () => ({ ReviewPanel: vi.fn(stubs.box(() => "review-panel")) }));
vi.mock("../panels/SessionsPanel", () => ({ SessionsPanel: stubs.box(() => "sessions-panel") }));
vi.mock("../panels/TasksPanel", () => ({ TasksPanel: stubs.box(() => "tasks-panel") }));
vi.mock("../panels/WorkflowsLayoutPanel", () => ({ WorkflowsLayoutPanel: stubs.box(() => "workflows-panel") }));
vi.mock("./ChannelHeaderInfo", () => ({ ChannelHeaderInfo: vi.fn(stubs.box(() => "header-info")) }));
vi.mock("./HeaderBranchPicker", () => ({ HeaderBranchPicker: vi.fn(stubs.box(() => "branch-picker")) }));
vi.mock("./SideChatPane", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./SideChatPane")>()),
  SideChatPane: vi.fn(stubs.box((p) => `side-chat-${p.leafId}`)),
}));
vi.mock("./ExplainPane", () => ({ ExplainPane: stubs.box(() => "explain-pane") }));
vi.mock("./ChatShellPane", () => ({ ChatShellPane: stubs.box(() => "shell-pane") }));
vi.mock("./PromptEditsPane", () => ({ PromptEditsPane: stubs.box(() => "edits-pane") }));
vi.mock("./LearnPane", () => ({
  LearnPane: () => (
    <div data-testid="learn-pane">
      <button type="button">Apply</button>
    </div>
  ),
}));

const CH = "ch1";
type Props = React.ComponentProps<typeof WorkspaceLayout>;

function makeChannel(over: Partial<Channel> = {}): Channel {
  return {
    id: CH,
    name: "proj",
    parent_id: "",
    dir_path: "/work/proj",
    session_id: "",
    active: true,
    container_running: false,
    agent_running: false,
    branch: "main",
    commit: "abc",
    worktree: false,
    locked: false,
    diff_additions: 0,
    diff_deletions: 0,
    review_enabled: true,
    ...over,
  } as Channel;
}

const leaf = (id: string, panel: LeafNode["panel"], extra: Partial<LeafNode> = {}): LeafNode => ({ type: "leaf", id, panel, flex: 1, ...extra });
const hsplit = (...children: PaneNode[]): PaneNode => ({ type: "split", direction: "horizontal", flex: 1, children });

/** Saves tree as the channel's active layout name. */
function seed(name: string, tree: PaneNode | CanvasNode, channelId = CH) {
  ensureDefaultLayouts(channelId);
  if (tree.type === "canvas") saveLayoutType(channelId, name, "canvas");
  saveLayout(channelId, name, tree as PaneNode);
}

const canvasOf = (...tiles: Array<Partial<CanvasNode["tiles"][number]> & { id: string; panel: LeafNode["panel"] }>): CanvasNode => ({
  type: "canvas",
  viewport: { x: 0, y: 0, zoom: 1 },
  tiles: tiles.map((t, i) => ({ x: i * 600, y: 0, width: 500, height: 400, zIndex: 0, ...t })),
});

const baseProps = (over: Partial<Props> = {}): Props => ({
  channelId: CH,
  channel: makeChannel(),
  sidebarOpen: true,
  onToggleSidebar: vi.fn(),
  onOpenPalette: vi.fn(),
  ...over,
});

function renderLayout(over: Partial<Props> = {}) {
  const ref = createRef<WorkspaceLayoutRef>();
  let props = baseProps(over);
  const r = render(<WorkspaceLayout ref={ref} {...props} />);
  return {
    ...r,
    ref,
    props,
    rerender: (more: Partial<Props>) => {
      props = { ...props, ...more };
      r.rerender(<WorkspaceLayout ref={ref} {...props} />);
    },
  };
}

/** The props of a stubbed component's last render. */
const last = (comp: any): any => vi.mocked(comp).mock.lastCall![0];
/** The props a stub last rendered with for a leaf. */
const lastFor = (comp: any, pred: (p: any) => boolean): any => [...vi.mocked(comp).mock.calls].reverse().find((c: any) => pred(c[0]))![0];

const splitTree = (): PaneNode => last(SplitPaneLayout).tree;
const splitPanels = () => collectLeaves(splitTree()).map((l) => l.panel);
const saved = (name: string) => loadChannelLayouts(CH)?.layouts[name];
const ws = () => getWorkspace(CH)!;
const tab = (name: string) => screen.getByTestId(`layout-tab-${name}`);
const tabNames = () => screen.getAllByTestId(/^layout-tab-/).map((t) => t.dataset.testid!.replace("layout-tab-", ""));

const openPanel = (detail: any) => act(() => void window.dispatchEvent(new CustomEvent("loop:open-panel", { detail })));
const openFile = (detail: any) => act(() => void window.dispatchEvent(new CustomEvent("loop:open-file", { detail })));
/** Lets queued timers (the Escape checks, presence fades) run. */
const flush = (ms = 0) => act(() => new Promise((r) => setTimeout(r, ms)));

interface Hooks {
  chatState: any;
  learn: any;
  explain: any;
  edits: any;
  editor: any;
}

function setHooks(over: Partial<Hooks> = {}): Hooks {
  const h: Hooks = {
    chatState: { gateApprovals: { "terminal:docker-agent-0": { id: "g1" } }, clearGateApproval: vi.fn() },
    learn: { learnChannelId: "learn-ch", running: false, proposals: [], shortcutsVersion: 0 },
    explain: { explanations: [] },
    edits: { editChannelId: null, running: false },
    editor: { roots: [], openFileAtLine: vi.fn() },
    ...over,
  };
  vi.mocked(useChatState).mockReturnValue(h.chatState);
  vi.mocked(useLearn).mockReturnValue(h.learn);
  vi.mocked(useExplain).mockReturnValue(h.explain);
  vi.mocked(usePromptEdits).mockReturnValue(h.edits);
  vi.mocked(useEditorState).mockReturnValue(h.editor);
  return h;
}

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  setHooks();
  vi.mocked(useAgentRegistry).mockReturnValue({ agents: new Map([["docker-agent-0", { name: "a0" }]]) } as any);
  vi.mocked(useContainerStats).mockReturnValue({});
  vi.mocked(useComposerSwitches).mockReturnValue({ switches: [], pending: new Set(), errors: new Map(), turn: vi.fn() });
  vi.mocked(useInstalledPlugins).mockReturnValue([]);
  vi.mocked(sideChats.close).mockResolvedValue(undefined);
  vi.mocked(getCloseForInstance).mockReturnValue(undefined);
  vi.mocked(takeStoredSession).mockReturnValue(null);
  vi.mocked(sideChats.sweep).mockResolvedValue([]);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("WorkspaceLayout header", () => {
  it("shows the default Chat layout with its panes", () => {
    renderLayout();
    expect(tabNames()).toEqual(["Chat", "Editor", "Memory", "Terminal", "Git", "Browser Chat", "Sessions", "Swarm", "Canvas", "Playground", "Kanban", "Workflows", "Review"]);
    expect(splitPanels()).toEqual(["chat", "git", "host-shell"]);
    expect(screen.getByTestId("chat-view")).toBeTruthy();
    expect(screen.getByTestId("git-panel")).toBeTruthy();
    expect(screen.getByTestId("terminal-host-shell-0")).toBeTruthy();
    expect(sideChats.sweep).toHaveBeenCalledWith(CH, expect.any(Function));
    const leafIds = vi.mocked(sideChats.sweep).mock.calls[0]![1];
    expect(leafIds()).toEqual(new Set());
  });

  it("expands the sidebar and opens the palette", async () => {
    const user = userEvent.setup();
    const { props } = renderLayout({ sidebarOpen: false });
    await user.click(screen.getByTitle("Expand sidebar"));
    expect(props.onToggleSidebar).toHaveBeenCalled();
    await user.click(screen.getByTitle("Search messages (Cmd+K)"));
    expect(props.onOpenPalette).toHaveBeenCalled();
    expect(screen.getByTitle("Search messages (Cmd+K)").textContent).toContain("Ctrl+K");
  });

  it("shows the Mac shortcut on a Mac", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    renderLayout();
    expect(screen.getByTitle("Search messages (Cmd+K)").textContent).toContain("⌘K");
    expect(screen.queryByTitle("Expand sidebar")).toBeNull();
  });

  it("shows the branch picker for a channel's checkout, with its actions", () => {
    const p = { onCreateWorktree: vi.fn(), onOpenPR: vi.fn(), onImportWorktree: vi.fn(), onSelectThread: vi.fn(), onStatusChange: vi.fn() };
    renderLayout(p);
    expect(last(ChannelHeaderInfo)).toMatchObject({ hideBranch: true });
    expect(last(HeaderBranchPicker)).toMatchObject({
      channelId: CH,
      branch: "main",
      onBranchChanged: p.onStatusChange,
      onCreateWorktree: p.onCreateWorktree,
      onOpenPR: p.onOpenPR,
      onImportWorktree: p.onImportWorktree,
      onSelectThread: p.onSelectThread,
    });
  });

  it("gives a thread's branch picker no worktree actions", () => {
    renderLayout({ channel: makeChannel({ parent_id: "parent" }), onCreateWorktree: vi.fn(), onOpenPR: vi.fn(), onImportWorktree: vi.fn(), onSelectThread: vi.fn() });
    expect(last(HeaderBranchPicker)).toMatchObject({ onCreateWorktree: undefined, onOpenPR: undefined, onImportWorktree: undefined, onSelectThread: undefined });
    // A thread has no Sessions tab.
    expect(tabNames()).not.toContain("Sessions");
  });

  it("shows no branch picker in a worktree, nor any header info without a dir", () => {
    const { rerender } = renderLayout({ channel: makeChannel({ worktree: true }) });
    expect(last(ChannelHeaderInfo)).toMatchObject({ hideBranch: false });
    expect(screen.queryByTestId("branch-picker")).toBeNull();
    rerender({ channel: makeChannel({ worktree: false }), parentIsWorktree: true });
    expect(screen.queryByTestId("branch-picker")).toBeNull();
    rerender({ channel: makeChannel({ dir_path: "", branch: "" }), parentIsWorktree: false });
    expect(screen.queryByTestId("header-info")).toBeNull();
  });

  it("shows the error and the branch picker's, and dismisses them", async () => {
    const user = userEvent.setup();
    const onDismissError = vi.fn();
    const { rerender } = renderLayout({ error: "boom", onDismissError });
    expect(screen.getByRole("alert").textContent).toContain("boom");
    act(() => last(HeaderBranchPicker).onError("checkout failed"));
    expect(screen.getByRole("alert").textContent).toContain("checkout failed");
    await user.click(within(screen.getByRole("alert")).getByRole("button"));
    expect(onDismissError).toHaveBeenCalled();
    rerender({ error: null, onDismissError: undefined });
    expect(screen.queryByRole("alert")).toBeNull();
    act(() => last(HeaderBranchPicker).onError("again"));
    await user.click(within(screen.getByRole("alert")).getByRole("button"));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("hides the Review tab and pane without the review feature", () => {
    renderLayout({ channel: makeChannel({ review_enabled: false }) });
    expect(tabNames()).not.toContain("Review");
    expect(last(SplitPaneLayout).hiddenPanels).toEqual(["review"]);
  });

  it("opens and closes the notification drawer", async () => {
    const user = userEvent.setup();
    renderLayout();
    expect(last(NotificationDrawer)).toMatchObject({ open: false, channelId: CH, channelName: "proj", dirPath: "/work/proj" });
    await user.click(screen.getByRole("button", { name: "Notifications" }));
    expect(last(NotificationButton).open).toBe(true);
    await user.click(screen.getByRole("button", { name: "Close notifications" }));
    expect(last(NotificationDrawer).open).toBe(false);
  });

  it("highlights the header buttons on hover", async () => {
    renderLayout({ channel: makeChannel({ container_running: true }) });
    for (const title of ["New layout", "Stop all shells and container", "Layout options"]) {
      const b = screen.getByTitle(title);
      const before = b.style.color;
      fireEvent.mouseEnter(b);
      expect(b.style.color).not.toBe(before);
      fireEvent.mouseLeave(b);
      expect(b.style.color).toBe(before);
    }
    fireEvent.click(screen.getByTitle("New layout"));
    fireEvent.click(screen.getByTitle("Layout options"));
    for (const name of ["Split", "Canvas", "Reset current"]) {
      const b = screen.getByRole("button", { name });
      fireEvent.mouseEnter(b);
      expect(b.style.backgroundColor).not.toBe("transparent");
      fireEvent.mouseLeave(b);
      expect(b.style.backgroundColor).toBe("transparent");
    }
  });
});

describe("WorkspaceLayout tabs", () => {
  it("switches tabs and remembers the active one", async () => {
    const user = userEvent.setup();
    renderLayout();
    await user.click(tab("Git"));
    expect(splitPanels()).toEqual(["git"]);
    expect(loadChannelLayouts(CH)?.active).toBe("Git");
    const renders = vi.mocked(SplitPaneLayout).mock.calls.length;
    // The active tab clicked again does nothing.
    await user.click(tab("Git"));
    expect(vi.mocked(SplitPaneLayout).mock.calls.length).toBe(renders);
  });

  it("switches by ref to a canvas, a split and an empty tab", () => {
    seed("Custom", canvasOf({ id: "chat", panel: "chat" }));
    saveLayout(CH, "Chat", createDefaultChat());
    const { ref } = renderLayout();
    act(() => ref.current!.switchToLayout("Canvas"));
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.id)).toEqual(["docker-agent-0", "docker-agent-1", "docker-agent-2", "git", "playground", "memory"]);
    expect(screen.getByTestId("terminal-docker-agent-0")).toBeTruthy();
    act(() => ref.current!.switchToLayout("Swarm"));
    expect(splitPanels()).toEqual(["docker-agent", "docker-agent", "docker-agent"]);
    // A tab not saved yet shows the empty picker.
    act(() => ref.current!.switchToLayout("Ghost"));
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
    // A canvas tab without a saved canvas shows an empty one.
    saveLayoutType(CH, "Blank", "canvas");
    act(() => ref.current!.switchToLayout("Blank"));
    expect(last(CanvasLayout).canvas.tiles).toEqual([]);
  });

  it("adds split and canvas tabs with fresh names", async () => {
    const user = userEvent.setup();
    renderLayout();
    await user.click(screen.getByTitle("New layout"));
    await user.click(screen.getByRole("button", { name: "Split" }));
    expect(tabNames().at(-1)).toBe("Layout 1");
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Split" })).toBeNull();
    await user.click(screen.getByTitle("New layout"));
    await user.click(screen.getByRole("button", { name: "Split" }));
    expect(tabNames().at(-1)).toBe("Layout 2");
    await user.click(screen.getByTitle("New layout"));
    await user.click(screen.getByTestId("new-layout-canvas"));
    expect(tabNames().at(-1)).toBe("Canvas 1");
    expect(last(CanvasLayout).canvas.tiles).toEqual([]);
    expect(loadChannelLayouts(CH)?.types["Canvas 1"]).toBe("canvas");
    await user.click(screen.getByTitle("New layout"));
    await user.click(screen.getByTestId("new-layout-canvas"));
    expect(tabNames().at(-1)).toBe("Canvas 2");
  });

  it("closes the new-layout menu on a click outside it", async () => {
    const user = userEvent.setup();
    renderLayout();
    await user.click(screen.getByTitle("New layout"));
    fireEvent.mouseDown(screen.getByRole("button", { name: "Split" }));
    expect(screen.getByRole("button", { name: "Split" })).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("button", { name: "Split" })).toBeNull();
  });

  it("fills an empty tab from the picker", () => {
    const { ref } = renderLayout();
    act(() => ref.current!.switchToLayout("Ghost"));
    act(() => last(EmptyLayoutPicker).onAdd("plugin"));
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
    act(() => last(EmptyLayoutPicker).onAdd("notes"));
    expect(splitPanels()).toEqual(["notes"]);
    expect(tabNames()).toContain("Ghost");
    expect(saved("Ghost")).toEqual(leaf("notes", "notes"));
  });

  it("puts a file tree beside an editor added to an empty tab", () => {
    const { ref } = renderLayout();
    act(() => ref.current!.switchToLayout("Ghost"));
    act(() => last(EmptyLayoutPicker).onAdd("editor"));
    expect(splitPanels()).toEqual(["file-tree", "editor"]);
  });

  it("adds plugin and agent panes from the picker", () => {
    const { ref } = renderLayout();
    act(() => ref.current!.switchToLayout("Ghost"));
    const plugin = { id: "acme", panel: "board", label: "Board" };
    act(() => last(EmptyLayoutPicker).onAdd("plugin", { plugin }));
    expect(collectLeaves(splitTree())[0]).toMatchObject({ panel: "plugin", plugin });
    expect(last(PluginPanel)).toMatchObject({ plugin, channelId: CH, channelName: "proj" });
    act(() => ref.current!.switchToLayout("Ghost2"));
    act(() => last(EmptyLayoutPicker).onAdd("docker-agent", { openMode: "fresh" }));
    expect(collectLeaves(splitTree())[0]).toMatchObject({ panel: "docker-agent", openMode: "fresh" });
  });

  it("renames a tab", async () => {
    const user = userEvent.setup();
    renderLayout();
    fireEvent.doubleClick(within(tab("Memory")).getByText("Memory"));
    const input = within(tab("Memory")).getByRole("textbox");
    await user.clear(input);
    await user.type(input, "  Notes  {Enter}");
    expect(tabNames()).toContain("Notes");
    expect(loadChannelLayouts(CH)?.order).toContain("Notes");
    expect(saved("Notes")).toEqual(leaf("memory", "memory"));
    // The active tab renamed stays active.
    fireEvent.doubleClick(within(tab("Chat")).getByText("Chat"));
    await user.type(within(tab("Chat")).getByRole("textbox"), "!{Enter}");
    expect(tab("Chat!").style.color).not.toBe(tab("Notes").style.color);
    expect(loadChannelLayouts(CH)?.active).toBe("Chat!");
  });

  it("keeps a tab's name on Escape, on an unchanged blur, and on a taken name", async () => {
    const user = userEvent.setup();
    renderLayout();
    fireEvent.doubleClick(within(tab("Git")).getByText("Git"));
    await user.type(within(tab("Git")).getByRole("textbox"), "X{Escape}");
    expect(within(tab("Git")).queryByRole("textbox")).toBeNull();
    expect(tabNames()).toContain("Git");
    // Clicking in the input doesn't select the tab.
    fireEvent.doubleClick(within(tab("Git")).getByText("Git"));
    await user.click(within(tab("Git")).getByRole("textbox"));
    expect(loadChannelLayouts(CH)?.active).toBe("Chat");
    fireEvent.blur(within(tab("Git")).getByRole("textbox"));
    expect(tabNames()).toContain("Git");
    fireEvent.doubleClick(within(tab("Git")).getByText("Git"));
    const input = within(tab("Git")).getByRole("textbox");
    await user.clear(input);
    await user.type(input, "Memory{Enter}");
    expect(tabNames().filter((n) => n === "Memory")).toHaveLength(1);
    expect(tabNames()).toContain("Git");
  });

  it("reorders tabs by dragging", () => {
    renderLayout();
    const data = new Map<string, string>();
    const dataTransfer = {
      setData: (k: string, v: string) => data.set(k, v),
      getData: (k: string) => data.get(k) ?? "",
      get types() {
        return [...data.keys()];
      },
      effectAllowed: "",
      dropEffect: "",
    };
    fireEvent.dragStart(tab("Git"), { dataTransfer });
    expect(dataTransfer.effectAllowed).toBe("move");
    const over = createEvent.dragOver(tab("Chat"), { dataTransfer });
    fireEvent(tab("Chat"), over);
    expect(over.defaultPrevented).toBe(true);
    expect(tab("Chat").style.outline).not.toBe("");
    fireEvent.dragOver(tab("Chat"), { dataTransfer });
    fireEvent.dragLeave(tab("Chat"));
    expect(tab("Chat").style.outline).toBe("");
    fireEvent.dragLeave(tab("Chat"));
    fireEvent.drop(tab("Chat"), { dataTransfer });
    expect(tabNames().slice(0, 2)).toEqual(["Git", "Chat"]);
    expect(loadChannelLayouts(CH)?.order.slice(0, 2)).toEqual(["Git", "Chat"]);
    // A tab dropped on itself, or a name that isn't a tab, moves nothing.
    fireEvent.drop(tab("Git"), { dataTransfer });
    data.set("application/x-loop-layout-tab", "Nope");
    fireEvent.drop(tab("Memory"), { dataTransfer });
    expect(tabNames().slice(0, 2)).toEqual(["Git", "Chat"]);
    // A drag of something else isn't taken.
    const other = createEvent.dragOver(tab("Chat"), { dataTransfer: { types: ["text/plain"] } });
    fireEvent(tab("Chat"), other);
    expect(other.defaultPrevented).toBe(false);
  });

  it("highlights an inactive tab and its delete button on hover", () => {
    renderLayout();
    const t = tab("Git");
    const before = t.style.color;
    fireEvent.mouseEnter(t);
    expect(t.style.color).not.toBe(before);
    fireEvent.mouseLeave(t);
    expect(t.style.color).toBe(before);
    const active = tab("Chat");
    const activeColor = active.style.color;
    fireEvent.mouseEnter(active);
    fireEvent.mouseLeave(active);
    expect(active.style.color).toBe(activeColor);
    const del = within(t).getByTitle("Delete layout");
    fireEvent.mouseEnter(del);
    const hovered = del.style.color;
    fireEvent.mouseLeave(del);
    expect(del.style.color).not.toBe(hovered);
  });
});

describe("WorkspaceLayout tab deletion", () => {
  const confirmDelete = async (user: ReturnType<typeof userEvent.setup>, name: string) => {
    await user.click(within(tab(name)).getByTitle("Delete layout"));
    await user.click(screen.getByRole("button", { name: "Yes" }));
  };

  it("deletes the active tab, closing its terminals, and opens the first one left", async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    vi.mocked(getCloseForInstance).mockReturnValue(close);
    renderLayout();
    await confirmDelete(user, "Chat");
    expect(getCloseForInstance).toHaveBeenCalledWith(`host:${CH}:host-shell-0`);
    expect(close).toHaveBeenCalled();
    expect(tabNames()).not.toContain("Chat");
    expect(loadChannelLayouts(CH)?.removed).toEqual(["Chat"]);
    expect(splitPanels()).toEqual(["file-tree", "editor", "host-shell", "chat"]);
    expect(loadChannelLayouts(CH)?.active).toBe("Editor");
  });

  it("deletes another tab, closing its stored terminal sessions", async () => {
    const user = userEvent.setup();
    vi.mocked(takeStoredSession).mockImplementation((_c, _t, id) => (id === "docker-agent-1" ? "sid-1" : null));
    renderLayout();
    await confirmDelete(user, "Swarm");
    expect(takeStoredSession).toHaveBeenCalledWith(CH, "agent", "docker-agent-0");
    expect(closeTerminalSessionById).toHaveBeenCalledTimes(1);
    expect(closeTerminalSessionById).toHaveBeenCalledWith("sid-1", "agent");
    expect(splitPanels()).toEqual(["chat", "git", "host-shell"]);
  });

  it("deletes the active canvas, closing its tiles' sessions and ending its side chats", async () => {
    const user = userEvent.setup();
    seed("Board", canvasOf({ id: "docker-shell-1", panel: "docker-shell" }, { id: "side-1", panel: "chat-side" }));
    renderLayout();
    await confirmDelete(user, "Board");
    expect(takeStoredSession).toHaveBeenCalledWith(CH, "agent", "docker-shell-1");
    expect(sideChats.close).toHaveBeenCalledWith(CH, "side-1");
    expect(tabNames()).not.toContain("Board");
  });

  it("opens a canvas or an empty tab left first", async () => {
    const user = userEvent.setup();
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({
        [CH]: {
          active: "A",
          order: ["A", "Board", "Empty"],
          layouts: { A: leaf("notes", "notes"), Board: canvasOf({ id: "git", panel: "git" }) },
          types: { Board: "canvas" },
          version: 13,
          removed: ["Chat", "Editor", "Memory", "Terminal", "Git", "Browser Chat", "Sessions", "Swarm", "Canvas", "Playground", "Kanban", "Workflows", "Review"],
        },
      }),
    );
    renderLayout();
    expect(tabNames()).toEqual(["A", "Board", "Empty"]);
    await confirmDelete(user, "A");
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.id)).toEqual(["git"]);
    await confirmDelete(user, "Board");
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
    // The last tab can't go.
    expect(within(tab("Empty")).queryByTitle("Delete layout")).toBeNull();
    expect(() => act(() => ws().removeTab("Empty"))).not.toThrow();
    expect(tabNames()).toEqual(["Empty"]);
  });

  it("keeps the tab when the deletion is declined or the popover closes", async () => {
    const user = userEvent.setup();
    renderLayout();
    const open = () => user.click(within(tab("Git")).getByTitle("Delete layout"));
    await open();
    await user.click(screen.getByRole("button", { name: "No" }));
    expect(screen.queryByRole("button", { name: "Yes" })).toBeNull();
    await open();
    fireEvent.mouseDown(screen.getByRole("button", { name: "Yes" }));
    expect(screen.getByRole("button", { name: "Yes" })).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("button", { name: "Yes" })).toBeNull();
    await open();
    fireEvent(window, new Event("resize"));
    expect(screen.queryByRole("button", { name: "Yes" })).toBeNull();
    await open();
    fireEvent.scroll(screen.getByTestId("layout-tabs-strip"));
    expect(screen.queryByRole("button", { name: "Yes" })).toBeNull();
    expect(tabNames()).toContain("Git");
    expect(loadChannelLayouts(CH)?.active).toBe("Chat");
  });
});

describe("WorkspaceLayout reset and stop", () => {
  it("resets the current default layout, closing its terminals and side chats", async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    vi.mocked(getCloseForInstance).mockImplementation((key) => (key.includes("docker-agent-5") ? close : undefined));
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("side-1", "chat-side"), leaf("docker-agent-5", "docker-agent"), leaf("docker-shell-6", "docker-shell"), leaf("host-shell-7", "host-shell")));
    renderLayout();
    await user.click(screen.getByTitle("Layout options"));
    expect(screen.queryByRole("button", { name: "Restore defaults" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Reset current" }));
    expect(sideChats.close).toHaveBeenCalledWith(CH, "side-1");
    expect(getCloseForInstance).toHaveBeenCalledWith(`agent:${CH}:docker-agent-5`);
    expect(getCloseForInstance).toHaveBeenCalledWith(`agent:${CH}:docker-shell-6`);
    expect(getCloseForInstance).toHaveBeenCalledWith(`host:${CH}:host-shell-7`);
    expect(close).toHaveBeenCalledTimes(1);
    expect(splitPanels()).toEqual(["chat", "git", "host-shell"]);
    expect(saved("Chat")).toMatchObject({ type: "split", children: [{ id: "chat" }, { type: "split" }] });
    expect(screen.queryByRole("button", { name: "Reset current" })).toBeNull();
  });

  it("resets the default canvas, ending its side chats", async () => {
    const user = userEvent.setup();
    seed("Canvas", canvasOf({ id: "side-2", panel: "chat-side" }));
    renderLayout();
    await user.click(screen.getByTitle("Layout options"));
    await user.click(screen.getByRole("button", { name: "Reset current" }));
    expect(sideChats.close).toHaveBeenCalledWith(CH, "side-2");
    expect(last(CanvasLayout).canvas.tiles).toHaveLength(6);
  });

  it("logs a side chat that fails to end", async () => {
    const user = userEvent.setup();
    const err = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(sideChats.close).mockRejectedValue(new Error("gone"));
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("side-1", "chat-side")));
    renderLayout();
    await user.click(screen.getByTitle("Layout options"));
    await user.click(screen.getByRole("button", { name: "Reset current" }));
    await flush();
    expect(err).toHaveBeenCalled();
  });

  it("restores deleted default tabs", async () => {
    const user = userEvent.setup();
    seed("Mine", leaf("notes", "notes"));
    renderLayout();
    await user.click(within(tab("Memory")).getByTitle("Delete layout"));
    await user.click(screen.getByRole("button", { name: "Yes" }));
    expect(tabNames()).not.toContain("Memory");
    // A custom tab has nothing to reset.
    await user.click(screen.getByTitle("Layout options"));
    expect(screen.queryByRole("button", { name: "Reset current" })).toBeNull();
    fireEvent.mouseDown(screen.getByRole("button", { name: "Restore defaults" }));
    await user.click(screen.getByRole("button", { name: "Restore defaults" }));
    expect(tabNames()).toContain("Memory");
    expect(screen.queryByTitle("Layout options")).toBeNull();
  });

  it("closes the options menu on a click outside it", async () => {
    const user = userEvent.setup();
    renderLayout();
    await user.click(screen.getByTitle("Layout options"));
    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("button", { name: "Reset current" })).toBeNull();
  });

  it("stops the shells and the container", async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    vi.mocked(getCloseForInstance).mockReturnValue(close);
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("docker-agent-0", "docker-agent"), leaf("docker-shell-1", "docker-shell"), leaf("host-shell-2", "host-shell")));
    renderLayout({ channel: makeChannel({ container_running: true }) });
    await user.click(screen.getByTitle("Stop all shells and container"));
    expect(getCloseForInstance).toHaveBeenCalledWith(`agent:${CH}:docker-agent-0`);
    expect(getCloseForInstance).toHaveBeenCalledWith(`agent:${CH}:docker-shell-1`);
    expect(getCloseForInstance).toHaveBeenCalledWith(`host:${CH}:host-shell-2`);
    expect(close).toHaveBeenCalledTimes(3);
    expect(killAgentContainer).toHaveBeenCalledWith(CH);
  });

  it("stops the container of a tab without panes", async () => {
    const user = userEvent.setup();
    const { ref } = renderLayout({ channel: makeChannel({ container_running: true }) });
    act(() => ref.current!.switchToLayout("Ghost"));
    await user.click(screen.getByTitle("Stop all shells and container"));
    expect(getCloseForInstance).not.toHaveBeenCalled();
    expect(killAgentContainer).toHaveBeenCalledWith(CH);
  });

  it("shows Stop while a terminal pane runs", () => {
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("docker-agent-0", "docker-agent"), leaf("host-shell-1", "host-shell")));
    const onStatusChange = vi.fn();
    renderLayout({ onStatusChange });
    expect(screen.queryByTitle("Stop all shells and container")).toBeNull();
    const agent = lastFor(Terminal, (p) => p.instanceId === "docker-agent-0");
    act(() => agent.onPaneStatus("running"));
    expect(screen.getByTitle("Stop all shells and container")).toBeTruthy();
    act(() => agent.onPaneStatus("completed"));
    const shell = lastFor(Terminal, (p) => p.instanceId === "host-shell-1");
    act(() => shell.onPaneStatus("running"));
    expect(onStatusChange).not.toHaveBeenCalled();
    act(() => shell.onPaneStatus("failed"));
    expect(onStatusChange).toHaveBeenCalledTimes(1);
    expect(screen.queryByTitle("Stop all shells and container")).toBeNull();
    // A status of a pane that isn't a terminal leaves only the terminals'.
    act(() => last(SplitPaneLayout).onRemoveLeaf("host-shell-1"));
    act(() => last(SplitPaneLayout).onRemoveLeaf("docker-agent-0"));
    expect(screen.queryByTitle("Stop all shells and container")).toBeNull();
  });
});

function createDefaultChat(): PaneNode {
  return hsplit(leaf("chat", "chat"), leaf("git", "git"));
}

describe("WorkspaceLayout channel switching and auto-switch", () => {
  it("reloads layouts when channelId changes to split, canvas, and empty channels", () => {
    seed("Board", canvasOf({ id: "git", panel: "git" }), "ch-canvas");
    seed("Custom", leaf("notes", "notes"), "ch-split");
    // Empty active layout on ch-empty
    ensureDefaultLayouts("ch-empty");
    saveLayoutType("ch-empty", "Blank", "split");
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({
        ...JSON.parse(localStorage.getItem("loop-workspace-layout")!),
        "ch-empty": { active: "Blank", order: ["Blank"], layouts: {}, types: {}, version: 13 },
        "ch-empty-canvas": { active: "CBlank", order: ["CBlank"], layouts: {}, types: { CBlank: "canvas" }, version: 13 },
      }),
    );

    const onChatStateUnmount = vi.fn();
    const { rerender } = renderLayout({ onChatStateUnmount });
    // Verify onUnmount callback passed to useChatState forwards channelId
    const chatStateOpts = vi.mocked(useChatState).mock.lastCall![2]!;
    chatStateOpts.onUnmount!({ messages: [] } as any);
    expect(onChatStateUnmount).toHaveBeenCalledWith(CH, { messages: [] });

    rerender({ channelId: "ch-canvas", channel: makeChannel({ id: "ch-canvas" }) });
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.id)).toEqual(["git"]);

    rerender({ channelId: "ch-split", channel: makeChannel({ id: "ch-split" }) });
    expect(splitPanels()).toEqual(["notes"]);

    rerender({ channelId: "ch-empty", channel: makeChannel({ id: "ch-empty" }) });
    expect(screen.getByTestId("empty-picker")).toBeTruthy();

    rerender({ channelId: "ch-empty-canvas", channel: makeChannel({ id: "ch-empty-canvas" }) });
    expect(last(CanvasLayout).canvas.tiles).toEqual([]);
  });

  it("auto-switches to a layout with a chat pane when scrollToMessageId is set", () => {
    const { ref, rerender } = renderLayout();
    act(() => ref.current!.switchToLayout("Git"));
    expect(splitPanels()).toEqual(["git"]);
    rerender({ scrollToMessageId: 42 });
    expect(splitPanels()).toContain("chat");
    expect(loadChannelLayouts(CH)?.active).toBe("Chat");

    // When active layout already has chat, does not switch
    rerender({ scrollToMessageId: 43 });
    expect(loadChannelLayouts(CH)?.active).toBe("Chat");
  });

  it("does nothing on scrollToMessageId when no saved chat layout exists", () => {
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({
        [CH]: {
          active: "OnlyGit",
          order: ["OnlyGit", "Board"],
          layouts: { OnlyGit: leaf("git", "git"), Board: canvasOf({ id: "chat", panel: "chat" }) },
          types: { Board: "canvas" },
          version: 13,
          removed: ["Chat", "Editor", "Memory", "Terminal", "Git", "Browser Chat", "Sessions", "Swarm", "Canvas", "Playground", "Kanban", "Workflows", "Review"],
        },
      }),
    );
    const { rerender } = renderLayout();
    rerender({ scrollToMessageId: 99 });
    expect(loadChannelLayouts(CH)?.active).toBe("OnlyGit");
    // Also when localStorage was cleared so loadChannelLayouts returns null
    localStorage.clear();
    rerender({ scrollToMessageId: 100 });
    expect(splitPanels()).toEqual(["git"]);
  });

  it("auto-switches to a layout with a memory pane when openMemoryFile is set", async () => {
    const onOpenMemoryFileComplete = vi.fn();
    const { rerender } = renderLayout({ onOpenMemoryFileComplete });
    expect(splitPanels()).not.toContain("memory");
    rerender({ openMemoryFile: "notes.md", onOpenMemoryFileComplete });
    expect(loadChannelLayouts(CH)?.active).toBe("Memory");
    await flush(10);
    expect(onOpenMemoryFileComplete).toHaveBeenCalled();

    const callsBefore = onOpenMemoryFileComplete.mock.calls.length;
    // When already on Memory layout, calls onOpenMemoryFileComplete directly
    rerender({ openMemoryFile: "other.md", onOpenMemoryFileComplete });
    await flush(10);
    expect(onOpenMemoryFileComplete).toHaveBeenCalledTimes(callsBefore + 1);
  });

  it("handles openMemoryFile when on canvas or when no memory layout exists", async () => {
    const onOpenMemoryFileComplete = vi.fn();
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({
        [CH]: {
          active: "OnlyGit",
          order: ["OnlyGit", "Board"],
          layouts: { OnlyGit: leaf("git", "git"), Board: canvasOf({ id: "memory", panel: "memory" }) },
          types: { Board: "canvas" },
          version: 13,
          removed: ["Chat", "Editor", "Memory", "Terminal", "Git", "Browser Chat", "Sessions", "Swarm", "Canvas", "Playground", "Kanban", "Workflows", "Review"],
        },
      }),
    );
    const { ref, rerender } = renderLayout({ onOpenMemoryFileComplete });
    rerender({ openMemoryFile: "a.md", onOpenMemoryFileComplete });
    await flush(10);
    expect(onOpenMemoryFileComplete).not.toHaveBeenCalled();

    // When localStorage cleared
    localStorage.clear();
    rerender({ openMemoryFile: "b.md", onOpenMemoryFileComplete });
    await flush(10);
    expect(onOpenMemoryFileComplete).not.toHaveBeenCalled();

    // When on canvas (!tree)
    act(() => ref.current!.switchToLayout("Board"));
    rerender({ openMemoryFile: "c.md", onOpenMemoryFileComplete });
    await flush(10);
    expect(onOpenMemoryFileComplete).not.toHaveBeenCalled();
  });
});

describe("WorkspaceLayout tree operations, maximize, minimize, and component focus", () => {
  it("swaps, moves, and resizes leaves via SplitPaneLayout callbacks", () => {
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("git", "git")));
    renderLayout();
    // Update flex
    act(() => last(SplitPaneLayout).onUpdateFlex([], 0, 30, 70));
    expect((splitTree() as any).children[0].flex).toBe(30);
    expect((splitTree() as any).children[1].flex).toBe(70);
    // Same id drop is a no-op
    act(() => last(SplitPaneLayout).onDrop("chat", "chat", "center"));
    expect(splitPanels()).toEqual(["chat", "git"]);
    // Center drop swaps leaf ids
    act(() => last(SplitPaneLayout).onDrop("chat", "git", "center"));
    expect(collectLeaves(splitTree()).map((l) => l.id)).toEqual(["git", "chat"]);
    // Directional drop moves
    act(() => last(SplitPaneLayout).onDrop("chat", "git", "left"));
    expect(collectLeaves(splitTree()).map((l) => l.id)).toEqual(["chat", "git"]);
  });

  it("splits leaves with normal, singleton, plugin, docker-agent, and editor panels", () => {
    seed("Chat", leaf("chat", "chat"));
    renderLayout();
    // Cannot add duplicate singleton
    act(() => last(SplitPaneLayout).onSplitLeaf("chat", "chat", "horizontal"));
    expect(splitPanels()).toEqual(["chat"]);
    // Plugin without meta.plugin is ignored
    act(() => last(SplitPaneLayout).onSplitLeaf("chat", "plugin", "horizontal"));
    expect(splitPanels()).toEqual(["chat"]);
    // Plugin with meta.plugin is added
    const plugin = { id: "acme", panel: "p1", label: "P1" };
    act(() => last(SplitPaneLayout).onSplitLeaf("chat", "plugin", "horizontal", { plugin }));
    expect(splitPanels()).toEqual(["chat", "plugin"]);
    // Docker-agent with openMode
    act(() => last(SplitPaneLayout).onSplitLeaf("chat", "docker-agent", "vertical", { openMode: "fresh" }));
    expect(collectLeaves(splitTree()).find((l) => l.panel === "docker-agent")?.openMode).toBe("fresh");
    // Editor adds default file-tree
    act(() => last(SplitPaneLayout).onSplitLeaf("chat", "editor", "horizontal"));
    expect(splitPanels()).toContain("editor");
    expect(splitPanels()).toContain("file-tree");
  });

  it("removes leaves including last leaf and agent leaves", () => {
    seed("Chat", hsplit(leaf("docker-agent-0", "docker-agent"), leaf("docker-agent-1", "docker-agent"), leaf("git", "git")));
    const close = vi.fn();
    vi.mocked(getCloseForInstance).mockReturnValue(close);
    renderLayout();
    // Set both agents running so agentState is running
    act(() => lastFor(Terminal, (p) => p.instanceId === "docker-agent-0").onPaneStatus("running"));
    expect(screen.getByTitle("Stop all shells and container")).toBeTruthy();
    // Removing one docker-agent leaves the other (hasAgentLeaf true)
    act(() => last(SplitPaneLayout).onRemoveLeaf("docker-agent-0"));
    expect(close).toHaveBeenCalledTimes(1);
    expect(splitPanels()).toEqual(["docker-agent", "git"]);
    // Removing second docker-agent leaves no agent leaf -> agentState "none"
    act(() => last(SplitPaneLayout).onRemoveLeaf("docker-agent-1"));
    expect(screen.queryByTitle("Stop all shells and container")).toBeNull();
    // Removing unknown leaf id is safe
    act(() => last(SplitPaneLayout).onRemoveLeaf("nonexistent"));
    // Removing last leaf clears layout and shows empty picker
    act(() => last(SplitPaneLayout).onRemoveLeaf("git"));
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
  });

  it("maximizes, splits from maximized, removes maximized, and toggles minimize", () => {
    seed("Chat", hsplit(leaf("docker-agent-0", "docker-agent"), leaf("git", "git")));
    renderLayout();
    // Toggle minimize on and off
    act(() => last(SplitPaneLayout).onToggleMinimize("git"));
    expect(screen.queryByTestId("leaf-git")).toBeNull();
    act(() => last(SplitPaneLayout).onToggleMinimize("git"));
    expect(screen.getByTestId("leaf-git")).toBeTruthy();

    // Maximize docker-agent leaf -> PaneLeafHeader gets agentInfo
    act(() => last(SplitPaneLayout).onMaximize("docker-agent-0"));
    expect(last(PaneLeafHeader)).toMatchObject({ leafId: "docker-agent-0", isMaximized: true, agentInfo: { name: "a0" } });
    // Restore via onToggleMaximize
    act(() => last(PaneLeafHeader).onToggleMaximize());
    expect(screen.getByTestId("split")).toBeTruthy();

    // Maximize git leaf -> no agentInfo, then split from maximized restores layout
    act(() => last(SplitPaneLayout).onMaximize("git"));
    expect(last(PaneLeafHeader)).toMatchObject({ leafId: "git", isMaximized: true, agentInfo: undefined });
    act(() => last(PaneLeafHeader).onSplitLeaf("git", "notes", "horizontal"));
    expect(screen.getByTestId("split")).toBeTruthy();
    expect(splitPanels()).toContain("notes");

    // Maximize notes leaf and remove it from header
    act(() => last(SplitPaneLayout).onMaximize("notes"));
    act(() => last(PaneLeafHeader).onRemove());
    expect(screen.getByTestId("split")).toBeTruthy();
    expect(splitPanels()).not.toContain("notes");
  });

  it("opens and closes ChatComponentFull, restoring maximize state only when it was not already maximized", async () => {
    const user = userEvent.setup();
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("git", "git")));
    renderLayout();
    // Open component when chat is NOT maximized -> maximizes chat, then restores on close
    await user.click(screen.getByRole("button", { name: "Show component" }));
    expect(screen.getByTestId("component-full")).toBeTruthy();
    expect(last(PaneLeafHeader)).toMatchObject({ leafId: "chat", isMaximized: true });
    await user.click(screen.getByRole("button", { name: "Close component" }));
    expect(screen.queryByTestId("component-full")).toBeNull();
    expect(screen.getByTestId("split")).toBeTruthy();

    // Open component when chat IS already maximized -> stays maximized on close
    act(() => last(SplitPaneLayout).onMaximize("chat"));
    await user.click(screen.getByRole("button", { name: "Show component" }));
    expect(screen.getByTestId("component-full")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Close component" }));
    expect(screen.queryByTestId("component-full")).toBeNull();
    expect(last(PaneLeafHeader)).toMatchObject({ leafId: "chat", isMaximized: true });
  });

  it("closes terminal sessions when canvas tiles are removed via onCanvasChange", () => {
    seed("Board", canvasOf({ id: "host-shell-1", panel: "host-shell" }, { id: "git", panel: "git" }));
    const close = vi.fn();
    vi.mocked(getCloseForInstance).mockReturnValue(close);
    renderLayout();
    act(() => last(CanvasLayout).onCanvasChange(canvasOf({ id: "git", panel: "git" })));
    expect(close).toHaveBeenCalledTimes(1);
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.id)).toEqual(["git"]);
  });
});

describe("WorkspaceLayout UI bridge controller", () => {
  it("inspects split view with status, busy, openMode, plugin, maximized, and side", () => {
    const plugin = { id: "acme", panel: "board", label: "Board" };
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("docker-agent-0", "docker-agent", { openMode: "fresh" }), leaf("plugin-1", "plugin", { plugin })));
    const unregisterTerm = registerTerminalInput(CH, "docker-agent-0", { kind: "agent", status: () => "running", lastOutputAt: () => 0, send: vi.fn(), read: () => "", busy: () => true });
    renderLayout();
    act(() => lastFor(Terminal, (p) => p.instanceId === "docker-agent-0").onPaneStatus("running"));
    act(() => ws().maximize("chat"));
    act(() => ws().openSide("learn"));

    const v = ws().view();
    expect(v.tab).toBe("Chat");
    expect(v.canvas).toBe(false);
    expect(v.maximized).toBe("chat");
    expect(v.side).toBe("learn");
    expect(v.panes).toEqual([
      { id: "chat", panel: "chat" },
      { id: "docker-agent-0", panel: "docker-agent", open_mode: "fresh", status: "running", busy: true },
      { id: "plugin-1", panel: "plugin", plugin: "acme/board" },
    ]);

    act(() => ws().closeSide());
    expect(ws().view().side).toBeUndefined();
    act(() => ws().maximize(null));
    expect(ws().view().maximized).toBeUndefined();
    unregisterTerm();
  });

  it("refuses openSide('shell') when agent container is not running, allows when running", () => {
    renderLayout();
    expect(() => ws().openSide("shell")).toThrow(/container isn't running/);
    vi.mocked(useContainerStats).mockReturnValue({ agent: { cpu_percent: 1, memory_mb: 10 } } as any);
    const { rerender } = renderLayout();
    rerender({});
    act(() => ws().openSide("shell"));
    expect(ws().view().side).toBe("shell");
  });

  it("manages tabs via setTab, createTab, renameTab, removeTab", () => {
    renderLayout();
    let created = "";
    act(() => {
      created = ws().createTab("CustomSplit", false);
    });
    expect(created).toBe("CustomSplit");
    expect(ws().view().tab).toBe("CustomSplit");
    expect(ws().view().canvas).toBe(false);

    act(() => {
      created = ws().createTab("CustomCanvas", true);
    });
    expect(created).toBe("CustomCanvas");
    expect(ws().view().canvas).toBe(true);

    act(() => ws().renameTab("CustomCanvas", "RenamedCanvas"));
    expect(ws().view().tab).toBe("RenamedCanvas");

    act(() => ws().setTab("Git"));
    expect(ws().view().tab).toBe("Git");

    act(() => ws().removeTab("RenamedCanvas"));
    expect(ws().view().tabs).not.toContain("RenamedCanvas");
  });

  it("reports pluginPanes on split and canvas, and null when plugins not loaded", () => {
    vi.mocked(useInstalledPlugins).mockReturnValue(null as any);
    renderLayout();
    expect(ws().pluginPanes()).toBeNull();

    const plugins = [
      {
        id: "acme",
        name: "Acme",
        panels: [
          { id: "board", label: "Board", singleton: true },
          { id: "dash", label: "Dash" },
        ],
      },
    ] as any;
    vi.mocked(useInstalledPlugins).mockReturnValue(plugins);
    seed("Chat", hsplit(leaf("plugin-0", "plugin", { plugin: { id: "acme", panel: "board", label: "Board" } }), leaf("git", "git")));
    const { rerender } = renderLayout();
    rerender({});
    // Without replacing, acme/board is marked disabled
    expect(
      ws()
        .pluginPanes()
        ?.map((p) => [p.key, p.disabled]),
    ).toEqual([
      ["acme/board", true],
      ["acme/dash", false],
    ]);
    // Replacing plugin-0 frees acme/board
    expect(
      ws()
        .pluginPanes("plugin-0")
        ?.map((p) => [p.key, p.disabled]),
    ).toEqual([
      ["acme/board", false],
      ["acme/dash", false],
    ]);

    // On canvas
    seed("Board", canvasOf({ id: "p1", panel: "plugin", plugin: { id: "acme", panel: "board", label: "Board" } }));
    act(() => ws().setTab("Board"));
    expect(
      ws()
        .pluginPanes()
        ?.map((p) => [p.key, p.disabled]),
    ).toEqual([
      ["acme/board", true],
      ["acme/dash", false],
    ]);
    expect(
      ws()
        .pluginPanes("p1")
        ?.map((p) => [p.key, p.disabled]),
    ).toEqual([
      ["acme/board", false],
      ["acme/dash", false],
    ]);
  });

  it("adds, replaces, and removes panes on split layouts", () => {
    const { ref } = renderLayout();
    act(() => ref.current!.switchToLayout("Ghost"));
    expect(ws().view().panes).toEqual([]);

    // Adding plugin without plugin option throws
    expect(() => ws().addPane("plugin", undefined, "horizontal", false)).toThrow(/needs the plugin panel/);

    // Add editor to empty Ghost tab -> creates editor + file-tree and adds Ghost to layoutNames
    let id = "";
    act(() => {
      id = ws().addPane("editor", undefined, "horizontal", false);
    });
    expect(id).toBe("editor");
    expect(splitPanels()).toEqual(["file-tree", "editor"]);
    expect(tabNames()).toContain("Ghost");

    // Adding duplicate singleton throws
    expect(() => ws().addPane("editor", undefined, "horizontal", false)).toThrow(/can't have another editor/);

    // Add playground with stored playground option beside editor
    act(() => {
      id = ws().addPane("playground", "editor", "horizontal", false, { playground: { name: "demo", scope: "global" } });
    });
    expect(JSON.parse(localStorage.getItem(`playground-active:${CH}:${id}`)!)).toEqual({ name: "demo", scope: "global" });

    // Add pane with missing nextTo throws
    expect(() => ws().addPane("notes", "missing-id", "horizontal", false)).toThrow(/no pane "missing-id"/);

    // Add pane without nextTo
    act(() => {
      id = ws().addPane("notes", undefined, "vertical", true);
    });
    expect(splitPanels()).toContain("notes");

    // Replace pane: duplicate singleton throws
    expect(() => ws().replacePane("notes", "editor")).toThrow(/can't have another editor/);
    // Replace pane: missing id throws
    expect(() => ws().replacePane("missing", "git")).toThrow(/no pane "missing"/);
    // Replace pane: valid, including clearing maximizedLeafId
    act(() => ws().maximize("notes"));
    act(() => {
      id = ws().replacePane("notes", "docker-agent", { openMode: "fresh" });
    });
    expect(ws().view().maximized).toBeUndefined();
    expect(
      ws()
        .view()
        .panes.find((p) => p.id === id)?.open_mode,
    ).toBe("fresh");

    // Replace with plugin
    const plugin = { id: "acme", panel: "board", label: "Board" };
    act(() => {
      id = ws().replacePane(id, "plugin", { plugin });
    });
    expect(
      ws()
        .view()
        .panes.find((p) => p.id === id)?.plugin,
    ).toBe("acme/board");

    // Remove panes down to last leaf
    for (const p of ws().view().panes) {
      act(() => ws().removePane(p.id));
    }
    expect(ws().view().panes).toEqual([]);
    expect(() => ws().removePane("nope")).toThrow(/no pane "nope"/);
  });

  it("adds, replaces, maximizes, and removes tiles on canvas layouts", () => {
    seed("Board", canvasOf({ id: "git", panel: "git" }));
    renderLayout();

    // Add tile without nextTo, with playground option
    let pgId = "";
    act(() => {
      pgId = ws().addPane("playground", undefined, "horizontal", false, { playground: { name: "g", scope: "project" } });
    });
    expect(JSON.parse(localStorage.getItem(`playground-active:${CH}:${pgId}`)!)).toEqual({ name: "g", scope: "project" });

    // Add tile with nextTo and openMode
    let agentId = "";
    act(() => {
      agentId = ws().addPane("docker-agent", "git", "vertical", true, { openMode: "fresh" });
    });
    expect(
      ws()
        .view()
        .panes.find((p) => p.id === agentId)?.open_mode,
    ).toBe("fresh");

    // Add plugin tile
    let plugId = "";
    act(() => {
      plugId = ws().addPane("plugin", undefined, "horizontal", false, { plugin: { id: "acme", panel: "b", label: "B" } });
    });
    expect(
      ws()
        .view()
        .panes.find((p) => p.id === plugId)?.plugin,
    ).toBe("acme/b");

    // Error cases on canvas
    expect(() => ws().addPane("plugin", undefined, "horizontal", false)).toThrow(/needs the plugin panel/);
    expect(() => ws().addPane("git", undefined, "horizontal", false)).toThrow(/can't have another git/);
    expect(() => ws().addPane("notes", "no-tile", "horizontal", false)).toThrow(/no pane "no-tile"/);
    expect(() => ws().maximize("no-tile")).toThrow(/no pane "no-tile"/);

    // Maximize tile then replace it (unmaximizes first)
    act(() => ws().maximize(agentId));
    expect(ws().view().maximized).toBe(agentId);
    let replacedId = "";
    act(() => {
      replacedId = ws().replacePane(agentId, "notes");
    });
    expect(ws().view().maximized).toBeUndefined();
    expect(
      ws()
        .view()
        .panes.some((p) => p.id === replacedId && p.panel === "notes"),
    ).toBe(true);

    // Replace with duplicate singleton throws
    expect(() => ws().replacePane(replacedId, "git")).toThrow(/can't have another git/);

    // Maximize and remove tile (unmaximizes first)
    act(() => ws().maximize(replacedId));
    act(() => ws().removePane(replacedId));
    expect(
      ws()
        .view()
        .panes.some((p) => p.id === replacedId),
    ).toBe(false);
    expect(ws().view().maximized).toBeUndefined();

    // Canvas controlRef null throws on maximize
    act(() => {
      last(CanvasLayout).controlRef.current = null;
    });
    expect(() => ws().maximize("git")).toThrow(/canvas isn't shown yet/);
  });
});

describe("WorkspaceLayout cross-component events (open-panel and open-file)", () => {
  it("handles loop:open-panel on split layouts: rival, existing (minimized/maximized), empty, anchor, fallback", async () => {
    const user = userEvent.setup();
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("docker-browser", "docker-browser"), leaf("git", "git")));
    renderLayout();

    // Ignored when missing detail or wrong channelId
    openPanel(null);
    openPanel({ channelId: "other", panel: "notes" });
    expect(splitPanels()).toEqual(["chat", "docker-browser", "git"]);

    // Rival panel replaces rival (host-browser replaces docker-browser)
    openPanel({ channelId: CH, panel: "host-browser" });
    expect(splitPanels()).toEqual(["chat", "host-browser", "git"]);

    // Existing panel that is minimized gets unminimized; if another panel is maximized (with component), restores
    act(() => last(SplitPaneLayout).onToggleMinimize("git"));
    expect(screen.queryByTestId("leaf-git")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Show component" }));
    expect(screen.getByTestId("component-full")).toBeTruthy();
    openPanel({ channelId: CH, panel: "git" });
    expect(screen.getByTestId("leaf-git")).toBeTruthy();
    expect(screen.queryByTestId("component-full")).toBeNull();

    // Existing panel that is neither minimized nor obscured by maximize is a no-op
    openPanel({ channelId: CH, panel: "git" });
    expect(splitPanels()).toEqual(["chat", "host-browser", "git"]);

    // AnchorPanel inserts opposite horizontal
    openPanel({ channelId: CH, panel: "notes", anchorPanel: "git" });
    expect(splitPanels()).toContain("notes");

    // Without anchorPanel falls back to vertical split on first leaf
    openPanel({ channelId: CH, panel: "tasks" });
    expect(splitPanels()).toContain("tasks");

    // Empty layout uses handleEmptyAdd
    act(() => ws().setTab("Ghost"));
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
    openPanel({ channelId: CH, panel: "audit" });
    expect(splitPanels()).toEqual(["audit"]);
  });

  it("handles loop:open-panel on canvas layouts: rival and new tile, including blank canvas", () => {
    seed("Board", canvasOf({ id: "docker-browser", panel: "docker-browser" }, { id: "chat", panel: "chat" }));
    renderLayout();
    openPanel({ channelId: CH, panel: "host-browser" });
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.panel)).toEqual(["host-browser", "chat"]);

    openPanel({ channelId: CH, panel: "notes", anchorPanel: "chat" });
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.panel)).toContain("notes");

    // Blank canvas (canvasState === null)
    saveLayoutType(CH, "BlankC", "canvas");
    act(() => ws().setTab("BlankC"));
    openPanel({ channelId: CH, panel: "git" });
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.panel)).toEqual(["git"]);
  });

  it("handles loop:open-file on split, empty, canvas, and from learnChannelId", () => {
    const h = setHooks({ learn: { learnChannelId: "learn-ch", running: false, proposals: [], shortcutsVersion: 0 } });
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("git", "git")));
    renderLayout();

    // Ignored when missing detail or wrong channelId
    openFile(null);
    openFile({ channelId: "other", target: { rootIndex: 0, relPath: "a.ts" }, line: 1 });
    expect(h.editor.openFileAtLine).not.toHaveBeenCalled();

    // Opens editor anchored to chat (without file-tree) and calls openFileAtLine
    openFile({ channelId: CH, target: { rootIndex: 0, relPath: "a.ts" }, line: 10, pin: true });
    expect(splitPanels()).toContain("editor");
    expect(splitPanels()).not.toContain("file-tree");
    expect(h.editor.openFileAtLine).toHaveBeenCalledWith("0:a.ts", 10, true);

    // Already has editor: does not add another, also accepts learnChannelId
    openFile({ channelId: "learn-ch", target: { rootIndex: 0, relPath: "b.ts" }, line: 5 });
    expect(h.editor.openFileAtLine).toHaveBeenCalledWith("0:b.ts", 5, undefined);

    // Split without chat anchors to first leaf
    seed("OnlyGit", leaf("git", "git"));
    act(() => ws().setTab("OnlyGit"));
    openFile({ channelId: CH, target: { rootIndex: 0, relPath: "c.ts" }, line: 2 });
    expect(splitPanels()).toEqual(["git", "editor"]);

    // Empty split tab uses handleEmptyAdd("editor")
    act(() => ws().setTab("Ghost"));
    openFile({ channelId: CH, target: { rootIndex: 0, relPath: "d.ts" }, line: 3 });
    expect(splitPanels()).toEqual(["file-tree", "editor"]);

    // Canvas tab adds editor tile
    saveLayoutType(CH, "BlankC", "canvas");
    act(() => ws().setTab("BlankC"));
    openFile({ channelId: CH, target: { rootIndex: 0, relPath: "e.ts" }, line: 4 });
    expect(last(CanvasLayout).canvas.tiles.map((t: any) => t.panel)).toContain("editor");
  });
});

describe("WorkspaceLayout side views (Learn, Explain, Shell, Edits) and SideChat views", () => {
  it("opens, switches, reads, and closes side views in overlay mode and handles Escape", async () => {
    const user = userEvent.setup();
    (SVGElement.prototype as any).getTotalLength = () => 100;
    (SVGElement.prototype as any).getPointAtLength = () => ({ x: 0, y: 0 });
    setHooks({
      learn: { learnChannelId: "learn-ch", running: true, proposals: [{ id: "p1", title: "Tip", status: "pending" }], shortcutsVersion: 1 },
      explain: { explanations: [{ turnId: "t1", status: "running", text: "" }] },
      edits: { editChannelId: "edits-ch", running: true },
    });
    vi.mocked(useContainerStats).mockReturnValue({ agent: { cpu_percent: 1 } } as any);
    const unregisterChat = registerChatInput(CH, "side", { send: vi.fn(), messages: () => [{ id: 1, author: "bot", bot: true, content: "hello" }], busy: () => false, blocked: () => null });
    seed("Chat", leaf("chat", "chat"));
    const { rerender } = renderLayout();

    // Side reader checks
    const reader = getSideReader(CH)!;
    expect(reader.explaining()).toBe(true);
    expect(typeof reader.read("explain", 10)).toBe("string");
    expect(typeof reader.read("edits", 10)).toBe("string");
    expect(typeof reader.read("learn", 10)).toBe("string");
    unregisterChat();
    // Without registered chat input, edits and learn still read cleanly
    expect(typeof reader.read("edits", 10)).toBe("string");
    expect(typeof reader.read("learn", 10)).toBe("string");

    // Open Learn via badge -> focuses composer, shows LearnPane
    const composerFocus = vi.spyOn(screen.getByLabelText("Composer"), "focus");
    await user.click(screen.getByRole("button", { name: "Learn chat" }));
    expect(screen.getByTestId("learn-pane")).toBeTruthy();
    expect(composerFocus).toHaveBeenCalled();

    // Switch to Explain, Edits, Shell via overlay badges
    await user.click(screen.getByRole("button", { name: "Explain learn-split" }));
    expect(screen.getByTestId("explain-pane")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Edits learn-split" }));
    expect(screen.getByTestId("edits-pane")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Shell learn-split" }));
    expect(screen.getByTestId("shell-pane")).toBeTruthy();

    // Clicking active Shell badge closes it
    await user.click(screen.getByRole("button", { name: "Shell learn-split" }));
    expect(ws().view().side).toBeUndefined();

    // Trigger via useExplain and useLearn onShow callbacks
    const onShowExplain = vi.mocked(useExplain).mock.lastCall![3]!;
    act(() => onShowExplain());
    expect(ws().view().side).toBe("explain");
    // Toggle active Explain badge closes it
    await user.click(screen.getByRole("button", { name: "Explain learn-split" }));
    expect(ws().view().side).toBeUndefined();

    const onShowLearn = vi.mocked(useLearn).mock.lastCall![4]!;
    act(() => onShowLearn());
    expect(ws().view().side).toBe("learn");
    // Focus inside learn-pane, then close via Learn badge -> restores focus to composer
    composerFocus.mockClear();
    screen.getByRole("button", { name: "Apply" }).focus();
    await user.click(screen.getByRole("button", { name: "Learn learn-split" }));
    expect(composerFocus).toHaveBeenCalled();

    // Escape key behavior: non-Escape ignored, defaultPrevented ignored, hidden ignored, plain Escape closes
    await user.click(screen.getByRole("button", { name: "Edits chat" }));
    expect(ws().view().side).toBe("edits");
    fireEvent.keyDown(window, { key: "Enter" });
    await flush(5);
    expect(ws().view().side).toBe("edits");

    const prevent = (e: KeyboardEvent) => e.preventDefault();
    window.addEventListener("keydown", prevent);
    fireEvent.keyDown(window, { key: "Escape" });
    await flush(5);
    expect(ws().view().side).toBe("edits");
    window.removeEventListener("keydown", prevent);

    rerender({ style: { display: "none" } });
    fireEvent.keyDown(window, { key: "Escape" });
    await flush(5);
    expect(ws().view().side).toBe("edits");

    rerender({ style: {} });
    fireEvent.keyDown(window, { key: "Escape" });
    await flush(5);
    expect(ws().view().side).toBeUndefined();

    // Toggle active Edits badge closes it too
    await user.click(screen.getByRole("button", { name: "Edits chat" }));
    await user.click(screen.getByRole("button", { name: "Edits learn-split" }));
    expect(ws().view().side).toBeUndefined();
  });

  it("docks side views on a canvas and closes them when the chat tile is removed", async () => {
    const user = userEvent.setup();
    vi.mocked(useContainerStats).mockReturnValue({ agent: { cpu_percent: 1 } } as any);
    seed("Board", canvasOf({ id: "chat", panel: "chat" }, { id: "git", panel: "git" }));
    renderLayout();

    // Open each view docked and toggle closed via badge
    for (const [name, view] of [
      ["Explain", "explain"],
      ["Learn", "learn"],
      ["Edits", "edits"],
      ["Shell", "shell"],
    ] as const) {
      await user.click(screen.getByRole("button", { name: `${name} chat` }));
      expect(screen.getByTestId("learn-dock").dataset.view).toBe(view);
      await user.click(screen.getByRole("button", { name: `${name} chat` }));
      expect(ws().view().side).toBeUndefined();
    }

    // Open docked, then remove chat tile -> learnOpen closes
    await user.click(screen.getByRole("button", { name: "Learn chat" }));
    expect(ws().view().side).toBe("learn");
    act(() => ws().removePane("chat"));
    expect(ws().view().side).toBeUndefined();
  });

  it("manages SideChatPane side views in overlay and docked modes, mutual exclusion, and Escape", async () => {
    const makeHost = () => {
      const el = document.createElement("div");
      const ta = document.createElement("textarea");
      el.appendChild(ta);
      document.body.appendChild(el);
      return el;
    };
    const host1 = makeHost();
    const pane1 = document.createElement("div");
    const host2 = makeHost();
    const pane2 = document.createElement("div");
    const close1 = vi.fn();
    const close2 = vi.fn();

    const h = setHooks();
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("sc-1", "chat-side", { openMode: "fork" }), leaf("sc-2", "chat-side", { openMode: "fresh" })));
    renderLayout();

    const sc1 = () => lastFor(SideChatPane, (p) => p.leafId === "sc-1");
    const sc2 = () => lastFor(SideChatPane, (p) => p.leafId === "sc-2");

    // Reporting null when not present is a no-op
    act(() => sc1().onSideView("sc-1", null));

    const view1: SideChatView = {
      open: true,
      sideChannelId: "side-1",
      learnChannelId: "side-learn-1",
      view: "learn",
      running: false,
      openMode: "fork",
      chatHost: host1,
      paneHost: pane1,
      close: close1,
    };
    const view2: SideChatView = { open: true, sideChannelId: "side-2", learnChannelId: "", view: "explain", running: true, openMode: "fresh", chatHost: host2, paneHost: pane2, close: close2 };

    // Open sc-1 side view -> renders side-chat-split overlay and focuses its textarea
    const focusSpy = vi.spyOn(host1.querySelector("textarea")!, "focus");
    act(() => sc1().onSideView("sc-1", view1));
    expect(screen.getByTestId("side-chat-split")).toBeTruthy();
    expect(sc1().sideViewPlace).toBe("overlay");
    expect(focusSpy).toHaveBeenCalled();

    // Escape closes sc-1 side view (and non-Escape / defaultPrevented ignored)
    fireEvent.keyDown(window, { key: "a" });
    await flush(5);
    expect(close1).not.toHaveBeenCalled();

    const prevent = (e: KeyboardEvent) => e.preventDefault();
    window.addEventListener("keydown", prevent);
    fireEvent.keyDown(window, { key: "Escape" });
    await flush(5);
    expect(close1).not.toHaveBeenCalled();
    window.removeEventListener("keydown", prevent);

    fireEvent.keyDown(window, { key: "Escape" });
    await flush(5);
    expect(close1).toHaveBeenCalledTimes(1);

    // Opening sc-2 while sc-1 is open closes sc-1
    act(() => sc2().onSideView("sc-2", view2));
    // sc-1 is still first open key; close sc-1 so sc-2 becomes active and check mutual exclusion
    act(() => sc1().onSideView("sc-1", { ...view1, open: false }));
    expect(sc2().sideViewPlace).toBe("overlay");

    // Opening main chat's Learn view closes open side chat views
    act(() => ws().openSide("learn"));
    expect(close2).toHaveBeenCalledTimes(1);

    // A side chat's file links open in the channel's editor, its Learn
    // view's too, closing its view when it's up.
    openFile({ channelId: "side-1", target: { rootIndex: 1, relPath: "s.ts" }, line: 6 });
    expect(h.editor.openFileAtLine).toHaveBeenLastCalledWith("1:s.ts", 6, undefined);
    openFile({ channelId: "side-learn-1", target: { rootIndex: 0, relPath: "l.ts" }, line: 7 });
    expect(h.editor.openFileAtLine).toHaveBeenLastCalledWith("0:l.ts", 7, undefined);
    expect(close1).toHaveBeenCalledTimes(1);
    openFile({ channelId: "side-2", target: { rootIndex: 0, relPath: "t.ts" }, line: 8 });
    expect(h.editor.openFileAtLine).toHaveBeenLastCalledWith("0:t.ts", 8, undefined);
    expect(close2).toHaveBeenCalledTimes(2);

    // Clearing sc-1 and sc-2
    act(() => sc1().onSideView("sc-1", null));
    act(() => sc2().onSideView("sc-2", null));

    // Docked mode on canvas
    seed("Board", canvasOf({ id: "sc-3", panel: "chat-side", openMode: "fresh" }));
    act(() => ws().setTab("Board"));
    const sc3 = lastFor(SideChatPane, (p) => p.leafId === "sc-3");
    act(() => sc3.onSideView("sc-3", { open: true, sideChannelId: "side-3", learnChannelId: "", view: "explain", running: false, openMode: "fresh", chatHost: host1, paneHost: pane1, close: close1 }));
    expect(screen.getByTestId("learn-dock").dataset.tile).toBe("sc-3");
    expect(lastFor(SideChatPane, (p) => p.leafId === "sc-3").sideViewPlace).toBe("dock");

    host1.remove();
    host2.remove();
  });
});

describe("WorkspaceLayout renderLeaf panel coverage and plugin portal lifecycle", () => {
  it("renders every panel type and wires their callbacks", () => {
    const h = setHooks({
      chatState: { gateApprovals: { "terminal:docker-agent-0": { id: "g1" } }, clearGateApproval: vi.fn() },
    });
    const allLeaves: PaneNode[] = [
      leaf("chat", "chat"),
      leaf("editor", "editor"),
      leaf("file-tree", "file-tree"),
      leaf("memory", "memory"),
      leaf("git", "git"),
      leaf("docker-agent-0", "docker-agent"),
      leaf("docker-shell-1", "docker-shell"),
      leaf("docker-browser", "docker-browser"),
      leaf("host-browser", "host-browser"),
      leaf("sessions", "sessions"),
      leaf("playground-2", "playground"),
      leaf("notes", "notes"),
      leaf("tasks", "tasks"),
      leaf("kanban", "kanban"),
      leaf("workflows", "workflows"),
      leaf("audit", "audit"),
      leaf("quality", "quality"),
      leaf("review", "review"),
      leaf("plugin-3", "plugin", { plugin: { id: "acme", panel: "board", label: "Board" } }),
      leaf("unknown-4", "unknown" as any),
    ];
    seed("All", { type: "split", direction: "horizontal", flex: 1, children: allLeaves });
    const { unmount } = renderLayout();

    expect(screen.getByTestId("chat-view")).toBeTruthy();
    expect(screen.getByTestId("editor-panel")).toBeTruthy();
    expect(screen.getByTestId("file-tree-panel")).toBeTruthy();
    expect(screen.getByTestId("memory-panel")).toBeTruthy();
    expect(screen.getByTestId("git-panel")).toBeTruthy();
    expect(screen.getByTestId("terminal-docker-agent-0")).toBeTruthy();
    expect(screen.getByTestId("terminal-docker-shell-1")).toBeTruthy();
    expect(screen.getByTestId("browser-docker")).toBeTruthy();
    expect(screen.getByTestId("browser-host")).toBeTruthy();
    expect(screen.getByTestId("sessions-panel")).toBeTruthy();
    expect(screen.getByTestId("playground-playground-2")).toBeTruthy();
    expect(screen.getByTestId("notes-panel")).toBeTruthy();
    expect(screen.getByTestId("tasks-panel")).toBeTruthy();
    expect(screen.getByTestId("kanban-panel")).toBeTruthy();
    expect(screen.getByTestId("workflows-panel")).toBeTruthy();
    expect(screen.getByTestId("audit-panel")).toBeTruthy();
    expect(screen.getByTestId("quality-panel")).toBeTruthy();
    expect(screen.getByTestId("review-panel")).toBeTruthy();
    expect(screen.getByTestId("plugin-panel-plugin-3")).toBeTruthy();
    expect(screen.getByTestId("leaf-unknown-4").children).toHaveLength(0);

    // Exercise callbacks on rendered panels
    expect(last(ChatView)).toBeTruthy();
    act(() => lastFor(Terminal, (p) => p.instanceId === "docker-agent-0").onGateApprovalResolved());
    expect(h.chatState.clearGateApproval).toHaveBeenCalledWith("terminal:docker-agent-0");
    act(() => lastFor(Terminal, (p) => p.instanceId === "docker-shell-1").onPaneStatus("running"));
    expect(last(ReviewPanel).hasChatPanel).toBe(true);
    act(() => last(ReviewPanel).onClearGateApproval("review:1"));
    expect(h.chatState.clearGateApproval).toHaveBeenCalledWith("review:1");

    // Exercise onClose on Editor, FileTree, Memory, Git, Quality
    act(() => last(EditorPanel).onClose());
    act(() => last(FileTreePanel).onClose());
    act(() => last(MemoryPanel).onClose());
    act(() => last(GitPanel).onClose());
    act(() => last(QualityPanel).onClose());
    expect(splitPanels()).not.toContain("editor");
    expect(splitPanels()).not.toContain("file-tree");
    expect(splitPanels()).not.toContain("memory");
    expect(splitPanels()).not.toContain("git");
    expect(splitPanels()).not.toContain("quality");

    // Removing plugin leaf cleans up its host; unmount cleans up any remaining hosts
    act(() => ws().addPane("plugin", undefined, "horizontal", false, { plugin: { id: "acme", panel: "dash", label: "Dash" } }));
    act(() => ws().removePane("plugin-3"));
    expect(screen.queryByTestId("plugin-panel-plugin-3")).toBeNull();
    unmount();
  });

  it("covers edge branches: empty initial mounts, reduced motion, islands=false, canvas status/review/reset, stale updaters, and mutual side-chat close", async () => {
    const user = userEvent.setup();

    // 1. Initial mount with empty split layout + islands=false + reduced motion + sweep with empty storage
    const origMatchMedia = window.matchMedia;
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as any;
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({
        [CH]: { active: "EmptyS", order: ["EmptyS", "EmptyC"], layouts: {}, types: { EmptyC: "canvas" }, version: 13 },
      }),
    );
    const { unmount: unmount1 } = render(
      <ThemeProvider initialIslands={false}>
        <WorkspaceLayout {...baseProps()} />
      </ThemeProvider>,
    );
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
    // Call sweep callback after clearing storage -> hits `?? {}`
    const sweepCb = vi.mocked(sideChats.sweep).mock.lastCall![1];
    localStorage.clear();
    expect(sweepCb()).toEqual(new Set());
    unmount1();

    // 2. Initial mount with empty canvas layout + canvasNow / onCanvasChange / maximize(null) / handleDeleteLayout to empty canvas
    const allDefaults = ["Chat", "Editor", "Memory", "Terminal", "Git", "Browser Chat", "Sessions", "Swarm", "Canvas", "Playground", "Kanban", "Workflows", "Review"];
    localStorage.setItem(
      "loop-workspace-layout",
      JSON.stringify({
        [CH]: { active: "EmptyC", order: ["EmptyC", "EmptyC2"], layouts: {}, types: { EmptyC: "canvas", EmptyC2: "canvas" }, version: 13, removed: allDefaults },
      }),
    );
    const { unmount: unmount2 } = renderLayout();
    expect(ws().view().panes).toEqual([]);
    // onCanvasChange while canvasRef.current is still null
    act(() => last(CanvasLayout).onCanvasChange(canvasOf({ id: "git", panel: "git" })));
    expect(last(CanvasLayout).canvas.tiles).toHaveLength(1);
    act(() => ws().maximize(null));
    // Delete EmptyC -> switches to EmptyC2 (canvas with no saved layout)
    act(() => ws().removeTab("EmptyC"));
    expect(ws().view().tab).toBe("EmptyC2");
    expect(ws().view().panes).toEqual([]);
    unmount2();
    window.matchMedia = origMatchMedia;

    // 3. Canvas with terminal (computeAgentState with !treeRef.current) and ReviewPanel (hasChatPanel=false)
    localStorage.clear();
    seed("Board", canvasOf({ id: "host-shell-1", panel: "host-shell" }, { id: "review", panel: "review" }));
    const { unmount: unmount3 } = renderLayout();
    act(() => lastFor(Terminal, (p) => p.instanceId === "host-shell-1").onPaneStatus("running"));
    expect(last(ReviewPanel).hasChatPanel).toBe(false);
    unmount3();

    // 4. Reset Canvas default when canvasRef.current is null + hover Restore defaults
    localStorage.clear();
    const { unmount: unmount4 } = renderLayout();
    // Delete Canvas from saved layouts after mount so switching to it sets canvasState = null
    const stored4 = JSON.parse(localStorage.getItem("loop-workspace-layout")!);
    delete stored4[CH].layouts.Canvas;
    stored4[CH].removed = ["Memory"];
    stored4[CH].order = stored4[CH].order.filter((n: string) => n !== "Memory");
    localStorage.setItem("loop-workspace-layout", JSON.stringify(stored4));
    act(() => ws().removeTab("Memory"));
    act(() => ws().setTab("Canvas"));
    await user.click(screen.getByTitle("Layout options"));
    const restoreBtn = screen.getByRole("button", { name: "Restore defaults" });
    fireEvent.mouseEnter(restoreBtn);
    fireEvent.mouseLeave(restoreBtn);
    expect(restoreBtn.style.backgroundColor).toBe("transparent");
    await user.click(screen.getByRole("button", { name: "Reset current" }));
    expect(last(CanvasLayout).canvas.tiles).toHaveLength(6);
    unmount4();

    // 5. EmptyLayoutPicker onAdd & ws().addPane when activeName is ALREADY in layoutNames
    localStorage.clear();
    const { unmount: unmount5 } = renderLayout();
    act(() => ws().createTab("PreNamed1", false));
    act(() => last(EmptyLayoutPicker).onAdd("notes"));
    expect(splitPanels()).toEqual(["notes"]);
    act(() => ws().createTab("PreNamed2", false));
    act(() => {
      ws().addPane("notes", undefined, "horizontal", false);
    });
    expect(splitPanels()).toEqual(["notes"]);
    unmount5();

    // 6. Removing & replacing a pane that is maximized and has shownComponent
    localStorage.clear();
    seed("Chat", hsplit(leaf("chat", "chat"), leaf("git", "git")));
    const { unmount: unmount6 } = renderLayout();
    await user.click(screen.getByRole("button", { name: "Show component" }));
    act(() => ws().removePane("chat"));
    expect(screen.queryByTestId("component-full")).toBeNull();
    seed("Chat2", hsplit(leaf("chat", "chat"), leaf("git", "git")));
    act(() => ws().setTab("Chat2"));
    await user.click(screen.getByRole("button", { name: "Show component" }));
    act(() => {
      ws().replacePane("chat", "notes");
    });
    expect(screen.queryByTestId("component-full")).toBeNull();
    unmount6();

    // 7. SideChat mutual exclusion when an earlier key opens while a later key is open
    localStorage.clear();
    const hostA = document.createElement("div");
    const paneA = document.createElement("div");
    const closeA = vi.fn();
    const closeB = vi.fn();
    seed("Chat", hsplit(leaf("sc-1", "chat-side"), leaf("sc-2", "chat-side")));
    const { unmount: unmount7 } = renderLayout();
    const sc1 = () => lastFor(SideChatPane, (p) => p.leafId === "sc-1");
    const sc2 = () => lastFor(SideChatPane, (p) => p.leafId === "sc-2");
    act(() => sc1().onSideView("sc-1", { open: false, view: "learn", running: false, openMode: "fork", chatHost: hostA, paneHost: paneA, close: closeA }));
    act(() => sc2().onSideView("sc-2", { open: true, view: "explain", running: false, openMode: "fork", chatHost: hostA, paneHost: paneA, close: closeB }));
    act(() => sc1().onSideView("sc-1", { open: true, view: "learn", running: false, openMode: "fork", chatHost: hostA, paneHost: paneA, close: closeA }));
    expect(closeB).toHaveBeenCalledTimes(1);
    unmount7();

    // 8. Stale tree callbacks & batched setTree updaters receiving prev === null
    localStorage.clear();
    seed("One", leaf("docker-browser", "docker-browser"));
    const { unmount: unmount8 } = renderLayout();
    const staleDrop = last(SplitPaneLayout).onDrop;
    const staleFlex = last(SplitPaneLayout).onUpdateFlex;
    const staleSplit = last(SplitPaneLayout).onSplitLeaf;
    const staleRemove = last(SplitPaneLayout).onRemoveLeaf;
    // Batched remove + open-panel (rival, anchor) + open-file + second remove all see prev === null in setTree updaters
    act(() => {
      staleRemove("docker-browser");
      staleRemove("docker-browser");
      window.dispatchEvent(new CustomEvent("loop:open-panel", { detail: { channelId: CH, panel: "host-browser" } }));
      window.dispatchEvent(new CustomEvent("loop:open-panel", { detail: { channelId: CH, panel: "notes", anchorPanel: "docker-browser" } }));
      window.dispatchEvent(new CustomEvent("loop:open-file", { detail: { channelId: CH, target: { rootIndex: 0, relPath: "x.ts" }, line: 1 } }));
    });
    expect(screen.getByTestId("empty-picker")).toBeTruthy();
    // Stale callbacks after tree became null
    act(() => {
      staleDrop("a", "b", "center");
      staleFlex([], 0, 1, 1);
      staleSplit("a", "notes", "horizontal");
    });
    unmount8();

    // 9. Degenerate empty split tree (0 leaves) for open-panel / open-file guards
    localStorage.clear();
    seed("EmptySplit", { type: "split", direction: "horizontal", flex: 1, children: [] });
    const { unmount: unmount9 } = renderLayout();
    openPanel({ channelId: CH, panel: "notes" });
    openFile({ channelId: CH, target: { rootIndex: 0, relPath: "z.ts" }, line: 1 });
    expect(splitPanels()).toEqual([]);
    unmount9();
  });
});
