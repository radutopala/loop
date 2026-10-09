import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { killAgentContainer } from "../../api/loopApi";
import { CanvasLayout } from "../../canvas/CanvasLayout";
import { withPanelTile } from "../../canvas/tilePlacement";
import type { CanvasNode } from "../../canvas/types";
import { ShortcutsVersionContext } from "../../hooks/shortcutsVersion";
import { useAgentRegistry } from "../../hooks/useAgentRegistry";
import { useChatState } from "../../hooks/useChatState";
import type { ActiveChatState, ChatEventListener } from "../../hooks/useChatStateStore";
import { useContainerStats } from "../../hooks/useContainerStats";
import { useEditorState } from "../../hooks/useEditorState";
import { ExplainContext, useExplain } from "../../hooks/useExplain";
import { LearnContext, useLearn } from "../../hooks/useLearn";
import { prefersReducedMotion, usePresence } from "../../hooks/usePresence";
import type { LayoutType } from "../../layouts/persistence";
import {
  clearLayout,
  createDefaultLayouts,
  DEFAULT_LAYOUT_NAMES,
  DEFAULT_LAYOUT_TYPES,
  deleteLayout,
  ensureDefaultLayouts,
  loadChannelLayouts,
  renameLayout,
  reorderLayout,
  restoreDefaultLayouts,
  saveActiveLayout,
  saveLayout,
  saveLayoutType,
} from "../../layouts/persistence";
import { EmptyLayoutPicker } from "../../splitPane/AddPanelButton";
import { PaneLeafHeader } from "../../splitPane/PaneLeafHeader";
import { paneBoxStyle, SplitPaneLayout } from "../../splitPane/SplitPaneLayout";
import {
  canAddPanel,
  collectLeaves,
  collectPanelTypes,
  findLastLeaf,
  findLeafById,
  hasAgentLeaf,
  leafCount,
  makeLeaf,
  moveLeaf,
  removeLeaf,
  splitLeaf,
  swapLeavesInTree,
  updateFlex,
} from "../../splitPane/treeOps";
import type { DropPosition, SplitDirection } from "../../splitPane/types";
import { useTheme } from "../../ThemeContext";
import type { ColorPalette } from "../../theme";
import { fonts } from "../../theme";
import type { Channel, SessionStatus } from "../../types";
import type { AgentOpenMode, LeafNode, PanelType, PaneNode } from "../../types/panels";
import { replaceLeaf } from "../../uiBridge/paneOps";
import { getTerminalInput, notifyUiChanged, registerWorkspace } from "../../uiBridge/registry";
import type { PaneOptions, WorkspaceController } from "../../uiBridge/types";
import { storageSetJSON } from "../../utils/storage";
import { ChatComponentFull, ComponentFocusContext, type ShownComponent } from "../chat/ChatComponent";
import { ChatView } from "../chat/ChatView";
import { ExplainBadge } from "../chat/ExplainBadge";
import { explanationPending } from "../chat/explainState";
import type { FileLinkOpenDetail } from "../chat/FileLink";
import { LearnBadge } from "../chat/LearnBadge";
import { AuditPanel } from "../panels/AuditPanel";
import { BrowserPanel } from "../panels/BrowserPanel";
import { makePathKey } from "../panels/EditorFileTree";
import { EditorPanel } from "../panels/EditorPanel";
import { FileTreePanel } from "../panels/FileTreePanel";
import { GitPanel } from "../panels/GitPanel";
import { KanbanPanel } from "../panels/KanbanPanel";
import { MemoryPanel } from "../panels/MemoryPanel";
import { NotesPanel } from "../panels/NotesPanel";
import { PlaygroundPanel } from "../panels/PlaygroundPanel";
import { QualityPanel } from "../panels/QualityPanel";
import { ReviewPanel } from "../panels/ReviewPanel";
import { SessionsPanel } from "../panels/SessionsPanel";
import { TasksPanel } from "../panels/TasksPanel";
import { getCloseForInstance, Terminal } from "../panels/Terminal";
import { WorkflowsLayoutPanel } from "../panels/WorkflowsLayoutPanel";
import { ChannelHeaderInfo } from "./ChannelHeaderInfo";
import { ChatSlot, createChatHost } from "./ChatSlot";
import { ExplainPane } from "./ExplainPane";
import { HeaderBranchPicker } from "./HeaderBranchPicker";
import { LearnPane } from "./LearnPane";
import { LEARN_SPLIT_MS, LEARN_SPLIT_SLOT_ID, LearnSplit, type SideView } from "./LearnSplit";

type AgentState = "running" | "stopped" | "none";

/** Per-storageKey counters so IDs don't collide across channels. */
const idCounters = new Map<string, number>();

function nextId(channelId: string, panel: PanelType): string {
  const key = `layout-${channelId}`;
  const n = idCounters.get(key) ?? 0;
  idCounters.set(key, n + 1);
  return `${panel}-${n}`;
}

function initIdCounter(channelId: string, tree: PaneNode) {
  const key = `layout-${channelId}`;
  const cur = idCounters.get(key) ?? 0;
  let max = 0;
  for (const leaf of collectLeaves(tree)) {
    const parts = leaf.id.split("-");
    const num = parseInt(parts[parts.length - 1] ?? "0", 10);
    if (!isNaN(num) && num > max) max = num;
  }
  // Only increase, never decrease (other layouts may have higher IDs)
  if (max + 1 > cur) idCounters.set(key, max + 1);
}

/** Closes the session of a terminal pane that's going away. */
function closeTerminalSession(channelId: string, leaf: LeafNode) {
  if (leaf.panel !== "docker-agent" && leaf.panel !== "host-shell" && leaf.panel !== "docker-shell") return;
  const target = leaf.panel === "host-shell" ? "host" : "agent";
  getCloseForInstance(`${target}:${channelId}:${leaf.id}`)?.();
}

function leafIdForPanel(channelId: string, panel: PanelType): string {
  if (panel === "chat" || panel === "editor" || panel === "memory" || panel === "git" || panel === "sessions" || panel === "notes" || panel === "audit") {
    return panel;
  }
  return nextId(channelId, panel);
}

/**
 * Insert `newLeaf` on the opposite horizontal side of the leaf identified by
 * `anchorId`. If the anchor's parent is a horizontal split, the new leaf is
 * appended at whichever end is farther from the anchor. Otherwise the anchor
 * is wrapped into a new horizontal split with the new leaf placed to its
 * right (the typical layout when chat is on the left).
 */
function insertOppositeHorizontal(node: PaneNode, anchorId: string, newLeaf: LeafNode): PaneNode {
  if (node.type === "leaf") {
    if (node.id !== anchorId) return node;
    return {
      type: "split",
      direction: "horizontal",
      flex: node.flex,
      children: [
        { ...node, flex: 1 },
        { ...newLeaf, flex: 1 },
      ],
    };
  }
  const directIdx = node.children.findIndex((c) => c.type === "leaf" && c.id === anchorId);
  if (directIdx !== -1) {
    if (node.direction === "horizontal") {
      const half = (node.children.length - 1) / 2;
      const appendRight = directIdx <= half;
      // Match the existing siblings' scale so the new leaf is visible. Using
      // `flex: 1` against siblings whose flex values are 50+ would compress the
      // new pane to ~1% of the container width.
      const totalFlex = node.children.reduce((sum, c) => sum + c.flex, 0);
      const avgFlex = totalFlex / node.children.length;
      const inserted: LeafNode = { ...newLeaf, flex: avgFlex };
      return {
        ...node,
        children: appendRight ? [...node.children, inserted] : [inserted, ...node.children],
      };
    }
    return {
      ...node,
      children: node.children.map((c, i) => {
        if (i !== directIdx) return c;
        return {
          type: "split" as const,
          direction: "horizontal" as const,
          flex: c.flex,
          children: [
            { ...c, flex: 1 },
            { ...newLeaf, flex: 1 },
          ],
        };
      }),
    };
  }
  return { ...node, children: node.children.map((c) => insertOppositeHorizontal(c, anchorId, newLeaf)) };
}

function withDefaultFileTreeForEditor(tree: PaneNode, editorLeafId: string, fileTreeLeafId: string): PaneNode {
  if (!canAddPanel(tree, "file-tree")) return tree;
  const transform = (n: PaneNode): PaneNode => {
    if (n.type === "leaf") {
      if (n.id !== editorLeafId) return n;
      return {
        type: "split",
        direction: "horizontal",
        flex: n.flex,
        children: [
          { type: "leaf", id: fileTreeLeafId, panel: "file-tree", flex: 25 },
          { ...n, flex: 75 },
        ],
      };
    }
    return { ...n, children: n.children.map(transform) };
  };
  return transform(tree);
}

function buildLayoutMenuItemStyle(colors: ColorPalette): React.CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    gap: 6,
    width: "100%",
    padding: "4px 8px",
    border: "none",
    background: "transparent",
    color: colors.textLight,
    fontSize: 11,
    textAlign: "left",
    cursor: "pointer",
    borderRadius: 4,
    fontFamily: fonts.sans,
    whiteSpace: "nowrap",
  };
}

function buildTabButtonStyle(colors: ColorPalette, active: boolean): React.CSSProperties {
  return {
    background: active ? colors.surface : "transparent",
    border: active ? `1px solid ${colors.textLight}` : "1px solid transparent",
    color: active ? colors.textLight : colors.textDim,
    cursor: "pointer",
    padding: "3px 10px",
    minWidth: 70,
    fontSize: 10,
    fontFamily: fonts.mono,
    lineHeight: 1,
    borderRadius: 12,
    display: "flex",
    alignItems: "center",
    gap: 4,
  };
}

export interface WorkspaceLayoutRef {
  switchToLayout: (name: string) => void;
}

interface WorkspaceLayoutProps {
  channelId: string;
  channel: Channel;
  /** Open Settings on the project config waiting for trust. */
  onReviewProjectTrust?: () => void;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
  onOpenPalette: () => void;
  scrollToMessageId?: number | null;
  onScrollComplete?: () => void;
  openMemoryFile?: string | null;
  onOpenMemoryFileComplete?: () => void;
  onStatusChange?: () => void;
  error?: string | null;
  onDismissError?: () => void;
  diffStats?: { add: number; del: number };
  style?: React.CSSProperties;
  parentIsWorktree?: boolean;
  onCreateWorktree?: (channelId: string, branch: string) => Promise<void>;
  onImportWorktree?: (channelId: string, worktreePath: string) => Promise<void>;
  onSelectThread?: (threadId: string) => void;
  /** Restored chat state from the app-level store. */
  initialChatState?: ActiveChatState;
  /** Called on unmount with latest chat state for store persistence (the
   * channel's, or its learn thread's in the Learn view). */
  onChatStateUnmount?: (channelId: string, state: ActiveChatState) => void;
  /** The app-level store's chat state for a channel, to restore the Learn
   * view's learn thread (kept up to date while the view is closed). */
  getChatState?: (channelId: string) => ActiveChatState | undefined;
  /** Subscribe to chat events from the store's single WebSocket. */
  subscribeChatEvents?: (listener: ChatEventListener) => () => void;
  /** Subscribe to one channel's events whether or not it's selected. */
  subscribeChannelEvents?: (channelId: string, listener: ChatEventListener) => () => void;
  /** Bumped on each WS (re)connect, for state to fetch again. */
  wsOpens?: number;
  /**
   * Register a channel as having its Review panel mounted. Drops the
   * pill immediately and blocks the WS / rehydrate path from relighting
   * it until the returned deregister fn is called.
   */
  registerReviewView?: (channelId: string) => () => void;
  /**
   * Tell the store to drop the sidebar's ask-pill for this channel. Wired
   * into useChatState's clearAskUser so the pill clears in lockstep with
   * the card disappearing (the backend doesn't emit an ask.resolved event).
   */
  clearAskUserPill?: (channelId: string) => void;
  clearPlanPill?: (channelId: string) => void;
}

export const WorkspaceLayout = forwardRef<WorkspaceLayoutRef, WorkspaceLayoutProps>(function WorkspaceLayout(
  {
    channelId,
    channel,
    onReviewProjectTrust,
    sidebarOpen,
    onToggleSidebar,
    onOpenPalette,
    scrollToMessageId,
    onScrollComplete,
    openMemoryFile,
    onOpenMemoryFileComplete,
    onStatusChange,
    error,
    onDismissError,
    diffStats: _diffStats,
    style,
    parentIsWorktree,
    onCreateWorktree,
    onImportWorktree,
    onSelectThread,
    initialChatState,
    onChatStateUnmount,
    getChatState,
    subscribeChatEvents,
    subscribeChannelEvents,
    wsOpens,
    registerReviewView,
    clearAskUserPill,
    clearPlanPill,
  },
  ref,
) {
  const { colors } = useTheme();
  const { agents: agentInfoMap } = useAgentRegistry(channelId);
  const containerStats = useContainerStats(channelId);
  const [branchError, setBranchError] = useState<string | null>(null);

  // --- Named layouts state ---
  const [layoutNames, setLayoutNames] = useState<string[]>(() => {
    return ensureDefaultLayouts(channelId).order;
  });
  const [activeName, setActiveName] = useState<string>(() => {
    return ensureDefaultLayouts(channelId).active;
  });
  const [layoutType, setLayoutType] = useState<LayoutType>(() => {
    const ch = ensureDefaultLayouts(channelId);
    return ch.types[ch.active] ?? "split";
  });
  // For the loop:open-panel listener, which outlives layout switches.
  const layoutTypeRef = useRef(layoutType);
  layoutTypeRef.current = layoutType;
  const [tree, setTree] = useState<PaneNode | null>(() => {
    const ch = ensureDefaultLayouts(channelId);
    // Seed the id counter from EVERY split layout (not just the active one) so a
    // newly added panel can never mint a leaf id that collides with one persisted
    // in another tab — leaf ids must stay unique per channel for per-instance
    // panel state (e.g. each playground's selected item) to stay isolated across
    // tabs. Canvas layouts hold a CanvasNode, not a pane tree, so skip them.
    for (const [name, t] of Object.entries(ch.layouts)) {
      if (t && ch.types[name] !== "canvas") initIdCounter(channelId, t as PaneNode);
    }
    const lt = ch.types[ch.active] ?? "split";
    if (lt !== "canvas") {
      return (ch.layouts[ch.active] as PaneNode) ?? null;
    }
    return null;
  });
  const treeRef = useRef(tree);

  const [canvasState, setCanvasState] = useState<CanvasNode | null>(() => {
    const ch = ensureDefaultLayouts(channelId);
    const lt = ch.types[ch.active] ?? "split";
    return lt === "canvas" ? ((ch.layouts[ch.active] as CanvasNode) ?? null) : null;
  });
  treeRef.current = tree;

  // Chat state — hoisted here so the WebSocket + messages survive layout switches.
  // initialChatState restores state from the app-level store on mount;
  // onChatStateUnmount saves state back to the store on unmount.
  const chatStateUnmount = useCallback((state: ActiveChatState) => onChatStateUnmount?.(channelId, state), [channelId, onChatStateUnmount]);
  const chatState = useChatState(channelId, channel.agent_running, {
    initialState: initialChatState,
    onUnmount: chatStateUnmount,
    subscribeChatEvents,
    clearAskUserPill,
    clearPlanPill,
  });

  // The channel's explanations, shown in the Explain view beside the chat
  // (the Learn view's, with the Explain pane) and at the end of the turns.
  // Showing one opens the view (openSide, defined below with the layout's).
  const openSideRef = useRef<(view: SideView) => void>(() => {});
  const showExplain = useCallback(() => openSideRef.current("explain"), []);
  const explain = useExplain(channelId, subscribeChatEvents, wsOpens, showExplain);
  // The channel's learn passes, shown in the Learn view beside the chat (see
  // chatHost) and at the end of the turns they reviewed.
  const showLearn = useCallback(() => openSideRef.current("learn"), []);
  const learn = useLearn(channelId, subscribeChatEvents, subscribeChannelEvents, wsOpens, showLearn);
  const { learnChannelId } = learn;
  // The Learn view is open (learnOpen), with the Learn pane or the Explain
  // one beside the chat (sideView, kept while it closes).
  const [learnOpen, setLearnOpen] = useState(false);
  const [sideView, setSideView] = useState<SideView>("learn");
  // It stays mounted until its closing animation ends.
  const learnMs = prefersReducedMotion() ? 0 : LEARN_SPLIT_MS;
  const learnSplit = usePresence(learnOpen, learnMs);
  const learnSplitRef = useRef<HTMLDivElement>(null);
  // On a canvas the Learn view docks to the chat's tile instead of covering
  // the layout. Set on opening, so a view closing over a layout switch goes
  // the way it came.
  const [learnDocked, setLearnDocked] = useState(false);
  const learnOverlay = learnSplit.mounted && !learnDocked;

  // Editor + file-tree shared state. Hoisted here so both panels (rendered
  // independently inside the layout) stay in sync, and so tab/cursor state
  // survives layout switches.
  const editorState = useEditorState(channelId, {
    tabsStorageKey: "loop-layout-editor-tabs",
    subscribeChatEvents,
  });

  // Track per-pane session status for aggregate agent state.
  const statusMapRef = useRef(new Map<string, SessionStatus>());
  const [agentState, setAgentState] = useState<AgentState>("none");
  const [showLayoutMenu, setShowLayoutMenu] = useState(false);
  const [showNewLayoutMenu, setShowNewLayoutMenu] = useState(false);
  const [maximizedLeafId, setMaximizedLeafId] = useState<string | null>(null);
  // A chat component opened across its pane. The pane is maximized while it
  // shows and restored on close, unless it was maximized already.
  const [shownComponent, setShownComponent] = useState<{ leafId: string; component: ShownComponent; wasMaximized: boolean } | null>(null);
  const maximizedLeafIdRef = useRef(maximizedLeafId);
  maximizedLeafIdRef.current = maximizedLeafId;
  const openComponent = useCallback((leafId: string, component: ShownComponent) => {
    setShownComponent({ leafId, component, wasMaximized: maximizedLeafIdRef.current === leafId });
    setMaximizedLeafId(leafId);
  }, []);
  const closeComponent = useCallback(() => {
    if (shownComponent && !shownComponent.wasMaximized) setMaximizedLeafId(null);
    setShownComponent(null);
  }, [shownComponent]);
  const [minimizedLeaves, setMinimizedLeaves] = useState<Set<string>>(new Set());

  // Close layout menu on outside click.
  useEffect(() => {
    if (!showLayoutMenu && !showNewLayoutMenu) return;
    const handler = () => {
      setShowLayoutMenu(false);
      setShowNewLayoutMenu(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showLayoutMenu, showNewLayoutMenu]);

  const computeAgentState = useCallback((): AgentState => {
    const current = treeRef.current;
    if (!current) return "none";
    const terminalLeaves = collectLeaves(current).filter((l) => l.panel === "docker-agent" || l.panel === "host-shell" || l.panel === "docker-shell");
    if (terminalLeaves.length === 0) return "none";
    const terminalStatuses = [...statusMapRef.current.entries()].filter(([leafId]) => {
      const p = findLeafById(current, leafId)?.panel;
      return p === "docker-agent" || p === "host-shell" || p === "docker-shell";
    });
    if (terminalStatuses.length === 0) return "running"; // terminal leaves exist but no status yet
    const allDead = terminalStatuses.every(([, s]) => s === "completed" || s === "failed");
    return allDead ? "stopped" : "running";
  }, []);

  const handlePaneStatus = useCallback(
    (id: string, status: SessionStatus) => {
      statusMapRef.current.set(id, status);
      setAgentState(computeAgentState());
      notifyUiChanged();
    },
    [computeAgentState],
  );

  // Reload when channelId changes.
  const prevChannelRef = useRef(channelId);
  useEffect(() => {
    if (channelId !== prevChannelRef.current) {
      prevChannelRef.current = channelId;
      statusMapRef.current.clear();
      const ch = ensureDefaultLayouts(channelId);
      setLayoutNames(ch.order);
      const name = ch.active;
      setActiveName(name);
      const lt = ch.types[name] ?? "split";
      const t = ch.layouts[name] ?? null;
      setLayoutType(lt);
      if (lt === "canvas") {
        setCanvasState((t as CanvasNode) ?? null);
        setTree(null);
      } else if (t) {
        initIdCounter(channelId, t as PaneNode);
        setTree(t as PaneNode);
        setCanvasState(null);
      } else {
        setTree(null);
        setCanvasState(null);
      }
      setAgentState("none");
      setMaximizedLeafId(null);
      setShownComponent(null);
    }
  }, [channelId]);

  // Save tree/canvas whenever it changes.
  useEffect(() => {
    if (tree) {
      saveLayout(channelId, activeName, tree);
    } else if (canvasState) {
      saveLayout(channelId, activeName, canvasState as any);
    }
  }, [channelId, activeName, tree, canvasState]);

  // --- Layout tab operations ---
  const switchLayout = useCallback(
    (name: string) => {
      const ch = loadChannelLayouts(channelId);
      const lt = ch?.types[name] ?? DEFAULT_LAYOUT_TYPES[name] ?? "split";
      const t = ch?.layouts[name] ?? null;
      statusMapRef.current.clear();
      // The Learn view holds the chat of the layout it opened over.
      setLearnOpen(false);
      setActiveName(name);
      setLayoutType(lt);
      if (lt === "canvas") {
        setCanvasState((t as CanvasNode) ?? null);
        setTree(null);
      } else if (t) {
        initIdCounter(channelId, t as PaneNode);
        setTree(t as PaneNode);
        setCanvasState(null);
      } else {
        setTree(null);
        setCanvasState(null);
      }
      setAgentState("none");
      setMaximizedLeafId(null);
      setShownComponent(null);
      saveActiveLayout(channelId, name);
    },
    [channelId],
  );

  useImperativeHandle(
    ref,
    () => ({
      switchToLayout: switchLayout,
    }),
    [switchLayout],
  );

  // Auto-switch to a layout with a chat pane when scrollToMessageId is set.
  useEffect(() => {
    if (!scrollToMessageId || !tree) return;
    if (collectLeaves(tree).some((l) => l.panel === "chat")) return;
    const saved = loadChannelLayouts(channelId);
    if (!saved) return;
    for (const name of saved.order) {
      const savedTree = saved.layouts[name];
      if (savedTree && (saved.types[name] ?? "split") !== "canvas" && collectLeaves(savedTree as PaneNode).some((l) => l.panel === "chat")) {
        switchLayout(name);
        return;
      }
    }
  }, [scrollToMessageId, tree, channelId, switchLayout]);

  // Auto-switch to a layout with a memory pane when openMemoryFile is set.
  // The Learn view closes, or the file would open under it.
  useEffect(() => {
    if (!openMemoryFile) return;
    setLearnOpen(false);
    if (!tree) return;
    if (collectLeaves(tree).some((l) => l.panel === "memory")) {
      // Memory pane exists, clear the prop after MemoryPanel consumes it.
      setTimeout(() => onOpenMemoryFileComplete?.(), 0);
      return;
    }
    const saved = loadChannelLayouts(channelId);
    if (!saved) return;
    for (const name of saved.order) {
      const savedTree = saved.layouts[name];
      if (savedTree && (saved.types[name] ?? "split") !== "canvas" && collectLeaves(savedTree as PaneNode).some((l) => l.panel === "memory")) {
        switchLayout(name);
        setTimeout(() => onOpenMemoryFileComplete?.(), 0);
        return;
      }
    }
  }, [openMemoryFile, tree, channelId, switchLayout, onOpenMemoryFileComplete]);

  const addLayout = useCallback(
    (lt: LayoutType) => {
      let n = 1;
      let name = lt === "canvas" ? `Canvas ${n}` : `Layout ${n}`;
      while (layoutNames.includes(name)) {
        n++;
        name = lt === "canvas" ? `Canvas ${n}` : `Layout ${n}`;
      }
      setLayoutNames((prev) => [...prev, name]);
      // The Learn view holds the chat of the layout it opened over.
      setLearnOpen(false);
      setActiveName(name);
      setLayoutType(lt);
      if (lt === "canvas") {
        setCanvasState({ type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] });
        setTree(null);
      } else {
        setTree(null);
        setCanvasState(null);
      }
      saveLayoutType(channelId, name, lt);
      statusMapRef.current.clear();
      setAgentState("none");
      setShowNewLayoutMenu(false);
    },
    [channelId, layoutNames],
  );

  const handleRenameLayout = useCallback(
    (oldName: string, newName: string) => {
      const trimmed = newName.trim();
      if (!trimmed || trimmed === oldName || layoutNames.includes(trimmed)) return;
      renameLayout(channelId, oldName, trimmed);
      setLayoutNames((prev) => prev.map((n) => (n === oldName ? trimmed : n)));
      if (activeName === oldName) setActiveName(trimmed);
    },
    [channelId, layoutNames, activeName],
  );

  const handleDeleteLayout = useCallback(
    (name: string) => {
      if (layoutNames.length <= 1) return;
      // Kill terminal sessions if deleting the active layout
      if (activeName === name) {
        const current = treeRef.current;
        if (current) {
          for (const leaf of collectLeaves(current)) {
            if (leaf.panel === "docker-agent" || leaf.panel === "host-shell" || leaf.panel === "docker-shell") {
              const target = leaf.panel === "host-shell" ? "host" : "agent";
              const closeKey = `${target}:${channelId}:${leaf.id}`;
              getCloseForInstance(closeKey)?.();
            }
          }
          if (hasAgentLeaf(current)) killAgentContainer(channelId);
        }
      }
      deleteLayout(channelId, name);
      if (activeName === name) setLearnOpen(false);
      const remaining = layoutNames.filter((n) => n !== name);
      setLayoutNames(remaining);
      if (activeName === name && remaining.length > 0) {
        const next = remaining[0]!;
        const ch = loadChannelLayouts(channelId);
        const lt = ch?.types[next] ?? DEFAULT_LAYOUT_TYPES[next] ?? "split";
        const t = ch?.layouts[next] ?? null;
        setActiveName(next);
        setLayoutType(lt);
        if (lt === "canvas") {
          setCanvasState((t as CanvasNode) ?? null);
          setTree(null);
        } else {
          if (t) initIdCounter(channelId, t as PaneNode);
          setTree(t as PaneNode | null);
          setCanvasState(null);
        }
        statusMapRef.current.clear();
        setAgentState("none");
        saveActiveLayout(channelId, next);
      }
    },
    [channelId, layoutNames, activeName],
  );

  const handleReorderLayout = useCallback(
    (fromName: string, toName: string) => {
      if (fromName === toName) return;
      setLayoutNames((prev) => {
        const from = prev.indexOf(fromName);
        const to = prev.indexOf(toName);
        if (from < 0 || to < 0) return prev;
        const next = prev.slice();
        const [moved] = next.splice(from, 1);
        next.splice(to, 0, moved!);
        reorderLayout(channelId, from, to);
        return next;
      });
    },
    [channelId],
  );

  // --- Tree operations ---
  const handleDrop = useCallback((dragId: string, dropId: string, position: DropPosition) => {
    if (dragId === dropId) return;
    setTree((prev) => {
      if (!prev) return prev;
      if (position === "center") {
        return swapLeavesInTree(prev, dragId, dropId);
      }
      return moveLeaf(prev, dragId, dropId, position);
    });
  }, []);

  const handleUpdateFlex = useCallback((parentPath: number[], dividerIndex: number, flexA: number, flexB: number) => {
    setTree((prev) => (prev ? updateFlex(prev, parentPath, dividerIndex, flexA, flexB) : prev));
  }, []);

  const handleRemoveLeaf = useCallback(
    (id: string) => {
      const current = treeRef.current;
      if (!current) return;
      const leaf = findLeafById(current, id);
      const wasAgent = leaf?.panel === "docker-agent";
      if (leaf) closeTerminalSession(channelId, leaf);
      statusMapRef.current.delete(id);
      setMaximizedLeafId((prev) => (prev === id ? null : prev));
      setShownComponent((prev) => (prev?.leafId === id ? null : prev));
      setTree((prev) => {
        if (!prev) return prev;
        if (leafCount(prev) <= 1) {
          clearLayout(channelId, activeName);
          if (wasAgent) killAgentContainer(channelId);
          setAgentState("none");
          return null;
        }
        const newTree = removeLeaf(prev, id) ?? null;
        if (wasAgent && newTree && !hasAgentLeaf(newTree)) {
          killAgentContainer(channelId);
          setAgentState("none");
        } else {
          setAgentState(computeAgentState());
        }
        return newTree;
      });
    },
    [channelId, activeName, computeAgentState],
  );

  const handleSplitLeaf = useCallback(
    (leafId: string, panel: PanelType, direction: SplitDirection, meta?: { openMode?: AgentOpenMode }) => {
      setTree((prev) => {
        if (!prev) return prev;
        if (!canAddPanel(prev, panel)) return prev;
        const newLeafId = leafIdForPanel(channelId, panel);
        const openMode = panel === "docker-agent" ? meta?.openMode : undefined;
        let result = splitLeaf(prev, leafId, direction, makeLeaf(newLeafId, panel, 1, openMode));
        if (panel === "editor") {
          result = withDefaultFileTreeForEditor(result, newLeafId, leafIdForPanel(channelId, "file-tree"));
        }
        return result;
      });
    },
    [channelId],
  );

  const handleEmptyAdd = useCallback(
    (panel: PanelType, meta?: { openMode?: AgentOpenMode }) => {
      const newLeafId = leafIdForPanel(channelId, panel);
      const openMode = panel === "docker-agent" ? meta?.openMode : undefined;
      let newTree: PaneNode = makeLeaf(newLeafId, panel, 1, openMode);
      if (panel === "editor") {
        newTree = withDefaultFileTreeForEditor(newTree, newLeafId, leafIdForPanel(channelId, "file-tree"));
      }
      setTree(newTree);
      // If this is the first panel in a new layout, ensure it's persisted with a name
      if (!layoutNames.includes(activeName)) {
        setLayoutNames((prev) => [...prev, activeName]);
      }
      saveLayout(channelId, activeName, newTree);
    },
    [channelId, activeName, layoutNames],
  );

  // --- UI commands (uiBridge) ---
  // Steps of a UI command run one after another before React renders, so the
  // pane ops update treeRef at once for the next step to see.
  const activeNameRef = useRef(activeName);
  activeNameRef.current = activeName;
  const layoutNamesRef = useRef(layoutNames);
  layoutNamesRef.current = layoutNames;
  const switchLayoutRef = useRef(switchLayout);
  switchLayoutRef.current = switchLayout;
  const handleRemoveLeafRef = useRef(handleRemoveLeaf);
  handleRemoveLeafRef.current = handleRemoveLeaf;

  useEffect(() => {
    const setTreeNow = (t: PaneNode) => {
      treeRef.current = t;
      setTree(t);
    };
    const newLeaf = (panel: PanelType, opts: PaneOptions = {}) => {
      const leaf = makeLeaf(leafIdForPanel(channelId, panel), panel, 1, panel === "docker-agent" ? opts.openMode : undefined);
      // A playground pane reads the playground it shows when it mounts.
      if (panel === "playground" && opts.playground) storageSetJSON(`playground-active:${channelId}:${leaf.id}`, opts.playground);
      return leaf;
    };
    const withFileTree = (t: PaneNode, leaf: LeafNode) => (leaf.panel === "editor" ? withDefaultFileTreeForEditor(t, leaf.id, leafIdForPanel(channelId, "file-tree")) : t);
    const leafOf = (id: string) => {
      const leaf = treeRef.current ? findLeafById(treeRef.current, id) : null;
      if (!leaf) throw new Error(`no pane "${id}"`);
      return leaf;
    };
    const controller: WorkspaceController = {
      view: () => {
        const canvas = layoutTypeRef.current === "canvas";
        const t = treeRef.current;
        return {
          tab: activeNameRef.current,
          tabs: layoutNamesRef.current,
          canvas,
          panes:
            canvas || !t
              ? []
              : collectLeaves(t).map((l) => {
                  const status = statusMapRef.current.get(l.id);
                  const busy = getTerminalInput(channelId, l.id)?.busy();
                  return { id: l.id, panel: l.panel, ...(l.openMode ? { open_mode: l.openMode } : {}), ...(status ? { status } : {}), ...(busy !== undefined ? { busy } : {}) };
                }),
          ...(!canvas && maximizedLeafIdRef.current ? { maximized: maximizedLeafIdRef.current } : {}),
        };
      },
      maximize: (id) => {
        maximizedLeafIdRef.current = id;
        setMaximizedLeafId(id);
      },
      setTab: (name) => switchLayoutRef.current(name),
      replacePane: (id, panel, opts) => {
        const current = treeRef.current;
        const old = leafOf(id);
        if (!canAddPanel(current ? removeLeaf(current, id) : null, panel)) throw new Error(`the tab can't have another ${panel} pane`);
        const leaf = newLeaf(panel, opts);
        const next = withFileTree(replaceLeaf(current as PaneNode, id, leaf), leaf);
        closeTerminalSession(channelId, old);
        statusMapRef.current.delete(id);
        setMaximizedLeafId((prev) => (prev === id ? null : prev));
        setShownComponent((prev) => (prev?.leafId === id ? null : prev));
        if (old.panel === "docker-agent" && !hasAgentLeaf(next)) killAgentContainer(channelId);
        setTreeNow(next);
        return leaf.id;
      },
      addPane: (panel, nextTo, direction, opts) => {
        const current = treeRef.current;
        if (!canAddPanel(current, panel)) throw new Error(`the tab can't have another ${panel} pane`);
        const leaf = newLeaf(panel, opts);
        if (!current) {
          const next = withFileTree(leaf, leaf);
          setTreeNow(next);
          const name = activeNameRef.current;
          if (!layoutNamesRef.current.includes(name)) setLayoutNames((prev) => [...prev, name]);
          saveLayout(channelId, name, next);
          return leaf.id;
        }
        const anchor = nextTo ? leafOf(nextTo) : (findLastLeaf(current) as LeafNode);
        setTreeNow(withFileTree(splitLeaf(current, anchor.id, direction, leaf), leaf));
        return leaf.id;
      },
      removePane: (id) => {
        leafOf(id);
        const current = treeRef.current as PaneNode;
        handleRemoveLeafRef.current(id);
        treeRef.current = leafCount(current) <= 1 ? null : removeLeaf(current, id);
      },
    };
    return registerWorkspace(channelId, controller);
  }, [channelId]);

  // What the window reports changes with the tab and its panes.
  useEffect(() => notifyUiChanged(), [tree, activeName, layoutNames, layoutType, maximizedLeafId]);

  // Listen for cross-component "open this panel" requests (e.g. the chat-bar
  // quality icon). The event carries the target channelId so other workspaces
  // ignore it. If the panel already exists in the active tree this is a no-op;
  // otherwise we split the first leaf or create a fresh tree if empty.
  //
  // The optional `anchorPanel` hint pins the new leaf to the right of an
  // existing panel (e.g. push-to-chat opens chat to the right of review).
  // If the anchor is missing we fall back to splitting the first leaf.
  useEffect(() => {
    const handler = (ev: Event) => {
      const ce = ev as CustomEvent<{ channelId: string; panel: PanelType; anchorPanel?: PanelType }>;
      if (!ce.detail || ce.detail.channelId !== channelId) return;
      // The panel is in the layout: the Learn view closes to show it.
      setLearnOpen(false);
      const panel = ce.detail.panel;
      const anchorPanel = ce.detail.anchorPanel;
      // On a canvas, a tile for it goes beside the anchor's, unless there's one.
      if (layoutTypeRef.current === "canvas") {
        setCanvasState((prev) => withPanelTile(prev ?? { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] }, panel, anchorPanel, `${panel}-${Date.now()}`));
        return;
      }
      const current = treeRef.current;
      const existing = current ? collectLeaves(current).find((l) => l.panel === panel) : undefined;
      if (existing) {
        // It may be in the layout yet hidden, minimized or under another
        // pane maximized (the chat stays mounted there, parked): show it.
        setMinimizedLeaves((prev) => {
          if (!prev.has(existing.id)) return prev;
          const next = new Set(prev);
          next.delete(existing.id);
          return next;
        });
        const maximized = maximizedLeafIdRef.current;
        if (maximized && maximized !== existing.id) {
          setMaximizedLeafId(null);
          setShownComponent(null);
        }
        return;
      }
      if (!current) {
        handleEmptyAdd(panel);
        return;
      }
      const leaves = collectLeaves(current);
      const anchorLeaf = anchorPanel ? leaves.find((l) => l.panel === anchorPanel) : undefined;
      if (anchorLeaf && canAddPanel(current, panel)) {
        const newLeafId = leafIdForPanel(channelId, panel);
        setTree((prev) => {
          if (!prev) return prev;
          return insertOppositeHorizontal(prev, anchorLeaf.id, makeLeaf(newLeafId, panel));
        });
        return;
      }
      if (leaves[0]) {
        handleSplitLeaf(leaves[0].id, panel, "vertical");
      }
    };
    window.addEventListener("loop:open-panel", handler);
    return () => window.removeEventListener("loop:open-panel", handler);
  }, [channelId, handleEmptyAdd, handleSplitLeaf]);

  // Listen for "open this file in the editor" events dispatched by chat
  // FileLink and by the review panel's per-file Open button. If no editor
  // leaf exists yet, place one on the opposite horizontal side of an
  // anchor leaf — chat if present, otherwise whichever leaf is first in
  // the tree (e.g. the review panel). The anchor split keeps the new
  // editor adjacent to the panel the user clicked from, and going through
  // insertOppositeHorizontal (rather than handleSplitLeaf) deliberately
  // skips the automatic file-tree sidecar that handleSplitLeaf would
  // attach — opening a file should give the editor alone, not the editor
  // + file tree combo.
  // The learn thread's FileLinks in the Learn view count too (it shares the channel's roots).
  useEffect(() => {
    const handler = (ev: Event) => {
      const ce = ev as CustomEvent<FileLinkOpenDetail>;
      if (!ce.detail || (ce.detail.channelId !== channelId && (!learnChannelId || ce.detail.channelId !== learnChannelId))) return;
      // The editor is in the layout: the Learn view closes to show it.
      setLearnOpen(false);
      const { target, line } = ce.detail;
      const pathKey = makePathKey(target.rootIndex, target.relPath);

      const current = treeRef.current;
      const hasEditor = current ? collectLeaves(current).some((l) => l.panel === "editor") : false;
      if (!hasEditor && current && canAddPanel(current, "editor")) {
        const leaves = collectLeaves(current);
        const anchorLeaf = leaves.find((l) => l.panel === "chat") ?? leaves[0];
        const editorId = leafIdForPanel(channelId, "editor");
        if (anchorLeaf) {
          setTree((prev) => {
            if (!prev) return prev;
            return insertOppositeHorizontal(prev, anchorLeaf.id, makeLeaf(editorId, "editor"));
          });
        }
      } else if (!current) {
        handleEmptyAdd("editor");
      }

      editorState.openFileAtLine(pathKey, line);
    };
    window.addEventListener("loop:open-file", handler);
    return () => window.removeEventListener("loop:open-file", handler);
  }, [channelId, learnChannelId, editorState, handleEmptyAdd]);

  const handleKillAgents = useCallback(() => {
    // Close all agent and shell terminal sessions in the current tree.
    const current = treeRef.current;
    if (current) {
      for (const leaf of collectLeaves(current)) {
        if (leaf.panel === "docker-agent" || leaf.panel === "host-shell" || leaf.panel === "docker-shell") {
          const target = leaf.panel === "host-shell" ? "host" : "agent";
          const closeKey = `${target}:${channelId}:${leaf.id}`;
          getCloseForInstance(closeKey)?.();
        }
      }
    }
    killAgentContainer(channelId);
  }, [channelId]);

  // "Review" is only a real default when the project enables the
  // review feature — without that flag the tab is hidden, so it must
  // also not count as a "missing default" that lights up the restore
  // button.
  const hasMissingDefaults = (DEFAULT_LAYOUT_NAMES as readonly string[]).some((n) => {
    if (n === "Review" && !channel.review_enabled) return false;
    return !layoutNames.includes(n);
  });
  const isDefaultLayout = (DEFAULT_LAYOUT_NAMES as readonly string[]).includes(activeName);

  const restoreDefaults = useCallback(() => {
    const ch = restoreDefaultLayouts(channelId);
    setLayoutNames(ch.order);
  }, [channelId]);

  const handleResetLayout = useCallback(() => {
    setLearnOpen(false);
    // Close any running terminal sessions in the current tree
    const current = treeRef.current;
    if (current) {
      for (const leaf of collectLeaves(current)) {
        if (leaf.panel === "docker-agent" || leaf.panel === "host-shell" || leaf.panel === "docker-shell") {
          const target = leaf.panel === "host-shell" ? "host" : "agent";
          const closeKey = `${target}:${channelId}:${leaf.id}`;
          getCloseForInstance(closeKey)?.();
        }
      }
      if (hasAgentLeaf(current)) {
        killAgentContainer(channelId);
      }
    }
    const defaults = createDefaultLayouts();
    const defaultTree = defaults.layouts[activeName] ?? null;
    const lt = defaults.types[activeName] ?? "split";
    if (defaultTree) {
      saveLayout(channelId, activeName, defaultTree as any);
      setLayoutType(lt);
      if (lt === "canvas") {
        setCanvasState(defaultTree as CanvasNode);
        setTree(null);
      } else {
        initIdCounter(channelId, defaultTree as PaneNode);
        setTree(defaultTree as PaneNode);
        setCanvasState(null);
      }
    } else {
      clearLayout(channelId, activeName);
      setTree(null);
      setCanvasState(null);
    }
    statusMapRef.current.clear();
    setAgentState("none");
  }, [channelId, activeName]);

  const dirPath = channel.dir_path || "";
  const branch = channel.branch || "";
  const hiddenPanels = useMemo<PanelType[] | undefined>(() => {
    const hidden: PanelType[] = [];
    if (!channel.review_enabled) hidden.push("review");
    return hidden.length > 0 ? hidden : undefined;
  }, [channel.review_enabled]);

  // The chat is mounted once, into chatHost (see ChatSlot); it renders once a slot holds the host.
  const [chatHost] = useState(createChatHost);
  const [chatHostPlaced, setChatHostPlaced] = useState(false);
  const placeChatHost = useCallback(() => setChatHostPlaced(true), []);
  // Leaving, the slot holding the host parked it; nothing will take it now.
  useEffect(
    () => () => {
      chatHost.closest("[data-chat-park]")?.remove();
      chatHost.remove();
    },
    [chatHost],
  );

  // Opening the Learn view makes the layout under it inert, so focus left
  // there (on the chat pane's Learn badge, say) would drop to nowhere: it
  // goes to the chat's composer, which moves into the view keeping it.
  // Docked on a canvas, nothing goes inert and focus stays.
  // Opening it with the other pane beside the chat swaps the pane.
  const openSide = useCallback(
    (view: SideView) => {
      const docked = layoutType === "canvas";
      const focused = document.activeElement;
      if (!docked && (!focused || !chatHost.contains(focused))) {
        chatHost.querySelector<HTMLTextAreaElement>("textarea")?.focus({ preventScroll: true });
      }
      setSideView(view);
      setLearnDocked(docked);
      setLearnOpen(true);
    },
    [chatHost, layoutType],
  );
  openSideRef.current = openSide;
  const openLearn = useCallback(() => openSide("learn"), [openSide]);
  const openExplain = useCallback(() => openSide("explain"), [openSide]);
  const learnShown = learnOpen && sideView === "learn";
  const explainShown = learnOpen && sideView === "explain";
  // Closing the Learn view with focus in its Learn pane would leave focus
  // nowhere once the pane goes: it goes back to the chat's composer, which
  // stays. Escape closes it too, unless something took the key first (a
  // picker, the find bar, a menu, a sidebar input, a shown component) or the
  // layout is hidden (under Settings, say).
  const closeLearn = useCallback(() => {
    const focused = document.activeElement;
    setLearnOpen(false);
    if (focused && learnSplitRef.current?.contains(focused) && !chatHost.contains(focused)) {
      chatHost.querySelector<HTMLTextAreaElement>("textarea")?.focus({ preventScroll: true });
    }
  }, [chatHost]);
  const hiddenRef = useRef(false);
  hiddenRef.current = style?.display === "none";
  useEffect(() => {
    if (!learnOpen) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || hiddenRef.current) return;
      // Whatever takes Escape marks it handled (preventDefault), but a
      // window listener added after this one (a menu opened in the view)
      // only sees it after this one does: look once the key is dispatched.
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (!e.defaultPrevented) closeLearn();
      });
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      clearTimeout(timer);
    };
  }, [learnOpen, closeLearn]);
  const chatLeafId = useMemo(
    () => (layoutType === "canvas" ? canvasState?.tiles.find((t) => t.panel === "chat")?.id : tree ? collectLeaves(tree).find((l) => l.panel === "chat")?.id : undefined),
    [layoutType, canvasState, tree],
  );

  // The Learn view keeps the chat while it fades out, even after a layout
  // switch took its pane away.
  const lastChatLeafIdRef = useRef(chatLeafId);
  if (chatLeafId) lastChatLeafIdRef.current = chatLeafId;
  const portalLeafId = chatLeafId ?? (learnOverlay ? lastChatLeafIdRef.current : undefined);
  // Docked, the view goes with the chat's tile when that's closed.
  useEffect(() => {
    if (learnDocked && !chatLeafId) setLearnOpen(false);
  }, [learnDocked, chatLeafId]);

  const renderChat = useCallback(
    (leafId: string) => (
      <ComponentFocusContext.Provider value={(c) => openComponent(leafId, c)}>
        <LearnContext.Provider value={learn}>
          <ExplainContext.Provider value={explain}>
            <ChatView
              key={`layout-chat-${channelId}`}
              channelId={channelId}
              chatState={chatState}
              roots={editorState.roots}
              scrollToMessageId={scrollToMessageId}
              onScrollComplete={onScrollComplete}
              trustPending={!!channel.trust_pending}
              onReviewTrust={onReviewProjectTrust}
              onSelectThread={onSelectThread}
            />
          </ExplainContext.Provider>
        </LearnContext.Provider>
        {shownComponent?.leafId === leafId && <ChatComponentFull component={shownComponent.component} onClose={closeComponent} />}
      </ComponentFocusContext.Provider>
    ),
    [
      openComponent,
      learn,
      explain,
      channelId,
      chatState,
      editorState.roots,
      scrollToMessageId,
      onScrollComplete,
      channel.trust_pending,
      onReviewProjectTrust,
      onSelectThread,
      shownComponent,
      closeComponent,
    ],
  );

  const renderLeaf = useCallback(
    (leaf: LeafNode): React.ReactNode => {
      switch (leaf.panel) {
        case "chat":
          // While the Learn view is up over the layout (open or fading out)
          // it holds the chat, and this pane stays empty. Docked on a canvas,
          // the chat stays here, and the badge closes the view.
          return (
            <>
              <ChatSlot host={chatHost} active={!learnOverlay} onAttach={placeChatHost} leafId={leaf.id} />
              <ExplainBadge leafId={leaf.id} explain={explain} open={learnDocked && explainShown} onToggle={learnDocked && explainShown ? closeLearn : openExplain} />
              <LearnBadge leafId={leaf.id} learn={learn} open={learnDocked && learnShown} onToggle={learnDocked && learnShown ? closeLearn : openLearn} />
            </>
          );
        case "editor":
          return (
            <EditorPanel key={`layout-editor-${channelId}`} channelId={channelId} dirPath={dirPath} branch={branch} embedded editorState={editorState} onClose={() => handleRemoveLeaf(leaf.id)} />
          );
        case "file-tree":
          return (
            <FileTreePanel key={`layout-file-tree-${channelId}`} channelId={channelId} dirPath={dirPath} branch={branch} embedded editorState={editorState} onClose={() => handleRemoveLeaf(leaf.id)} />
          );
        case "memory":
          return (
            <MemoryPanel
              key={`layout-memory-${channelId}`}
              channelId={channelId}
              dirPath={dirPath}
              branch={branch}
              embedded
              openMemoryFile={openMemoryFile}
              onClose={() => handleRemoveLeaf(leaf.id)}
            />
          );
        case "git":
          return (
            <GitPanel
              key={`layout-git-${channelId}`}
              channelId={channelId}
              dirPath={dirPath}
              branch={branch}
              embedded
              leafId={leaf.id}
              isWorktree={channel.worktree}
              hasBranch={!channel.parent_id && !!channel.branch}
              onImportWorktree={onImportWorktree}
              onSelectThread={onSelectThread}
              onStatusChange={onStatusChange}
              onClose={() => handleRemoveLeaf(leaf.id)}
            />
          );
        case "docker-agent": {
          // The backend tags terminal-sourced gates as "terminal:<leafId>"
          // where <leafId> is the FE pane id stamped on the exec via the
          // LOOP_TERMINAL_LEAF env var. Match exactly so a gate triggered in
          // pane A doesn't render in pane B.
          const paneSourceTag = `terminal:${leaf.id}`;
          // Legacy persisted leaves (pre-feature) have no openMode — default
          // to "fork" so behavior is unchanged for existing layouts.
          const openMode: AgentOpenMode = leaf.openMode ?? "fork";
          return (
            <div
              key={`layout-docker-agent-${channelId}-${leaf.id}`}
              data-testid="docker-agent-pane"
              style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, overflow: "hidden", backgroundColor: colors.sidebar }}
            >
              <Terminal
                channelId={channelId}
                target="agent"
                instanceId={leaf.id}
                openMode={openMode}
                hideActions
                onStatusChange={onStatusChange}
                onPaneStatus={(status) => handlePaneStatus(leaf.id, status)}
                gateApproval={chatState.gateApprovals[paneSourceTag] ?? null}
                onGateApprovalResolved={() => chatState.clearGateApproval(paneSourceTag)}
              />
            </div>
          );
        }
        case "host-shell":
          return (
            <div key={`layout-host-shell-${channelId}-${leaf.id}`} style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, overflow: "hidden", backgroundColor: colors.sidebar }}>
              <Terminal channelId={channelId} target="host" instanceId={leaf.id} hideActions onStatusChange={onStatusChange} onPaneStatus={(status) => handlePaneStatus(leaf.id, status)} />
            </div>
          );
        case "docker-shell":
          return (
            <div key={`layout-docker-shell-${channelId}-${leaf.id}`} style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, overflow: "hidden", backgroundColor: colors.sidebar }}>
              <Terminal
                channelId={channelId}
                target="agent"
                cmd={["/bin/bash"]}
                instanceId={leaf.id}
                hideActions
                onStatusChange={onStatusChange}
                onPaneStatus={(status) => handlePaneStatus(leaf.id, status)}
              />
            </div>
          );
        case "docker-browser":
          return <BrowserPanel key={`layout-docker-browser-${channelId}`} channelId={channelId} fixedMode="docker" />;
        case "host-browser":
          return <BrowserPanel key={`layout-host-browser-${channelId}`} channelId={channelId} fixedMode="host" />;
        case "sessions":
          return <SessionsPanel key={`layout-sessions-${channelId}`} channelId={channelId} onStatusChange={onStatusChange} />;
        case "playground":
          return <PlaygroundPanel key={`layout-playground-${channelId}-${leaf.id}`} channelId={channelId} instanceId={leaf.id} />;
        case "notes":
          return <NotesPanel key={`layout-notes-${channelId}`} channelId={channelId} />;
        case "tasks":
          return <TasksPanel key={`layout-tasks-${channelId}`} channelId={channelId} allowWorktree={!channel.worktree && !!channel.branch} onSelectChannel={onSelectThread} />;
        case "kanban":
          // Assigning from inside a worktree is fine: the daemon resolves the
          // ticket store and the new worktree's base to the root checkout.
          return (
            <KanbanPanel key={`layout-kanban-${channelId}`} channelId={channelId} dirPath={dirPath} rootDirPath={channel.root_dir_path} allowWorktree={!!dirPath} onSelectChannel={onSelectThread} />
          );
        case "workflows":
          return <WorkflowsLayoutPanel key={`layout-workflows-${channelId}`} channelId={channelId} />;
        case "audit":
          return <AuditPanel key={`layout-audit-${channelId}`} channelId={channelId} />;
        case "quality":
          return <QualityPanel key={`layout-quality-${channelId}`} channelId={channelId} dirPath={dirPath} branch={branch} embedded onClose={() => handleRemoveLeaf(leaf.id)} />;
        case "review":
          return (
            <ReviewPanel
              key={`layout-review-${channelId}`}
              channelId={channelId}
              subscribeChatEvents={subscribeChatEvents}
              registerReviewView={registerReviewView}
              hasChatPanel={tree ? collectLeaves(tree).some((l) => l.panel === "chat") : false}
              gateApprovals={chatState.gateApprovals}
              onClearGateApproval={(source) => chatState.clearGateApproval(source)}
            />
          );
        default:
          return null;
      }
    },
    [
      channelId,
      chatState,
      editorState,
      dirPath,
      channel.root_dir_path,
      branch,
      scrollToMessageId,
      onScrollComplete,
      openMemoryFile,
      onStatusChange,
      handlePaneStatus,
      handleRemoveLeaf,
      subscribeChatEvents,
      registerReviewView,
      tree,
      openComponent,
      shownComponent,
      closeComponent,
      learn,
      openLearn,
      closeLearn,
      learnOverlay,
      learnDocked,
      learnShown,
      explain,
      openExplain,
      explainShown,
      chatHost,
      placeChatHost,
    ],
  );

  const learnPane =
    sideView === "explain" ? (
      <ExplainPane channelId={channelId} explain={explain} onClose={closeLearn} />
    ) : (
      <LearnPane
        learn={learn}
        worktree={!!channel.worktree}
        roots={editorState.roots}
        subscribeChannelEvents={subscribeChannelEvents}
        getChatState={getChatState}
        onChatStateUnmount={onChatStateUnmount}
        onClose={closeLearn}
      />
    );
  // The logo on the seam animates while the pane's work runs.
  const sideRunning = sideView === "explain" ? explain.explanations.some(explanationPending) : learn.running;

  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        backgroundColor: colors.islandRadius ? "transparent" : colors.sidebar,
        overflow: "hidden",
        ...style,
      }}
    >
      {/* Drag region */}
      <div
        style={{
          height: 38,
          flexShrink: 0,
          display: "flex",
          alignItems: "center",
          paddingLeft: !sidebarOpen ? 76 : 4,
          WebkitAppRegion: "drag",
        }}
      >
        {!sidebarOpen && (
          <button
            onClick={onToggleSidebar}
            title="Expand sidebar"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "2px 4px",
              lineHeight: 1,
              borderRadius: 4,
              display: "flex",
              alignItems: "center",
              WebkitAppRegion: "no-drag",
            }}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <line x1="9" y1="3" x2="9" y2="21" />
              <polyline points="13,9 16,12 13,15" />
            </svg>
          </button>
        )}
        <button
          onClick={onOpenPalette}
          title="Search messages (Cmd+K)"
          style={{
            background: "none",
            border: `1px solid ${colors.border}`,
            color: colors.textDim,
            cursor: "pointer",
            padding: "2px 8px",
            lineHeight: 1,
            borderRadius: 4,
            display: "flex",
            alignItems: "center",
            gap: 4,
            fontSize: 11,
            fontFamily: fonts.mono,
            marginLeft: 6,
            WebkitAppRegion: "no-drag",
          }}
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <span style={{ opacity: 0.7 }}>{navigator.platform.includes("Mac") ? "\u2318K" : "Ctrl+K"}</span>
        </button>
        {dirPath && (
          <>
            <ChannelHeaderInfo channel={channel} colors={colors} hideBranch={!channel.worktree && !parentIsWorktree} />
            {branch && !channel.worktree && !parentIsWorktree && (
              <>
                <span style={{ color: colors.border, flexShrink: 0, margin: "0 8px" }}>|</span>
                <HeaderBranchPicker
                  channelId={channelId}
                  branch={branch}
                  onBranchChanged={onStatusChange}
                  onCreateWorktree={channel.parent_id ? undefined : onCreateWorktree}
                  onImportWorktree={channel.parent_id ? undefined : onImportWorktree}
                  onSelectThread={channel.parent_id ? undefined : onSelectThread}
                  onError={setBranchError}
                />
              </>
            )}
          </>
        )}
        <div style={{ flex: 1 }} />
      </div>

      {/* Error bar */}
      {(error || branchError) && (
        <div
          role="alert"
          style={{
            padding: "8px 12px",
            backgroundColor: colors.errorBannerBg,
            color: colors.errorBannerText,
            fontSize: "13px",
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          <span>{branchError || error}</span>
          <button
            onClick={() => {
              setBranchError(null);
              onDismissError?.();
            }}
            style={{
              background: "none",
              border: "none",
              color: colors.errorBannerText,
              cursor: "pointer",
              fontSize: "16px",
            }}
          >
            &times;
          </button>
        </div>
      )}

      {/* Layout buttons & tabs */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          padding: "0 8px",
          height: 35,
          boxSizing: "border-box",
          gap: 4,
          backgroundColor: colors.surface,
          borderRadius: colors.islandRadius,
          boxShadow: colors.islandShadow,
          border: colors.islandBorder,
          marginBottom: colors.islandGap,
          ...(!colors.islandRadius && { borderBottom: `1px solid ${colors.border}` }),
        }}
      >
        <span
          style={{
            fontSize: 10,
            fontWeight: 700,
            color: colors.textDim,
            textTransform: "uppercase",
            letterSpacing: 1,
            flexShrink: 0,
          }}
        >
          Layouts
        </span>
        {layoutNames
          .filter((name) => !(channel.parent_id && name === "Sessions"))
          .filter((name) => !(name === "Review" && !channel.review_enabled))
          .map((name) => (
            <LayoutTab
              key={name}
              name={name}
              active={name === activeName}
              canDelete={layoutNames.length > 1}
              onSelect={() => {
                if (name !== activeName) switchLayout(name);
              }}
              onRename={(newName) => handleRenameLayout(name, newName)}
              onDelete={() => handleDeleteLayout(name)}
              onReorder={handleReorderLayout}
            />
          ))}
        <div style={{ position: "relative", flexShrink: 0 }}>
          <button
            onClick={() => setShowNewLayoutMenu((v) => !v)}
            title="New layout"
            style={{
              background: "none",
              border: "none",
              color: colors.textDim,
              cursor: "pointer",
              padding: "4px",
              lineHeight: 1,
              borderRadius: 12,
              display: "flex",
              alignItems: "center",
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.color = colors.textLight;
              e.currentTarget.style.background = colors.hoverBg;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.color = colors.textDim;
              e.currentTarget.style.background = "none";
            }}
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
          </button>
          {showNewLayoutMenu && (
            <div
              style={{
                position: "absolute",
                top: "100%",
                left: 0,
                marginTop: 4,
                backgroundColor: colors.surface,
                border: `1px solid ${colors.border}`,
                borderRadius: 6,
                padding: 4,
                zIndex: 1000,
                minWidth: 100,
                boxShadow: `0 4px 12px ${colors.shadow}`,
              }}
              onMouseDown={(e) => e.stopPropagation()}
            >
              <button
                onClick={() => addLayout("split")}
                style={{
                  display: "block",
                  width: "100%",
                  padding: "4px 12px",
                  background: "none",
                  border: "none",
                  color: colors.textLight,
                  fontSize: 12,
                  textAlign: "left",
                  cursor: "pointer",
                  borderRadius: 3,
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.backgroundColor = "transparent";
                }}
              >
                Split
              </button>
              <button
                data-testid="new-layout-canvas"
                onClick={() => addLayout("canvas")}
                style={{
                  display: "block",
                  width: "100%",
                  padding: "4px 12px",
                  background: "none",
                  border: "none",
                  color: colors.textLight,
                  fontSize: 12,
                  textAlign: "left",
                  cursor: "pointer",
                  borderRadius: 3,
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.backgroundColor = "rgba(255,255,255,0.08)";
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.backgroundColor = "transparent";
                }}
              >
                Canvas
              </button>
            </div>
          )}
        </div>
        <div style={{ flex: 1 }} />
        {(agentState === "running" || channel.container_running) && (
          <button
            onClick={handleKillAgents}
            title="Stop all shells and container"
            style={{
              flexShrink: 0,
              background: "none",
              border: `1px solid ${colors.error}`,
              color: colors.error,
              cursor: "pointer",
              padding: "2px 8px",
              fontSize: 10,
              fontFamily: fonts.mono,
              lineHeight: 1,
              borderRadius: 10,
              display: "flex",
              alignItems: "center",
              gap: 4,
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.backgroundColor = colors.error;
              e.currentTarget.style.color = colors.textLight;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.backgroundColor = "transparent";
              e.currentTarget.style.color = colors.error;
            }}
          >
            Stop
          </button>
        )}
        {(hasMissingDefaults || isDefaultLayout) && (
          <div style={{ position: "relative", flexShrink: 0 }}>
            <button
              onClick={() => setShowLayoutMenu((v) => !v)}
              title="Layout options"
              style={{
                background: "none",
                border: `1px solid ${colors.border}`,
                color: colors.textDim,
                cursor: "pointer",
                padding: "2px 8px",
                fontSize: 10,
                fontFamily: fonts.mono,
                lineHeight: 1,
                borderRadius: 10,
                display: "flex",
                alignItems: "center",
                gap: 4,
              }}
              onMouseEnter={(e) => {
                e.currentTarget.style.backgroundColor = colors.hoverBg;
                e.currentTarget.style.color = colors.textLight;
                e.currentTarget.style.borderColor = colors.textDim;
              }}
              onMouseLeave={(e) => {
                e.currentTarget.style.backgroundColor = "transparent";
                e.currentTarget.style.color = colors.textDim;
                e.currentTarget.style.borderColor = colors.border;
              }}
            >
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M1 4v6h6" />
                <path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10" />
              </svg>
              Reset
            </button>
            {showLayoutMenu && (
              <div
                style={{
                  position: "absolute",
                  top: "100%",
                  right: 0,
                  marginTop: 4,
                  backgroundColor: colors.surface,
                  border: `1px solid ${colors.border}`,
                  borderRadius: 6,
                  padding: 4,
                  zIndex: 1000,
                  minWidth: 150,
                  boxShadow: `0 4px 12px ${colors.shadow}`,
                  fontFamily: fonts.sans,
                }}
                onMouseDown={(e) => e.stopPropagation()}
              >
                {hasMissingDefaults && (
                  <button
                    onClick={() => {
                      restoreDefaults();
                      setShowLayoutMenu(false);
                    }}
                    style={buildLayoutMenuItemStyle(colors)}
                    onMouseEnter={(e) => {
                      e.currentTarget.style.backgroundColor = colors.hoverBg;
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.backgroundColor = "transparent";
                    }}
                  >
                    Restore defaults
                  </button>
                )}
                {isDefaultLayout && (
                  <button
                    onClick={() => {
                      handleResetLayout();
                      setShowLayoutMenu(false);
                    }}
                    style={buildLayoutMenuItemStyle(colors)}
                    onMouseEnter={(e) => {
                      e.currentTarget.style.backgroundColor = colors.hoverBg;
                    }}
                    onMouseLeave={(e) => {
                      e.currentTarget.style.backgroundColor = "transparent";
                    }}
                  >
                    Reset current
                  </button>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      {/* Layout content. Its shortcut pickers (the composers', the
          terminals') fetch again when a learn proposal adds a shortcut. */}
      <ShortcutsVersionContext.Provider value={learn.shortcutsVersion}>
        <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0, position: "relative" }}>
          {/* The layout stays put under the Learn view while it's open. */}
          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              overflow: "hidden",
              minHeight: 0,
              position: "relative",
            }}
            // Nor can it be clicked or reached from the keyboard.
            inert={learnOverlay}
          >
            {layoutType === "canvas" ? (
              <CanvasLayout
                canvas={canvasState ?? { type: "canvas", viewport: { x: 0, y: 0, zoom: 1 }, tiles: [] }}
                renderLeaf={renderLeaf}
                agentInfoMap={agentInfoMap}
                containerStats={containerStats}
                onCanvasChange={(c) => {
                  setCanvasState(c);
                }}
                hiddenPanels={hiddenPanels}
                learnDock={learnDocked && learnSplit.mounted ? { shown: learnSplit.shown, ms: learnMs, view: sideView, running: sideRunning, pane: learnPane, ref: learnSplitRef } : undefined}
              />
            ) : !tree ? (
              <EmptyLayoutPicker onAdd={handleEmptyAdd} hiddenPanels={hiddenPanels} />
            ) : maximizedLeafId && findLeafById(tree, maximizedLeafId) ? (
              (() => {
                const leaf = findLeafById(tree, maximizedLeafId)!;
                const usedSingletons = collectPanelTypes(tree);
                return (
                  <div
                    style={{
                      flex: 1,
                      display: "flex",
                      flexDirection: "column",
                      overflow: "hidden",
                      minHeight: 0,
                      minWidth: 0,
                      ...paneBoxStyle(colors),
                    }}
                  >
                    <PaneLeafHeader
                      leafId={leaf.id}
                      panel={leaf.panel}
                      usedSingletons={usedSingletons}
                      hiddenPanels={hiddenPanels}
                      agentInfo={leaf.panel === "docker-agent" ? agentInfoMap.get(leaf.id) : undefined}
                      containerStats={containerStats}
                      isMaximized
                      onRemove={() => {
                        setMaximizedLeafId(null);
                        handleRemoveLeaf(leaf.id);
                      }}
                      onDrop={handleDrop}
                      onSplitLeaf={handleSplitLeaf}
                      onToggleMaximize={() => setMaximizedLeafId(null)}
                    />
                    <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", minHeight: 0 }}>{renderLeaf(leaf)}</div>
                  </div>
                );
              })()
            ) : (
              <SplitPaneLayout
                tree={tree}
                renderLeaf={renderLeaf}
                agentInfoMap={agentInfoMap}
                containerStats={containerStats}
                minimizedLeaves={minimizedLeaves}
                hiddenPanels={hiddenPanels}
                onUpdateFlex={handleUpdateFlex}
                onDrop={handleDrop}
                onRemoveLeaf={handleRemoveLeaf}
                onSplitLeaf={handleSplitLeaf}
                onMaximize={(leafId) => setMaximizedLeafId(leafId)}
                onToggleMinimize={(leafId) =>
                  setMinimizedLeaves((prev) => {
                    const next = new Set(prev);
                    if (next.has(leafId)) next.delete(leafId);
                    else next.add(leafId);
                    return next;
                  })
                }
              />
            )}
          </div>
          {learnOverlay && (
            <LearnSplit
              ref={learnSplitRef}
              shown={learnSplit.shown}
              ms={learnMs}
              view={sideView}
              running={sideRunning}
              chatHeader={
                // The chat's pane header, without the layout controls: this
                // isn't a layout pane. Its Learn or Explain badge closes the view.
                <PaneLeafHeader leafId={LEARN_SPLIT_SLOT_ID} panel="chat" usedSingletons={new Set()} containerStats={containerStats} />
              }
              chat={
                <>
                  <ChatSlot host={chatHost} active onAttach={placeChatHost} />
                  <ExplainBadge leafId={LEARN_SPLIT_SLOT_ID} explain={explain} open={sideView === "explain"} onToggle={sideView === "explain" ? closeLearn : openExplain} />
                  <LearnBadge leafId={LEARN_SPLIT_SLOT_ID} learn={learn} open={sideView === "learn"} onToggle={sideView === "learn" ? closeLearn : openLearn} />
                </>
              }
              learnPane={learnPane}
            />
          )}
        </div>
        {chatHostPlaced && portalLeafId && createPortal(renderChat(portalLeafId), chatHost)}
      </ShortcutsVersionContext.Provider>
    </div>
  );
});

// ── Layout Tab ──

const TAB_DRAG_MIME = "application/x-loop-layout-tab";

function LayoutTab({
  name,
  active,
  canDelete,
  onSelect,
  onRename,
  onDelete,
  onReorder,
}: {
  name: string;
  active: boolean;
  canDelete: boolean;
  onSelect: () => void;
  onRename: (newName: string) => void;
  onDelete: () => void;
  onReorder: (fromName: string, toName: string) => void;
}) {
  const { colors } = useTheme();
  const [editing, setEditing] = useState(false);
  const [editValue, setEditValue] = useState(name);
  const [confirming, setConfirming] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) inputRef.current?.select();
  }, [editing]);

  // Close confirm popover on outside click.
  useEffect(() => {
    if (!confirming) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (!target.closest("[data-confirm-popover]")) setConfirming(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [confirming]);

  const commitRename = () => {
    setEditing(false);
    if (editValue.trim() && editValue.trim() !== name) {
      onRename(editValue.trim());
    } else {
      setEditValue(name);
    }
  };

  const handleDragStart = (e: React.DragEvent<HTMLDivElement>) => {
    e.dataTransfer.setData(TAB_DRAG_MIME, name);
    e.dataTransfer.effectAllowed = "move";
  };

  const handleDragOver = (e: React.DragEvent<HTMLDivElement>) => {
    if (!e.dataTransfer.types.includes(TAB_DRAG_MIME)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (!dragOver) setDragOver(true);
  };

  const handleDragLeave = () => {
    if (dragOver) setDragOver(false);
  };

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragOver(false);
    const fromName = e.dataTransfer.getData(TAB_DRAG_MIME);
    if (fromName && fromName !== name) onReorder(fromName, name);
  };

  return (
    <div
      data-testid={`layout-tab-${name}`}
      draggable={!editing}
      onDragStart={handleDragStart}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      onClick={onSelect}
      style={{
        ...buildTabButtonStyle(colors, active),
        flexShrink: 0,
        // Inactive tabs give up width (their names ellipsized) before the
        // bar overflows, so the controls at its right end stay on screen.
        ...(!active && { flexShrink: 1, minWidth: 32 }),
        position: "relative",
        paddingLeft: canDelete && !editing ? 4 : 10,
        paddingRight: canDelete && !editing ? 10 : 10,
        outline: dragOver ? `2px solid ${colors.textLight}` : undefined,
        outlineOffset: dragOver ? -2 : undefined,
        cursor: editing ? "text" : "grab",
      }}
      onMouseEnter={(e) => {
        if (!active) {
          e.currentTarget.style.background = colors.hoverBg;
          e.currentTarget.style.color = colors.textLight;
        }
      }}
      onMouseLeave={(e) => {
        if (!active) {
          e.currentTarget.style.background = "transparent";
          e.currentTarget.style.color = colors.textDim;
        }
      }}
    >
      {canDelete && !editing && (
        <button
          onClick={(e) => {
            e.stopPropagation();
            setConfirming(true);
          }}
          title="Delete layout"
          style={{
            background: "none",
            border: "none",
            color: active ? colors.textLight : colors.textDim,
            cursor: "pointer",
            padding: 0,
            lineHeight: 1,
            fontSize: 10,
            display: "flex",
            alignItems: "center",
          }}
          onMouseEnter={(e) => {
            e.currentTarget.style.color = colors.textLight;
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.color = colors.textDim;
          }}
        >
          <svg width="8" height="8" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      )}
      {editing ? (
        <input
          ref={inputRef}
          value={editValue}
          onChange={(e) => setEditValue(e.target.value)}
          onBlur={commitRename}
          onKeyDown={(e) => {
            if (e.key === "Enter") commitRename();
            if (e.key === "Escape") {
              e.preventDefault();
              setEditValue(name);
              setEditing(false);
            }
          }}
          onClick={(e) => e.stopPropagation()}
          style={{
            background: "none",
            border: `1px solid ${colors.border}`,
            color: colors.textLight,
            fontSize: 10,
            fontFamily: fonts.mono,
            padding: "0 4px",
            borderRadius: 3,
            outline: "none",
            width: Math.max(40, editValue.length * 7),
          }}
        />
      ) : (
        <span
          onDoubleClick={(e) => {
            e.stopPropagation();
            setEditValue(name);
            setEditing(true);
          }}
          title={`${name} (double-click to rename)`}
          style={{ flex: 1, minWidth: 0, textAlign: "center", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
        >
          {name}
        </span>
      )}
      {confirming && (
        <div
          data-confirm-popover
          onMouseDown={(e) => e.stopPropagation()}
          style={{
            position: "absolute",
            top: "100%",
            left: "50%",
            transform: "translateX(-50%)",
            marginTop: 10,
            backgroundColor: colors.surface,
            border: `1px solid ${colors.textLight}`,
            borderRadius: 6,
            padding: "0 8px",
            height: 22,
            boxSizing: "border-box",
            zIndex: 1000,
            boxShadow: `0 4px 12px ${colors.shadow}`,
            display: "flex",
            alignItems: "center",
            gap: 6,
            whiteSpace: "nowrap",
            fontFamily: fonts.sans,
            fontSize: 9,
          }}
        >
          {/* Arrow pointing up */}
          <svg width="16" height="9" viewBox="0 0 16 9" style={{ position: "absolute", top: -8, left: "50%", transform: "translateX(-50%)", filter: "drop-shadow(0 -2px 4px rgba(0,0,0,0.3))" }}>
            <path d="M1 9 L7 2.5 Q8 1.5 9 2.5 L15 9 Z" fill={colors.surface} stroke={colors.textLight} strokeWidth="0.75" />
            <rect x="0" y="8" width="16" height="2" fill={colors.surface} />
          </svg>
          <span style={{ color: colors.textLight }}>Delete?</span>
          <button
            onClick={(e) => {
              e.stopPropagation();
              setConfirming(false);
              onDelete();
            }}
            style={{
              background: colors.dangerBg,
              border: `1px solid ${colors.dangerText}`,
              color: colors.dangerText,
              cursor: "pointer",
              padding: "1px 6px",
              fontSize: 9,
              fontFamily: fonts.sans,
              borderRadius: 4,
              lineHeight: 1.4,
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.background = colors.dangerHoverBg;
              e.currentTarget.style.color = colors.white;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.background = colors.dangerBg;
              e.currentTarget.style.color = colors.dangerText;
            }}
          >
            Yes
          </button>
          <button
            onClick={(e) => {
              e.stopPropagation();
              setConfirming(false);
            }}
            style={{
              background: "none",
              border: `1px solid ${colors.border}`,
              color: colors.textDim,
              cursor: "pointer",
              padding: "1px 6px",
              fontSize: 9,
              fontFamily: fonts.sans,
              borderRadius: 4,
              lineHeight: 1.4,
            }}
            onMouseEnter={(e) => {
              e.currentTarget.style.color = colors.textLight;
              e.currentTarget.style.borderColor = colors.textDim;
            }}
            onMouseLeave={(e) => {
              e.currentTarget.style.color = colors.textDim;
              e.currentTarget.style.borderColor = colors.border;
            }}
          >
            No
          </button>
        </div>
      )}
    </div>
  );
}
