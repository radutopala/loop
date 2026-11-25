import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildRawFileBase } from "../../api/files";
import { DEFAULT_EDITOR_CONFIG, type RootEntry } from "../../api/loopApi";
import type { EditorStateApi } from "../../hooks/useEditorState";
import { closeWalkthrough, getOpenWalkthrough, goToStep, type OpenWalkthrough, pauseWalkthrough, playWalkthrough, setEditorSelection, useOpenWalkthrough } from "../../hooks/walkthroughStore";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors } from "../../theme";
import { logErr } from "../../utils/log";
import { openChannelFile } from "../../utils/openChannelFile";
import { askAgent, askMessage } from "./askAgent";
import { EditorPanel } from "./EditorPanel";
import { emptyGitLineChanges } from "./editorGitGutter";
import type { WalkthroughCard, WalkthroughHandlers, WalkthroughRange } from "./editorWalkthrough";

vi.mock("../../api/files", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/files")>()),
  buildRawFileBase: vi.fn(),
}));

vi.mock("../../hooks/walkthroughStore", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../hooks/walkthroughStore")>()),
  useOpenWalkthrough: vi.fn(() => null),
  getOpenWalkthrough: vi.fn(() => undefined),
  goToStep: vi.fn(() => Promise.resolve()),
  pauseWalkthrough: vi.fn(),
  playWalkthrough: vi.fn(),
  closeWalkthrough: vi.fn(),
  setEditorSelection: vi.fn(),
}));

vi.mock("../../utils/openChannelFile", () => ({
  openChannelFile: vi.fn(() => Promise.resolve({ rootIndex: 0, relPath: "src/a.ts" })),
}));

vi.mock("./askAgent", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./askAgent")>()),
  askAgent: vi.fn(() => Promise.resolve()),
}));

vi.mock("../../utils/log", () => ({
  logErr: vi.fn(() => vi.fn()),
}));

interface CapturedCodeEditorProps {
  fileContent: string | null;
  isBinary: boolean;
  binarySize: number;
  selectedRelPath: string | null;
  selectedPath: string | null;
  loading: boolean;
  error: string | null;
  previewMode: string;
  onDocChanged: () => void;
  onPreviewUpdate: (html: string) => void;
  editorMenu: { x: number; y: number } | null;
  onEditorMenuClose: () => void;
  onEditorContextMenu: (e: React.MouseEvent) => void;
  previewHtml: string;
  htmlBaseURL: string | null;
  imageURL: string | null;
  walkthrough: WalkthroughCard | null;
  walkthroughHandlers: WalkthroughHandlers;
  onSelectionChange: (range: WalkthroughRange | null) => void;
  onAskAgent: (range: WalkthroughRange) => void;
  compareWith: string | null;
  compareSource: "disk" | "head";
  tabSize: number;
  insertSpaces: boolean;
}

let lastCodeEditorProps: CapturedCodeEditorProps | null = null;

vi.mock("./CodeEditor", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./CodeEditor")>();
  const { forwardRef } = await import("react");
  const CodeEditor = forwardRef((props: CapturedCodeEditorProps, _ref) => {
    lastCodeEditorProps = props;
    return (
      <div data-testid="mock-code-editor" onContextMenu={props.onEditorContextMenu}>
        <div className="cm-editor" data-testid="cm-editor-inner">
          editor
        </div>
        <div data-testid="outside-cm-editor">outside</div>
      </div>
    );
  });
  return {
    isMarkdownFile: actual.isMarkdownFile,
    isHtmlFile: actual.isHtmlFile,
    CodeEditor,
  };
});

vi.mock("./WalkthroughsMenu", () => ({
  WalkthroughsBadge: ({ leafId, channelId, open }: { leafId: string; channelId: string; open: OpenWalkthrough | null }) => (
    <div data-testid="mock-walkthroughs-badge" data-leaf={leafId} data-channel={channelId} data-open={open ? String(open.id) : ""} />
  ),
}));

const roots: RootEntry[] = [
  { index: 0, path: "/w/app", display_path: "~/w/app", name: "app" },
  { index: 1, path: "/w/lib", name: "lib" },
];

type Api = EditorStateApi;

const makeState = (overrides: Partial<EditorStateApi> = {}): Api =>
  ({
    roots,
    expandedDirs: new Set(["0:"]),
    dirContents: new Map(),
    selectedDir: "0:",
    openTabs: ["0:src/a.ts"],
    selectedPath: "0:src/a.ts",
    previewTab: null,
    fileContent: "const x = 1;",
    isBinary: false,
    binarySize: 0,
    imageURL: null,
    retryMedia: vi.fn(),
    loading: false,
    error: null,
    gitChanges: emptyGitLineChanges(),
    dirtyTabs: new Set<string>(),
    pendingRefresh: new Map<string, string>(),
    agentEditedTabs: new Set<string>(),
    deletedTabs: new Set<string>(),
    autoSaveOnBlur: false,
    previewTabsEnabled: true,
    codeEditorRef: { current: null },
    loadDir: vi.fn(() => Promise.resolve()),
    refreshTree: vi.fn(() => Promise.resolve()),
    showTree: vi.fn(),
    toggleDir: vi.fn(),
    tabViewFor: vi.fn(() => undefined),
    saveTabView: vi.fn(),
    revealDir: vi.fn(),
    revealFile: vi.fn(),
    revealRequest: null,
    setSelectedDir: vi.fn(),
    addExtraDir: vi.fn(() => Promise.resolve()),
    handleCreateFile: vi.fn(),
    handleDeleteFilePath: vi.fn(),
    handleCreateDirPath: vi.fn(),
    handleDeleteDirPath: vi.fn(),
    handleRenamePath: vi.fn(() => Promise.resolve()),
    handleCopyPath: vi.fn(() => Promise.resolve("0:copy")),
    switchToTab: vi.fn(),
    openFile: vi.fn(),
    promoteFile: vi.fn(),
    closeTab: vi.fn(),
    closeTabs: vi.fn(),
    unpinFile: vi.fn(),
    moveTab: vi.fn(),
    markDirty: vi.fn(),
    saveFile: vi.fn(),
    saveAllDirty: vi.fn(),
    clearError: vi.fn(),
    openFileAtLine: vi.fn(),
    acceptPendingRefresh: vi.fn(),
    dismissPendingRefresh: vi.fn(),
    comparing: null,
    setComparing: vi.fn(),
    clearAgentEdited: vi.fn(),
    editorConfig: { ...DEFAULT_EDITOR_CONFIG },
    headCompare: null,
    toggleHeadCompare: vi.fn(),
    notice: null,
    dismissNotice: vi.fn(),
    ...overrides,
  }) as Api;

interface MountOpts {
  channelId?: string;
  dirPath?: string;
  leafId?: string;
  embedded?: boolean;
}

const mount = (overrides: Partial<EditorStateApi> = {}, opts: MountOpts = {}) => {
  const state = makeState(overrides);
  const { channelId = "ch-1", dirPath = "/w/app", leafId, embedded = true } = opts;
  const ui = (s: Api, o: MountOpts = opts) => (
    <ThemeProvider initialTheme="dark">
      <EditorPanel channelId={o.channelId ?? "ch-1"} dirPath={o.dirPath ?? "/w/app"} branch="main" editorState={s} leafId={o.leafId} embedded={o.embedded ?? true} onClose={() => {}} />
    </ThemeProvider>
  );
  const rendered = render(ui(state, { channelId, dirPath, leafId, embedded }));
  return {
    state,
    ...rendered,
    rerenderState: (next: Partial<EditorStateApi>, nextOpts?: MountOpts) => {
      const merged = makeState({ ...state, ...next });
      rendered.rerender(ui(merged, { channelId, dirPath, leafId, embedded, ...nextOpts }));
      return merged;
    },
  };
};

const css = (color: string) => {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
};

let resizeCallback: ResizeObserverCallback | null = null;
const resizeObserve = vi.fn();
const resizeDisconnect = vi.fn();

class MockResizeObserver {
  constructor(cb: ResizeObserverCallback) {
    resizeCallback = cb;
  }
  observe = resizeObserve;
  unobserve = vi.fn();
  disconnect = resizeDisconnect;
}

const tabAt = (index: number): HTMLElement => {
  const el = screen.getAllByTestId("editor-tab")[index];
  if (!el) throw new Error(`missing tab ${index}`);
  return el;
};

const menuItem = (label: string) => within(screen.getByTestId("context-menu")).getByRole("menuitem", { name: new RegExp(`^${label}$`) });
const menuLabels = () =>
  within(screen.getByTestId("context-menu"))
    .getAllByRole("menuitem")
    .map((b) => b.dataset.label);

describe("EditorPanel", () => {
  const origResizeObserver = globalThis.ResizeObserver;
  const writeText = vi.fn(() => Promise.resolve());

  beforeEach(() => {
    localStorage.clear();
    lastCodeEditorProps = null;
    resizeCallback = null;
    resizeObserve.mockReset();
    resizeDisconnect.mockReset();
    globalThis.ResizeObserver = MockResizeObserver as unknown as typeof ResizeObserver;
    vi.mocked(buildRawFileBase).mockReset();
    vi.mocked(buildRawFileBase).mockResolvedValue("http://localhost/raw/base/");
    vi.mocked(useOpenWalkthrough).mockReset();
    vi.mocked(useOpenWalkthrough).mockReturnValue(null);
    vi.mocked(getOpenWalkthrough).mockReset();
    vi.mocked(getOpenWalkthrough).mockReturnValue(undefined);
    vi.mocked(goToStep).mockReset();
    vi.mocked(goToStep).mockResolvedValue(undefined);
    vi.mocked(pauseWalkthrough).mockReset();
    vi.mocked(playWalkthrough).mockReset();
    vi.mocked(closeWalkthrough).mockReset();
    vi.mocked(setEditorSelection).mockReset();
    vi.mocked(openChannelFile).mockReset();
    vi.mocked(openChannelFile).mockResolvedValue({ rootIndex: 0, relPath: "src/a.ts" });
    vi.mocked(askAgent).mockReset();
    vi.mocked(askAgent).mockResolvedValue(undefined);
    vi.mocked(logErr).mockReset();
    vi.mocked(logErr).mockReturnValue(vi.fn());
    writeText.mockReset();
    writeText.mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText },
      configurable: true,
    });
  });

  afterEach(() => {
    globalThis.ResizeObserver = origResizeObserver;
    vi.restoreAllMocks();
  });

  describe("tabs and basic rendering", () => {
    it("renders without tabs when openTabs is empty and selectedPath is null", () => {
      mount({ openTabs: [], selectedPath: null, fileContent: null });
      expect(screen.queryByTestId("editor-tabs")).toBeNull();
      expect(screen.queryByTestId("editor-diff-head")).toBeNull();
      expect(screen.queryByTestId("preview-mode-toggle")).toBeNull();
      expect(lastCodeEditorProps?.selectedPath).toBeNull();
      expect(lastCodeEditorProps?.selectedRelPath).toBeNull();
      expect(lastCodeEditorProps?.compareWith).toBeNull();
      expect(lastCodeEditorProps?.compareSource).toBe("head");
    });

    it("renders WalkthroughsBadge only when leafId is provided", () => {
      const { rerenderState } = mount({}, { leafId: undefined });
      expect(screen.queryByTestId("mock-walkthroughs-badge")).toBeNull();

      rerenderState({}, { leafId: "leaf-42" });
      const badge = screen.getByTestId("mock-walkthroughs-badge");
      expect(badge.dataset.leaf).toBe("leaf-42");
      expect(badge.dataset.channel).toBe("ch-1");
    });

    it("renders tab states (active, preview, dirty, deleted, agent-edited, pending, descriptions, fallback root/name)", () => {
      const { state } = mount({
        openTabs: ["0:src/a.ts", "1:src/a.ts", "2:trailing/", "0:dirty.ts", "0:deleted.ts", "0:pending.ts"],
        selectedPath: "0:src/a.ts",
        previewTab: "1:src/a.ts",
        dirtyTabs: new Set(["0:dirty.ts"]),
        deletedTabs: new Set(["0:deleted.ts"]),
        agentEditedTabs: new Set(["0:src/a.ts"]),
        pendingRefresh: new Map([["0:pending.ts", "disk"]]),
      });

      expect(screen.getAllByTestId("editor-tab")).toHaveLength(6);
      const t0 = tabAt(0);
      const t1 = tabAt(1);
      const t2 = tabAt(2);
      const t4 = tabAt(4);
      expect(t0.dataset.active).toBe("true");
      expect(t1.dataset.preview).toBe("true");
      expect(t4.dataset.deleted).toBe("true");
      // Trailing slash falls back to full relativePath when pop() is empty
      expect(t2.textContent).toContain("trailing/");
      // Duplicate basenames show descriptions
      expect(t0.textContent).toContain("app");
      expect(t1.textContent).toContain("lib");
      // Agent-edited / pending dots
      expect(screen.getAllByTestId("editor-tab-agent-edited")).toHaveLength(2);

      // Clicking active tab clears agent-edited; clicking inactive switches tab
      fireEvent.click(t0);
      expect(state.clearAgentEdited).toHaveBeenCalledWith("0:src/a.ts");
      fireEvent.click(t1);
      expect(state.switchToTab).toHaveBeenCalledWith("1:src/a.ts");

      // Double-clicking preview tab promotes it; double-clicking non-preview does nothing
      fireEvent.doubleClick(t1);
      expect(state.promoteFile).toHaveBeenCalledWith("1:src/a.ts");
      fireEvent.doubleClick(t0);
      expect(state.promoteFile).toHaveBeenCalledTimes(1);

      // Hover on inactive tab changes background; active tab ignores hover
      fireEvent.mouseEnter(t1);
      expect(t1.style.backgroundColor).toBe(css(darkColors.hoverBg));
      fireEvent.mouseLeave(t1);
      expect(t1.style.backgroundColor).toBe("transparent");

      const activeBg = t0.style.backgroundColor;
      fireEvent.mouseEnter(t0);
      fireEvent.mouseLeave(t0);
      expect(t0.style.backgroundColor).toBe(activeBg);

      // Close icon hover & click on clean tab, and click on dirty dot
      const closeChar = within(t1).getByText("×");
      fireEvent.mouseEnter(closeChar);
      expect(closeChar.style.opacity).toBe("1");
      fireEvent.mouseLeave(closeChar);
      expect(closeChar.style.opacity).toBe("0.5");
      fireEvent.click(closeChar);
      expect(state.closeTab).toHaveBeenCalledWith("1:src/a.ts", expect.anything());
    });

    it("scrolls active tab into view when strip has width and handles ResizeObserver", () => {
      const { rerenderState } = mount({
        openTabs: ["0:a.ts", "0:b.ts"],
        selectedPath: "0:b.ts",
      });
      const strip = screen.getByTestId("editor-tabs");
      const t1 = tabAt(1);

      // clientWidth === 0 initially -> does not scroll
      expect(strip.scrollLeft).toBe(0);

      // Give strip a non-zero width and position active tab off the right edge
      Object.defineProperty(strip, "clientWidth", { value: 100, configurable: true });
      vi.spyOn(strip, "getBoundingClientRect").mockReturnValue({ left: 0, width: 100, right: 100, top: 0, bottom: 24, height: 24, x: 0, y: 0, toJSON: () => ({}) });
      vi.spyOn(t1, "getBoundingClientRect").mockReturnValue({ left: 150, width: 60, right: 210, top: 0, bottom: 24, height: 24, x: 150, y: 0, toJSON: () => ({}) });

      // Trigger ResizeObserver callback
      act(() => {
        resizeCallback?.([], {} as ResizeObserver);
      });
      expect(strip.scrollLeft).toBe(110);

      // Now active tab is already in view -> scrollLeftToShow returns null
      vi.spyOn(t1, "getBoundingClientRect").mockReturnValue({ left: 10, width: 50, right: 60, top: 0, bottom: 24, height: 24, x: 10, y: 0, toJSON: () => ({}) });
      act(() => {
        resizeCallback?.([], {} as ResizeObserver);
      });
      expect(strip.scrollLeft).toBe(110);

      // selectedPath not matching any tab -> early return in show()
      rerenderState({ openTabs: ["0:a.ts"], selectedPath: "0:missing.ts" });
      // selectedPath null -> early return in effect
      rerenderState({ openTabs: ["0:a.ts"], selectedPath: null });
      expect(resizeDisconnect).toHaveBeenCalled();
    });
  });

  describe("tab drag and drop", () => {
    const makeDataTransfer = () => ({
      setData: vi.fn(),
      effectAllowed: "",
      dropEffect: "",
    });

    const fireDrag = (el: Element, type: string, opts: { clientX?: number; dataTransfer?: ReturnType<typeof makeDataTransfer>; relatedTarget?: Node | null } = {}) => {
      const evt = new MouseEvent(type, { bubbles: true, cancelable: true, clientX: opts.clientX ?? 0, relatedTarget: opts.relatedTarget ?? null });
      Object.defineProperty(evt, "dataTransfer", { value: opts.dataTransfer ?? makeDataTransfer() });
      fireEvent(el, evt);
    };

    it("ignores dragover and drop when no tab from this panel is being dragged", () => {
      const { state } = mount({ openTabs: ["0:a.ts", "0:b.ts"], selectedPath: "0:a.ts" });
      const strip = screen.getByTestId("editor-tabs");
      const t0 = tabAt(0);

      fireDrag(strip, "dragover");
      fireDrag(strip, "drop");
      fireDrag(t0, "dragover");
      fireDrag(t0, "drop");
      expect(state.moveTab).not.toHaveBeenCalled();
    });

    it("reorders tabs before/after another tab and handles dragLeave and dragEnd", () => {
      const { state } = mount({ openTabs: ["0:a.ts", "0:b.ts"], selectedPath: "0:a.ts" });
      const t0 = tabAt(0);
      const t1 = tabAt(1);
      vi.spyOn(t1, "getBoundingClientRect").mockReturnValue({ left: 100, width: 100, right: 200, top: 0, bottom: 24, height: 24, x: 100, y: 0, toJSON: () => ({}) });

      const dt = makeDataTransfer();
      fireDrag(t0, "dragstart", { dataTransfer: dt });
      expect(dt.setData).toHaveBeenCalledWith("application/x-loop-editor-tab", "0:a.ts");
      expect(t0.dataset.dragging).toBe("true");

      // Drag over left half of tab 1 (after = false) twice to hit both set and no-op branches
      fireDrag(t1, "dragover", { clientX: 120 });
      expect(t1.style.boxShadow).toContain("inset 2px");
      fireDrag(t1, "dragover", { clientX: 125 });

      // Drag over right half of tab 1 (after = true)
      fireDrag(t1, "dragover", { clientX: 180 });
      expect(t1.style.boxShadow).toContain("inset -2px");

      // DragLeave into child does not clear dropAt
      fireDrag(t1, "dragleave", { relatedTarget: t1.firstChild });
      expect(t1.style.boxShadow).toContain("inset -2px");

      // DragLeave on tab 0 (dropAt is on tab 1) keeps dropAt on tab 1
      fireDrag(t0, "dragleave", { relatedTarget: null });
      expect(t1.style.boxShadow).toContain("inset -2px");

      // DragLeave on tab 1 to outside clears dropAt
      fireDrag(t1, "dragleave", { relatedTarget: null });
      expect(t1.style.boxShadow).toBe("");

      // Drop on right half of tab 1
      fireDrag(t1, "drop", { clientX: 180 });
      expect(state.moveTab).toHaveBeenCalledWith("0:a.ts", "0:b.ts", true);

      // Drag again and cancel with dragEnd
      fireDrag(t0, "dragstart");
      fireDrag(t0, "dragend");
      expect(t0.dataset.dragging).toBeUndefined();
    });

    it("drops on the tab strip after the last tab", () => {
      const { state } = mount({ openTabs: ["0:a.ts", "0:b.ts"], selectedPath: "0:a.ts" });
      const strip = screen.getByTestId("editor-tabs");
      const t0 = tabAt(0);

      fireDrag(t0, "dragstart");
      fireDrag(strip, "dragover");
      // Second dragOver hits already-matching dropAt branch
      fireDrag(strip, "dragover");
      fireDrag(strip, "drop");
      expect(state.moveTab).toHaveBeenCalledWith("0:a.ts", "0:b.ts", true);
    });
  });

  describe("tab context menu", () => {
    it("opens menu for a clean non-preview tab, runs bulk/unpin/reveal/copy actions, and closes", () => {
      const { state } = mount({
        openTabs: ["0:a.ts", "0:b.ts", "1:c.ts", "2:d.ts"],
        selectedPath: "0:a.ts",
        previewTab: null,
        previewTabsEnabled: true,
        dirtyTabs: new Set(["0:b.ts"]),
        deletedTabs: new Set(["1:c.ts"]),
      });
      const t0 = tabAt(0);
      const t2 = tabAt(2);
      const t3 = tabAt(3);

      // Right-click on active tab 0:a.ts
      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      expect(menuLabels()).toEqual(["Close", "Close others", "Close to the right", "Close saved", "Unpin tab", "Reveal in tree", "Copy relative path", "Copy absolute path"]);

      fireEvent.click(menuItem("Close"));
      expect(state.closeTab).toHaveBeenCalledWith("0:a.ts");

      // Bulk actions
      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Close others"));
      expect(state.closeTabs).toHaveBeenCalledWith(["2:d.ts"]);

      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Close to the right"));
      expect(state.closeTabs).toHaveBeenCalledWith(["2:d.ts"]);

      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Close saved"));
      expect(state.closeTabs).toHaveBeenCalledWith(["0:a.ts", "2:d.ts"]);

      // Unpin tab
      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Unpin tab"));
      expect(state.unpinFile).toHaveBeenCalledWith("0:a.ts");

      // Reveal on active tab does not call switchToTab
      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Reveal in tree"));
      expect(state.switchToTab).not.toHaveBeenCalled();
      expect(state.revealFile).toHaveBeenCalledWith("0:a.ts");

      // Reveal on inactive tab switches first
      fireEvent.contextMenu(t2, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Reveal in tree"));
      expect(state.switchToTab).toHaveBeenCalledWith("1:c.ts");
      expect(state.revealFile).toHaveBeenCalledWith("1:c.ts");

      // Copy relative & absolute path (display_path on root 0, path on root 1, dirPath fallback on root 2)
      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Copy relative path"));
      expect(writeText).toHaveBeenCalledWith("a.ts");

      fireEvent.contextMenu(t0, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Copy absolute path"));
      expect(writeText).toHaveBeenCalledWith("~/w/app/a.ts");

      fireEvent.contextMenu(t2, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Copy absolute path"));
      expect(writeText).toHaveBeenCalledWith("/w/lib/c.ts");

      fireEvent.contextMenu(t3, { clientX: 50, clientY: 20 });
      fireEvent.click(menuItem("Copy absolute path"));
      expect(writeText).toHaveBeenCalledWith("/w/app/d.ts");
    });

    it("shows Pin tab for preview tab and omits Pin/Unpin when unsaved or previewTabsEnabled is false", () => {
      const { state, rerenderState } = mount({
        openTabs: ["0:a.ts"],
        selectedPath: "0:a.ts",
        previewTab: "0:a.ts",
        previewTabsEnabled: true,
      });

      fireEvent.contextMenu(screen.getByTestId("editor-tab"), { clientX: 10, clientY: 10 });
      expect(menuLabels()).toEqual(["Close", "Close saved", "Pin tab", "Reveal in tree", "Copy relative path", "Copy absolute path"]);
      fireEvent.click(menuItem("Pin tab"));
      expect(state.promoteFile).toHaveBeenCalledWith("0:a.ts");

      // Non-preview tab when previewTabsEnabled is false -> no Pin/Unpin, separator on Reveal
      rerenderState({ previewTab: null, previewTabsEnabled: false });
      fireEvent.contextMenu(screen.getByTestId("editor-tab"), { clientX: 10, clientY: 10 });
      expect(menuLabels()).toEqual(["Close", "Close saved", "Reveal in tree", "Copy relative path", "Copy absolute path"]);

      // Dismiss via Escape
      fireEvent.keyDown(document, { key: "Escape" });
      expect(screen.queryByTestId("context-menu")).toBeNull();
    });
  });

  describe("banners and diffs", () => {
    it("renders and dismisses notice banner only for the selected tab", () => {
      const { state, rerenderState } = mount({
        selectedPath: "0:src/a.ts",
        notice: { pathKey: "0:other.ts", message: "Format failed" },
      });
      expect(screen.queryByTestId("editor-notice")).toBeNull();

      rerenderState({ notice: { pathKey: "0:src/a.ts", message: "Format failed" } });
      expect(screen.getByTestId("editor-notice").textContent).toContain("Format failed");
      fireEvent.click(screen.getByTestId("editor-notice-dismiss"));
      expect(state.dismissNotice).toHaveBeenCalled();
    });

    it("toggles HEAD diff and hides diff button for binary files", () => {
      const { state, rerenderState } = mount({
        selectedPath: "0:src/a.ts",
        headCompare: null,
      });

      const diffBtn = screen.getByTestId("editor-diff-head");
      expect(diffBtn.getAttribute("aria-pressed")).toBe("false");
      fireEvent.click(diffBtn);
      expect(state.toggleHeadCompare).toHaveBeenCalledTimes(1);

      // Active HEAD diff for selected file
      rerenderState({ headCompare: { pathKey: "0:src/a.ts", content: "old head" } });
      expect(screen.getByTestId("editor-diff-head").getAttribute("aria-pressed")).toBe("true");
      expect(screen.getByTestId("head-diff-banner")).toBeTruthy();
      expect(lastCodeEditorProps?.compareWith).toBe("old head");
      expect(lastCodeEditorProps?.compareSource).toBe("head");

      fireEvent.click(screen.getByTestId("head-diff-hide"));
      expect(state.toggleHeadCompare).toHaveBeenCalledTimes(2);

      // Binary file hides diff button
      rerenderState({ isBinary: true, headCompare: null });
      expect(screen.queryByTestId("editor-diff-head")).toBeNull();
    });

    it("renders deleted banner with Restore and Close actions", () => {
      const { state } = mount({
        selectedPath: "0:src/a.ts",
        deletedTabs: new Set(["0:src/a.ts"]),
      });
      const banner = screen.getByTestId("editor-deleted-banner");
      fireEvent.click(within(banner).getByText("Restore"));
      expect(state.saveFile).toHaveBeenCalledWith("0:src/a.ts");
      fireEvent.click(within(banner).getByText("Close"));
      expect(state.closeTab).toHaveBeenCalledWith("0:src/a.ts");
    });

    it("renders disk-change banner in normal and compare modes", () => {
      const { state, rerenderState } = mount({
        selectedPath: "0:src/a.ts",
        pendingRefresh: new Map([["0:src/a.ts", "disk content"]]),
        comparing: null,
        headCompare: { pathKey: "0:src/a.ts", content: "head content" },
      });

      expect(screen.getByTestId("disk-change-banner").textContent).toContain("Replace your unsaved changes?");
      expect(screen.getByTestId("disk-change-compare").textContent).toBe("Compare");
      expect(screen.getByTestId("disk-change-keep").textContent).toBe("Keep mine");

      fireEvent.click(screen.getByTestId("disk-change-compare"));
      expect(state.setComparing).toHaveBeenCalledWith("0:src/a.ts");
      fireEvent.click(screen.getByTestId("disk-change-replace"));
      expect(state.acceptPendingRefresh).toHaveBeenCalledWith("0:src/a.ts");
      fireEvent.click(screen.getByTestId("disk-change-keep"));
      expect(state.dismissPendingRefresh).toHaveBeenCalledWith("0:src/a.ts");

      // Comparing against disk version supersedes HEAD diff
      rerenderState({ comparing: "0:src/a.ts" });
      expect(screen.getByTestId("disk-change-banner").textContent).toContain("Use disk or keep mine per change");
      expect(screen.getByTestId("disk-change-compare").textContent).toBe("Hide diff");
      expect(screen.getByTestId("disk-change-keep").textContent).toBe("Keep this");
      expect(screen.queryByTestId("head-diff-banner")).toBeNull();
      expect(lastCodeEditorProps?.compareWith).toBe("disk content");
      expect(lastCodeEditorProps?.compareSource).toBe("disk");

      fireEvent.click(screen.getByTestId("disk-change-compare"));
      expect(state.setComparing).toHaveBeenCalledWith(null);
    });
  });

  describe("preview modes, HTML base URL, and editor context menu", () => {
    it("switches markdown preview modes and persists them", () => {
      mount({ openTabs: ["0:README.md"], selectedPath: "0:README.md" });
      expect(screen.getByTestId("preview-mode-editor").textContent).toBe("Edit");
      expect(screen.getByTestId("preview-mode-editor").title).toBe("Editor only");
      expect(screen.getByTestId("preview-mode-both").title).toBe("Editor + Preview");
      expect(screen.getByTestId("preview-mode-preview").title).toBe("Preview only");
      expect(lastCodeEditorProps?.previewMode).toBe("both");

      fireEvent.click(screen.getByTestId("preview-mode-editor"));
      expect(lastCodeEditorProps?.previewMode).toBe("editor");

      fireEvent.click(screen.getByTestId("preview-mode-preview"));
      expect(lastCodeEditorProps?.previewMode).toBe("preview");
    });

    it("switches HTML preview modes, resolves htmlBaseURL, handles updates, cancellation, and rejection", async () => {
      let resolveSecond!: (v: string) => void;
      vi.mocked(buildRawFileBase)
        .mockResolvedValueOnce("http://base/v1/")
        .mockImplementationOnce(
          () =>
            new Promise((res) => {
              resolveSecond = res;
            }),
        )
        .mockRejectedValueOnce(new Error("boom"));

      const { rerenderState, unmount } = mount({ openTabs: ["0:index.html", "0:other.html"], selectedPath: "0:index.html" });
      expect(screen.getByTestId("preview-mode-editor").textContent).toBe("Source");
      expect(screen.getByTestId("preview-mode-editor").title).toBe("Source only");
      expect(screen.getByTestId("preview-mode-both").title).toBe("Source + Preview");
      expect(lastCodeEditorProps?.previewMode).toBe("preview");

      await waitFor(() => {
        expect(lastCodeEditorProps?.htmlBaseURL).toBe("http://base/v1/");
      });

      // Switch HTML preview mode
      fireEvent.click(screen.getByTestId("preview-mode-both"));
      expect(lastCodeEditorProps?.previewMode).toBe("both");

      // Trigger onPreviewUpdate -> triggers second buildRawFileBase call
      act(() => {
        lastCodeEditorProps?.onPreviewUpdate("<h1>Hi</h1>");
      });
      expect(lastCodeEditorProps?.previewHtml).toBe("<h1>Hi</h1>");

      // Switch file before second promise resolves -> cancelled branch (!cancelled is false)
      rerenderState({ selectedPath: "0:other.html" });
      await act(async () => {
        resolveSecond("http://base/stale/");
        await Promise.resolve();
      });
      // Third call rejected (caught silently), htmlBaseURL stayed null after file switch
      expect(lastCodeEditorProps?.htmlBaseURL).toBeNull();

      unmount();
    });

    it("opens and closes editor context menu only when right-clicking inside .cm-editor", () => {
      mount();
      expect(lastCodeEditorProps?.editorMenu).toBeNull();

      // Right-click outside .cm-editor -> ignored
      fireEvent.contextMenu(screen.getByTestId("outside-cm-editor"), { clientX: 10, clientY: 20 });
      expect(lastCodeEditorProps?.editorMenu).toBeNull();

      // Right-click inside .cm-editor -> opens menu
      fireEvent.contextMenu(screen.getByTestId("cm-editor-inner"), { clientX: 30, clientY: 40 });
      expect(lastCodeEditorProps?.editorMenu).toEqual({ x: 30, y: 40 });

      // Close menu
      act(() => {
        lastCodeEditorProps?.onEditorMenuClose();
      });
      expect(lastCodeEditorProps?.editorMenu).toBeNull();
    });
  });

  describe("walkthroughs, selection, and askAgent", () => {
    const sampleWalkthrough = (overrides: Partial<OpenWalkthrough> = {}): OpenWalkthrough => ({
      id: 7,
      title: "Auth tour",
      steps: [
        { path: "src/a.ts", start_line: 1, end_line: 3, title: "Step 1", body: "Intro" },
        { path: "src/b.ts", start_line: 5, end_line: 8, title: "", body: "Second" },
      ],
      step: 0,
      target: { rootIndex: 0, relPath: "src/a.ts" },
      playing: false,
      ...overrides,
    });

    it("builds walkthroughCard only when walkthrough matches active file and step exists", () => {
      const w = sampleWalkthrough();
      vi.mocked(useOpenWalkthrough).mockReturnValue(w);

      const { rerenderState } = mount({ openTabs: ["0:src/a.ts", "1:src/a.ts", "0:src/b.ts"], selectedPath: "0:src/a.ts" });
      expect(lastCodeEditorProps?.walkthrough).toEqual({
        walkthroughTitle: "Auth tour",
        index: 0,
        count: 2,
        step: w.steps[0],
        notice: undefined,
        playing: false,
      });

      // Different rootIndex -> null
      rerenderState({ selectedPath: "1:src/a.ts" });
      expect(lastCodeEditorProps?.walkthrough).toBeNull();

      // Different relPath -> null
      rerenderState({ selectedPath: "0:src/b.ts" });
      expect(lastCodeEditorProps?.walkthrough).toBeNull();

      // Out-of-range step -> null
      vi.mocked(useOpenWalkthrough).mockReturnValue(sampleWalkthrough({ step: 99 }));
      rerenderState({ selectedPath: "0:src/a.ts" });
      expect(lastCodeEditorProps?.walkthrough).toBeNull();
    });

    it("handles walkthrough prev, next, close, pause, togglePlay, and ask", async () => {
      const w0 = sampleWalkthrough({ step: 0, playing: false });
      vi.mocked(useOpenWalkthrough).mockReturnValue(w0);
      vi.mocked(goToStep).mockImplementation(async (ch, _idx, openFn) => {
        await openFn(ch, "src/a.ts");
      });

      mount();
      const handlers = lastCodeEditorProps!.walkthroughHandlers;

      // When getOpenWalkthrough returns undefined
      vi.mocked(getOpenWalkthrough).mockReturnValue(undefined);
      handlers.prev();
      handlers.togglePlay();
      handlers.ask({ start_line: 1, end_line: 2, text: "hi" });
      expect(goToStep).not.toHaveBeenCalled();
      expect(askAgent).not.toHaveBeenCalled();

      // prev / next / close / pause when walkthrough is open
      vi.mocked(getOpenWalkthrough).mockReturnValue(w0);
      handlers.next();
      expect(pauseWalkthrough).toHaveBeenCalledWith("ch-1");
      expect(goToStep).toHaveBeenCalledWith("ch-1", 1, expect.any(Function));
      expect(openChannelFile).toHaveBeenCalledWith("ch-1", "src/a.ts");

      handlers.prev();
      expect(goToStep).toHaveBeenCalledWith("ch-1", -1, expect.any(Function));

      handlers.close();
      expect(closeWalkthrough).toHaveBeenCalledWith("ch-1");

      handlers.pause();
      expect(pauseWalkthrough).toHaveBeenCalledWith("ch-1");

      // togglePlay: not playing & before last step -> playWalkthrough
      handlers.togglePlay();
      expect(playWalkthrough).toHaveBeenCalledWith("ch-1", expect.any(Function));

      // togglePlay: currently playing -> pauseWalkthrough
      vi.mocked(getOpenWalkthrough).mockReturnValue(sampleWalkthrough({ step: 0, playing: true }));
      handlers.togglePlay();
      expect(pauseWalkthrough).toHaveBeenCalledWith("ch-1");

      // togglePlay: not playing & on last step -> goToStep(0) then playWalkthrough
      vi.mocked(playWalkthrough).mockClear();
      vi.mocked(getOpenWalkthrough).mockReturnValue(sampleWalkthrough({ step: 1, playing: false }));
      handlers.togglePlay();
      await waitFor(() => {
        expect(playWalkthrough).toHaveBeenCalledWith("ch-1", expect.any(Function));
      });

      // ask: with step.title present
      vi.mocked(getOpenWalkthrough).mockReturnValue(sampleWalkthrough({ step: 0 }));
      handlers.ask({ start_line: 1, end_line: 2, text: "code" });
      expect(askAgent).toHaveBeenCalledWith("ch-1", askMessage("src/a.ts", 1, 2, "code", "Step 1"));

      // ask: with step.title empty -> falls back to walkthrough title
      vi.mocked(getOpenWalkthrough).mockReturnValue(sampleWalkthrough({ step: 1 }));
      handlers.ask({ start_line: 5, end_line: 6, text: "more" });
      expect(askAgent).toHaveBeenCalledWith("ch-1", askMessage("src/b.ts", 5, 6, "more", "Auth tour"));

      // ask: out-of-bounds step -> no-op
      vi.mocked(askAgent).mockClear();
      vi.mocked(getOpenWalkthrough).mockReturnValue(sampleWalkthrough({ step: 5 }));
      handlers.ask({ start_line: 1, end_line: 1, text: "x" });
      expect(askAgent).not.toHaveBeenCalled();
    });

    it("reports editor selection, handles askAgent from editor, and clears selection on unmount", () => {
      const { rerenderState, unmount } = mount({ openTabs: ["0:src/a.ts"], selectedPath: "0:src/a.ts" });

      // Selection with active tab (and fallback to dirPath when root is unknown)
      lastCodeEditorProps?.onSelectionChange({ start_line: 2, end_line: 4, text: "abc" });
      expect(setEditorSelection).toHaveBeenCalledWith("ch-1", { path: "/w/app/src/a.ts", start_line: 2, end_line: 4, text: "abc" });

      rerenderState({ openTabs: ["9:fallback.ts"], selectedPath: "9:fallback.ts" });
      lastCodeEditorProps?.onSelectionChange({ start_line: 1, end_line: 1, text: "z" });
      expect(setEditorSelection).toHaveBeenCalledWith("ch-1", { path: "/w/app/fallback.ts", start_line: 1, end_line: 1, text: "z" });

      rerenderState({ openTabs: ["0:src/a.ts"], selectedPath: "0:src/a.ts" });

      // Null selection
      lastCodeEditorProps?.onSelectionChange(null);
      expect(setEditorSelection).toHaveBeenCalledWith("ch-1", null);

      // Ask agent with active tab
      lastCodeEditorProps?.onAskAgent({ start_line: 2, end_line: 4, text: "abc" });
      expect(askAgent).toHaveBeenCalledWith("ch-1", askMessage("/w/app/src/a.ts", 2, 4, "abc"));

      // With no active tab -> selection sets null, askAgent is no-op
      vi.mocked(askAgent).mockClear();
      rerenderState({ selectedPath: null });
      lastCodeEditorProps?.onSelectionChange({ start_line: 1, end_line: 1, text: "x" });
      expect(setEditorSelection).toHaveBeenLastCalledWith("ch-1", null);
      lastCodeEditorProps?.onAskAgent({ start_line: 1, end_line: 1, text: "x" });
      expect(askAgent).not.toHaveBeenCalled();

      // Unmount clears selection
      vi.mocked(setEditorSelection).mockClear();
      unmount();
      expect(setEditorSelection).toHaveBeenCalledWith("ch-1", null);
    });
  });
});
