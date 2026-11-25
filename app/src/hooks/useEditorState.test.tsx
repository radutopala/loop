import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type DiffResponse, fetchDiff } from "../api/git";
import {
  buildFileUrl,
  CopyTargetExistsError,
  clearContentCaps,
  copyFile,
  createDir,
  DEFAULT_EDITOR_CONFIG,
  deleteFile,
  type EditorConfig,
  FileNotFoundError,
  fetchEditorConfig,
  fetchFileContent,
  fetchFiles,
  fetchRoots,
  forgetContentCap,
  type RootEntry,
  renameFile,
  saveFileContent,
  updateExtraDirs,
} from "../api/loopApi";
import type { CodeEditorHandle } from "../components/panels/CodeEditor";
import { type FakeWebSocket, fakeWebSocketClass } from "../test/hooks-a-ws";
import type { WSEvent } from "../types";
import type { ChatEventListener } from "./useChatStateStore";
import { useEditorState } from "./useEditorState";

vi.mock("../api/loopApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/loopApi")>()),
  buildFileUrl: vi.fn(),
  copyFile: vi.fn(),
  createDir: vi.fn(),
  deleteFile: vi.fn(),
  fetchEditorConfig: vi.fn(),
  fetchFileContent: vi.fn(),
  fetchFiles: vi.fn(),
  fetchRoots: vi.fn(),
  forgetContentCap: vi.fn(),
  renameFile: vi.fn(),
  saveFileContent: vi.fn(),
  updateExtraDirs: vi.fn(),
}));
vi.mock("../api/git", () => ({ fetchDiff: vi.fn() }));

const CH = "ch1";
const ROOTS: RootEntry[] = [
  { index: 0, path: "/w", name: "w" },
  { index: 1, path: "/x", name: "x" },
];
const NO_DIFF: DiffResponse = {
  files: [],
  diff: "",
  staged_diff: "",
  unstaged_diff: "",
  untracked_diff: "",
  total_additions: 0,
  total_deletions: 0,
};
const A_DIFF = "diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1,1 +1,2 @@\n x\n+y\n";

/** An editor view stand-in holding its document as plain text. */
class FakeEditor implements CodeEditorHandle {
  constructor(public content: string | null) {}
  getContent = vi.fn(() => this.content);
  replaceContent = vi.fn((c: string) => {
    this.content = c;
  });
  appendNewlineIfMissing = vi.fn();
  selectAll = vi.fn();
  openSearch = vi.fn();
  getSelection = vi.fn(() => null);
  replaceSelection = vi.fn();
  scrollToLine = vi.fn();
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Lets pending promise chains settle. */
const flush = () =>
  act(async () => {
    await new Promise((r) => setTimeout(r, 0));
  });

function persistTabs(state: { tabs: string[]; selected: string | null; preview?: string | null }, key = "loop-editor-tabs", channel = CH) {
  localStorage.setItem(key, JSON.stringify({ [channel]: state }));
}

function withConfig(over: Partial<EditorConfig>) {
  vi.mocked(fetchEditorConfig).mockResolvedValue({ ...DEFAULT_EDITOR_CONFIG, ...over });
}

describe("useEditorState", () => {
  let sockets: FakeWebSocket[];
  let warn: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    const fake = fakeWebSocketClass();
    sockets = fake.sockets;
    vi.stubGlobal("WebSocket", fake.WebSocket);
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      cb(0);
      return 1;
    });
    warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    vi.mocked(fetchRoots).mockResolvedValue(ROOTS);
    vi.mocked(fetchFiles).mockResolvedValue([]);
    vi.mocked(fetchFileContent).mockResolvedValue({ content: "disk", binary: false });
    vi.mocked(saveFileContent).mockResolvedValue({});
    vi.mocked(fetchEditorConfig).mockResolvedValue(DEFAULT_EDITOR_CONFIG);
    vi.mocked(buildFileUrl).mockImplementation(async (_ch, p, root, t) => `url:${root}:${p}?t=${t}`);
    vi.mocked(fetchDiff).mockResolvedValue(NO_DIFF);
    vi.mocked(deleteFile).mockResolvedValue(undefined);
    vi.mocked(createDir).mockResolvedValue(undefined);
    vi.mocked(renameFile).mockResolvedValue(undefined);
    vi.mocked(copyFile).mockResolvedValue(undefined);
    vi.mocked(updateExtraDirs).mockResolvedValue(undefined);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const watchSocket = () => sockets.find((s) => s.url.endsWith(`/api/channels/${CH}/files/watch`))!;

  async function mount(options?: Parameters<typeof useEditorState>[1], channel = CH) {
    const hook = renderHook(() => useEditorState(channel, options));
    await flush();
    return hook;
  }

  /** Mounts with an editor attached and the given file open as a kept tab. */
  async function mountWithOpen(pathKey: string, content = "disk", options?: Parameters<typeof useEditorState>[1]) {
    const hook = await mount(options);
    const editor = new FakeEditor(content);
    hook.result.current.codeEditorRef.current = editor;
    vi.mocked(fetchFileContent).mockResolvedValueOnce({ content, binary: false });
    act(() => hook.result.current.promoteFile(pathKey));
    await flush();
    return { ...hook, editor };
  }

  describe("roots, config and tree", () => {
    it("loads the roots and editor config, and lists each root once a tree shows", async () => {
      withConfig({ preview_tabs: false, auto_save_on_blur: true });
      const { result } = await mount();
      expect(fetchRoots).toHaveBeenCalledWith(CH);
      expect(result.current.roots).toEqual(ROOTS);
      expect(result.current.previewTabsEnabled).toBe(false);
      expect(result.current.autoSaveOnBlur).toBe(true);
      expect(result.current.editorConfig.auto_save_on_blur).toBe(true);
      expect(fetchFiles).not.toHaveBeenCalled();

      vi.mocked(fetchFiles).mockResolvedValue([{ name: "a.ts", is_dir: false } as never]);
      act(() => result.current.showTree());
      await flush();
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 1);
      expect(result.current.dirContents.get("0:")).toEqual([{ name: "a.ts", is_dir: false }]);
      expect(result.current.dirContents.has("1:")).toBe(true);
    });

    it("lists only the first root when the roots can't be read", async () => {
      vi.mocked(fetchRoots).mockRejectedValue(new Error("down"));
      const { result } = await mount();
      act(() => result.current.showTree());
      await flush();
      expect(fetchFiles).toHaveBeenCalledTimes(1);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
    });

    it("logs a config that can't be read and reads it again on focus", async () => {
      vi.mocked(fetchEditorConfig).mockRejectedValueOnce(new Error("nope"));
      const { result } = await mount();
      expect(warn).toHaveBeenCalledWith("[loop] loading editor settings:", expect.any(Error));
      expect(result.current.editorConfig).toEqual(DEFAULT_EDITOR_CONFIG);
      withConfig({ tab_size: 2 });
      act(() => {
        window.dispatchEvent(new Event("focus"));
      });
      await flush();
      expect(result.current.editorConfig.tab_size).toBe(2);
    });

    it("loads a directory under its key and ignores one that fails", async () => {
      const { result } = await mount();
      vi.mocked(fetchFiles).mockResolvedValueOnce([{ name: "b", is_dir: true } as never]);
      await act(() => result.current.loadDir("src", 1));
      expect(result.current.dirContents.get("1:src")).toEqual([{ name: "b", is_dir: true }]);
      vi.mocked(fetchFiles).mockRejectedValueOnce(new Error("gone"));
      await act(() => result.current.loadDir("missing"));
      expect(result.current.dirContents.has("0:missing")).toBe(false);
    });

    it("toggles a directory, loading it only the first time", async () => {
      const { result } = await mount();
      expect(result.current.expandedDirs.has("0:")).toBe(true);
      act(() => result.current.toggleDir("0:"));
      expect(result.current.expandedDirs.has("0:")).toBe(false);
      act(() => result.current.toggleDir("1:"));
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 1);
      await flush();
      act(() => result.current.toggleDir("0:src"));
      expect(fetchFiles).toHaveBeenCalledWith(CH, "src", 0);
      await flush();
      act(() => result.current.toggleDir("0:src"));
      vi.mocked(fetchFiles).mockClear();
      act(() => result.current.toggleDir("0:src"));
      expect(fetchFiles).not.toHaveBeenCalled();
      expect(result.current.expandedDirs.has("0:src")).toBe(true);
    });

    it("reveals a directory: every folder above it opens and it's selected", async () => {
      const { result } = await mount();
      act(() => result.current.showTree());
      await flush();
      vi.mocked(fetchFiles).mockClear();
      act(() => result.current.revealDir("0:a/b"));
      await flush();
      expect(fetchFiles).toHaveBeenCalledTimes(2);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "a", 0);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "a/b", 0);
      expect([...result.current.expandedDirs]).toEqual(expect.arrayContaining(["0:", "0:a", "0:a/b"]));
      expect(result.current.selectedDir).toBe("0:a/b");

      const before = result.current.expandedDirs;
      vi.mocked(fetchFiles).mockClear();
      act(() => result.current.revealDir("0:a"));
      expect(fetchFiles).not.toHaveBeenCalled();
      expect(result.current.expandedDirs).toBe(before);
      expect(result.current.selectedDir).toBe("0:a");
    });

    it("reveals a file's folder and counts repeated reveals", async () => {
      const { result } = await mount();
      expect(result.current.revealRequest).toBeNull();
      act(() => result.current.revealFile("1:d/f.ts"));
      expect(result.current.revealRequest).toEqual({ path: "1:d/f.ts", seq: 1 });
      expect(result.current.selectedDir).toBe("1:d");
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 1);
      act(() => result.current.revealFile("1:d/f.ts"));
      expect(result.current.revealRequest).toEqual({ path: "1:d/f.ts", seq: 2 });
      act(() => result.current.setSelectedDir("0:"));
      expect(result.current.selectedDir).toBe("0:");
    });

    it("adds an extra dir after the existing ones and lists the new roots", async () => {
      const { result } = await mount();
      const next = [...ROOTS, { index: 2, path: "/y", name: "y" }];
      vi.mocked(fetchRoots).mockResolvedValueOnce(next);
      await act(() => result.current.addExtraDir("/y"));
      expect(updateExtraDirs).toHaveBeenCalledWith(CH, ["/x", "/y"]);
      expect(result.current.roots).toEqual(next);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 2);
    });
  });

  describe("persisted tabs", () => {
    it("starts empty with nothing saved, or only other channels' tabs saved", async () => {
      persistTabs({ tabs: ["0:a.ts"], selected: "0:a.ts" }, "loop-editor-tabs", "other");
      const { result } = await mount();
      expect(result.current.openTabs).toEqual([]);
      expect(result.current.selectedPath).toBeNull();
      expect(result.current.previewTab).toBeNull();
      expect(fetchFileContent).not.toHaveBeenCalled();
      expect(result.current.gitChanges.changed.size).toBe(0);
    });

    it("restores the tabs, preview and selection, and loads the selected file", async () => {
      persistTabs({ tabs: ["0:a.ts", "0:b.ts"], selected: "0:a.ts", preview: "0:b.ts" });
      vi.mocked(fetchDiff).mockResolvedValue({ ...NO_DIFF, unstaged_diff: A_DIFF });
      const { result } = await mount();
      expect(result.current.openTabs).toEqual(["0:a.ts", "0:b.ts"]);
      expect(result.current.previewTab).toBe("0:b.ts");
      expect(result.current.selectedPath).toBe("0:a.ts");
      expect(fetchFileContent).toHaveBeenCalledWith(CH, "a.ts", 0);
      expect(result.current.fileContent).toBe("disk");
      expect(result.current.loading).toBe(false);
      expect(fetchDiff).toHaveBeenCalledWith(CH, undefined, undefined, 0);
      expect(result.current.gitChanges.changed.get(2)).toBe("added");
    });

    it("drops a saved preview that isn't among the tabs, under a custom key", async () => {
      persistTabs({ tabs: ["0:a.ts"], selected: "0:a.ts", preview: "0:gone.ts" }, "custom");
      const { result } = await mount({ tabsStorageKey: "custom" });
      expect(result.current.openTabs).toEqual(["0:a.ts"]);
      expect(result.current.previewTab).toBeNull();
      expect(JSON.parse(localStorage.getItem("custom")!)).toEqual({ [CH]: { tabs: ["0:a.ts"], selected: "0:a.ts", preview: null } });
      expect(localStorage.getItem("loop-editor-tabs")).toBeNull();
    });

    it("shows a saved binary tab as binary", async () => {
      persistTabs({ tabs: ["0:a.bin"], selected: "0:a.bin" });
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "", binary: true });
      const { result } = await mount();
      expect(result.current.isBinary).toBe(true);
      expect(result.current.binarySize).toBe(0);
      expect(result.current.fileContent).toBeNull();
    });

    it("marks a saved tab whose file is gone as deleted", async () => {
      persistTabs({ tabs: ["0:a.ts"], selected: "0:a.ts" });
      vi.mocked(fetchFileContent).mockRejectedValue(new FileNotFoundError("missing"));
      const { result } = await mount();
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(true);
      expect(result.current.fileContent).toBe("");
      expect(result.current.error).toBeNull();
    });

    it("shows why a saved tab couldn't load", async () => {
      persistTabs({ tabs: ["0:a.ts"], selected: "0:a.ts" });
      vi.mocked(fetchFileContent).mockRejectedValue(new Error("denied"));
      const { result } = await mount();
      expect(result.current.error).toBe("denied");
      expect(result.current.loading).toBe(false);
    });

    it("falls back to a generic error for a non-Error failure", async () => {
      persistTabs({ tabs: ["0:a.ts"], selected: "0:a.ts" });
      vi.mocked(fetchFileContent).mockRejectedValue("bad");
      const { result } = await mount();
      expect(result.current.error).toBe("Failed to load file");
    });

    it("shows a saved media tab through a content link, without git markers", async () => {
      persistTabs({ tabs: ["1:pic.png"], selected: "1:pic.png" });
      const { result } = await mount();
      expect(buildFileUrl).toHaveBeenCalledWith(CH, "pic.png", 1, 0);
      expect(result.current.imageURL).toBe("url:1:pic.png?t=0");
      expect(result.current.fileContent).toBeNull();
      expect(fetchFileContent).not.toHaveBeenCalled();
      expect(fetchDiff).not.toHaveBeenCalled();
    });

    it("saves the tabs as they change and forgets the channel once none are open", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      expect(JSON.parse(localStorage.getItem("loop-editor-tabs")!)).toEqual({ [CH]: { tabs: ["0:a.ts"], selected: "0:a.ts", preview: null } });
      act(() => result.current.closeTab("0:a.ts"));
      expect(JSON.parse(localStorage.getItem("loop-editor-tabs")!)).toEqual({});
    });
  });

  describe("media", () => {
    it("shows no link when one can't be minted", async () => {
      vi.mocked(buildFileUrl).mockRejectedValue(new Error("down"));
      const { result } = await mount();
      act(() => result.current.switchToTab("0:a.png"));
      await flush();
      expect(result.current.imageURL).toBeNull();
      expect(result.current.loading).toBe(false);
    });

    it("keeps only the newest tab's link when an older one settles late", async () => {
      const first = deferred<string>();
      const second = deferred<string>();
      vi.mocked(buildFileUrl).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
      const { result } = await mount();
      act(() => result.current.switchToTab("0:a.png"));
      act(() => result.current.switchToTab("0:b.png"));
      second.resolve("b-url");
      await flush();
      first.resolve("a-url");
      await flush();
      expect(result.current.imageURL).toBe("b-url");
    });

    it("ignores a late failure for a tab no longer shown", async () => {
      const first = deferred<string>();
      vi.mocked(buildFileUrl).mockReturnValueOnce(first.promise);
      const { result } = await mount();
      act(() => result.current.switchToTab("0:a.png"));
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      first.reject(new Error("late"));
      await flush();
      expect(result.current.imageURL).toBeNull();
      expect(result.current.fileContent).toBe("disk");
    });

    it("retries a failed media load once per shown file, with a fresh link", async () => {
      const { result } = await mount();
      act(() => result.current.retryMedia());
      expect(forgetContentCap).not.toHaveBeenCalled();
      act(() => result.current.switchToTab("1:v.mp4"));
      await flush();
      vi.mocked(buildFileUrl).mockClear();
      act(() => result.current.retryMedia());
      expect(forgetContentCap).toHaveBeenCalledWith({ kind: "raw", channelId: CH, root: 1 });
      expect(buildFileUrl).toHaveBeenCalledWith(CH, "v.mp4", 1, 0);
      act(() => result.current.retryMedia());
      expect(forgetContentCap).toHaveBeenCalledTimes(1);
    });

    it("mints the shown file's link again when every link is forgotten", async () => {
      const { result } = await mount();
      act(() => clearContentCaps());
      expect(buildFileUrl).not.toHaveBeenCalled();
      act(() => result.current.switchToTab("0:doc.pdf"));
      await flush();
      vi.mocked(buildFileUrl).mockClear();
      act(() => clearContentCaps());
      expect(buildFileUrl).toHaveBeenCalledWith(CH, "doc.pdf", 0, 0);
    });
  });

  describe("diff against HEAD", () => {
    it("does nothing with no tab selected", async () => {
      const { result } = await mount();
      act(() => result.current.toggleHeadCompare());
      expect(fetchFileContent).not.toHaveBeenCalled();
    });

    it("shows the file at HEAD and hides it again", async () => {
      const { result } = await mountWithOpen("1:a.ts");
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "head", binary: false });
      act(() => result.current.toggleHeadCompare());
      await flush();
      expect(fetchFileContent).toHaveBeenLastCalledWith(CH, "a.ts", 1, "HEAD");
      expect(result.current.headCompare).toEqual({ pathKey: "1:a.ts", content: "head" });
      expect(result.current.notice).toBeNull();
      act(() => result.current.toggleHeadCompare());
      expect(result.current.headCompare).toBeNull();
    });

    it("hides the comparison when another tab is selected", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      act(() => result.current.toggleHeadCompare());
      await flush();
      expect(result.current.headCompare).not.toBeNull();
      act(() => result.current.switchToTab("0:b.ts"));
      expect(result.current.headCompare).toBeNull();
    });

    it.each([
      ["a binary file", () => Promise.resolve({ content: "", binary: true }), "The file at HEAD is binary."],
      ["a file not in HEAD", () => Promise.reject(new FileNotFoundError("x")), "This file isn't in HEAD: it's new, or not in a git repository."],
      ["a read failure", () => Promise.reject(new Error("x")), "Couldn't read the file at HEAD."],
    ])("gives a notice for %s", async (_name, result$, message) => {
      const { result } = await mountWithOpen("0:a.ts");
      vi.mocked(fetchFileContent).mockImplementationOnce(result$);
      act(() => result.current.toggleHeadCompare());
      await flush();
      expect(result.current.notice).toEqual({ pathKey: "0:a.ts", message });
      expect(result.current.headCompare).toBeNull();
      act(() => result.current.dismissNotice());
      expect(result.current.notice).toBeNull();
    });

    it("drops a HEAD read that settles after another tab is selected", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      const ok = deferred<{ content: string; binary: boolean }>();
      vi.mocked(fetchFileContent).mockReturnValueOnce(ok.promise);
      act(() => result.current.toggleHeadCompare());
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      ok.resolve({ content: "head", binary: false });
      await flush();
      expect(result.current.headCompare).toBeNull();

      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      const fail = deferred<{ content: string; binary: boolean }>();
      vi.mocked(fetchFileContent).mockReturnValueOnce(fail.promise);
      act(() => result.current.toggleHeadCompare());
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      fail.reject(new Error("late"));
      await flush();
      expect(result.current.notice).toBeNull();
    });

    it("clears an active notice when switching to another tab", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      act(() => result.current.promoteFile("0:b.ts"));
      await flush();
      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "", binary: true });
      act(() => result.current.toggleHeadCompare());
      await flush();
      expect(result.current.notice).toEqual({ pathKey: "0:a.ts", message: "The file at HEAD is binary." });
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      expect(result.current.notice).toBeNull();
    });
  });

  describe("git gutter", () => {
    it("drops a diff that settles after another tab is selected, and clears on failure", async () => {
      const { result } = await mount();
      const late = deferred<typeof NO_DIFF>();
      vi.mocked(fetchDiff).mockReturnValueOnce(late.promise).mockRejectedValueOnce(new Error("no git"));
      act(() => result.current.switchToTab("0:a.ts"));
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      late.resolve({ ...NO_DIFF, staged_diff: A_DIFF });
      await flush();
      expect(result.current.gitChanges.changed.size).toBe(0);
    });

    it("clears the markers when no tab is left", async () => {
      vi.mocked(fetchDiff).mockResolvedValue({ ...NO_DIFF, untracked_diff: A_DIFF });
      const { result } = await mountWithOpen("0:a.ts");
      expect(result.current.gitChanges.changed.size).toBe(1);
      act(() => result.current.closeTab("0:a.ts"));
      await flush();
      expect(result.current.gitChanges.changed.size).toBe(0);
    });
  });

  describe("editing and saving", () => {
    it("marks the selected tab dirty, leaving the preview and taking it out of preview", async () => {
      const { result } = await mount();
      act(() => result.current.markDirty());
      expect(result.current.dirtyTabs.size).toBe(0);
      act(() => result.current.openFile("0:a.ts"));
      await flush();
      expect(result.current.previewTab).toBe("0:a.ts");
      act(() => result.current.markDirty());
      expect(result.current.dirtyTabs.has("0:a.ts")).toBe(true);
      expect(result.current.previewTab).toBeNull();
      const dirty = result.current.dirtyTabs;
      act(() => result.current.markDirty());
      expect(result.current.dirtyTabs).toBe(dirty);
    });

    it("leaves another tab's preview when the selected one is edited", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      act(() => result.current.openFile("0:b.ts"));
      await flush();
      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      act(() => result.current.markDirty());
      expect(result.current.previewTab).toBe("0:b.ts");
    });

    it("does nothing to save without a path, an editor, the tab on screen or content", async () => {
      const { result } = await mount();
      act(() => result.current.saveFile());
      act(() => result.current.saveFile("0:a.ts"));
      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      const editor = new FakeEditor(null);
      result.current.codeEditorRef.current = editor;
      act(() => result.current.saveFile("0:b.ts"));
      act(() => result.current.saveFile());
      expect(editor.appendNewlineIfMissing).toHaveBeenCalledTimes(1);
      expect(saveFileContent).not.toHaveBeenCalled();
    });

    it("saves the selected tab with formatting and takes the formatted text", async () => {
      const { result, editor } = await mountWithOpen("1:a.ts", "x");
      act(() => result.current.markDirty());
      vi.mocked(saveFileContent).mockResolvedValueOnce({ content: "x;\n" });
      vi.mocked(fetchDiff).mockClear();
      act(() => result.current.saveFile());
      await flush();
      expect(editor.appendNewlineIfMissing).toHaveBeenCalled();
      expect(saveFileContent).toHaveBeenCalledWith(CH, "a.ts", "x", 1, true);
      expect(editor.replaceContent).toHaveBeenCalledWith("x;\n");
      expect(result.current.dirtyTabs.size).toBe(0);
      expect(fetchDiff).toHaveBeenCalledTimes(1);
    });

    it("keeps edits made while the save ran", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "x");
      const save = deferred<{ content?: string }>();
      vi.mocked(saveFileContent).mockReturnValueOnce(save.promise);
      act(() => result.current.saveFile());
      editor.content = "xy";
      save.resolve({ content: "x;" });
      await flush();
      expect(editor.replaceContent).not.toHaveBeenCalled();
    });

    it("doesn't touch the editor or the gutter when the saved tab is no longer selected", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "x");
      const save = deferred<{ content?: string }>();
      vi.mocked(saveFileContent).mockReturnValueOnce(save.promise);
      act(() => result.current.saveFile());
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      vi.mocked(fetchDiff).mockClear();
      save.resolve({ content: "x;" });
      await flush();
      expect(editor.replaceContent).not.toHaveBeenCalled();
      expect(fetchDiff).not.toHaveBeenCalled();
    });

    it("tells why formatting failed, and clears that once a save formats", async () => {
      const { result } = await mountWithOpen("0:a.ts", "x");
      vi.mocked(saveFileContent).mockResolvedValueOnce({ formatError: "no formatter" });
      act(() => result.current.saveFile());
      await flush();
      expect(result.current.notice).toEqual({ pathKey: "0:a.ts", message: "Saved without formatting: no formatter" });
      act(() => result.current.saveFile());
      await flush();
      expect(result.current.notice).toBeNull();
    });

    it("logs a save that fails and keeps the tab dirty", async () => {
      const { result } = await mountWithOpen("0:a.ts", "x");
      act(() => result.current.markDirty());
      vi.mocked(saveFileContent).mockRejectedValueOnce(new Error("disk full"));
      act(() => result.current.saveFile());
      await flush();
      expect(warn).toHaveBeenCalledWith("[loop] saving file:", expect.any(Error));
      expect(result.current.dirtyTabs.has("0:a.ts")).toBe(true);
    });

    it("saves all dirty tabs only when the selected one is dirty", async () => {
      const { result } = await mount();
      act(() => result.current.saveAllDirty());
      const { result: r2 } = await mountWithOpen("0:a.ts", "x");
      act(() => r2.current.saveAllDirty());
      expect(saveFileContent).not.toHaveBeenCalled();
      act(() => r2.current.markDirty());
      act(() => r2.current.saveAllDirty());
      expect(saveFileContent).toHaveBeenCalledTimes(1);
      expect(result.current.dirtyTabs.size).toBe(0);
    });

    it("saves on Cmd+S or Ctrl+S, writing a deleted tab back even when clean", async () => {
      const { result } = await mountWithOpen("0:a.ts", "x");
      act(() => {
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "s" }));
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "x", metaKey: true }));
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "s", metaKey: true }));
      });
      expect(saveFileContent).not.toHaveBeenCalled();
      act(() => result.current.markDirty());
      const ev = new KeyboardEvent("keydown", { key: "s", ctrlKey: true, cancelable: true });
      act(() => {
        window.dispatchEvent(ev);
      });
      expect(ev.defaultPrevented).toBe(true);
      expect(saveFileContent).toHaveBeenCalledTimes(1);
      await flush();

      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("gone"));
      act(() => watchSocket().receive(JSON.stringify({ type: "file", root: 0, path: "a.ts" })));
      await flush();
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(true);
      act(() => {
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "s", metaKey: true }));
      });
      await flush();
      expect(saveFileContent).toHaveBeenCalledTimes(2);
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(false);
    });

    it("saves on blur only with auto-save on", async () => {
      const { result } = await mountWithOpen("0:a.ts", "x");
      act(() => result.current.markDirty());
      act(() => {
        window.dispatchEvent(new Event("blur"));
      });
      expect(saveFileContent).not.toHaveBeenCalled();

      withConfig({ auto_save_on_blur: true });
      act(() => {
        window.dispatchEvent(new Event("focus"));
      });
      await flush();
      act(() => {
        window.dispatchEvent(new Event("blur"));
      });
      expect(saveFileContent).toHaveBeenCalledWith(CH, "a.ts", "x", 0, true);
    });

    it("clears the error", async () => {
      persistTabs({ tabs: ["0:a.ts"], selected: "0:a.ts" });
      vi.mocked(fetchFileContent).mockRejectedValue(new Error("denied"));
      const { result } = await mount();
      act(() => result.current.clearError());
      expect(result.current.error).toBeNull();
    });
  });

  describe("switching tabs", () => {
    it("keeps a dirty tab's unsaved text and shows it again without a fetch", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.markDirty());
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      expect(result.current.fileContent).toBe("disk");
      vi.mocked(fetchFileContent).mockClear();
      editor.content = "b";
      act(() => result.current.switchToTab("0:a.ts"));
      expect(result.current.fileContent).toBe("mine");
      expect(fetchFileContent).not.toHaveBeenCalled();
    });

    it("saves a dirty tab it leaves with auto-save on", async () => {
      withConfig({ auto_save_on_blur: true });
      const { result } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.markDirty());
      act(() => result.current.switchToTab("0:b.ts"));
      expect(saveFileContent).toHaveBeenCalledWith(CH, "a.ts", "mine", 0, true);
    });

    it("can't keep a dirty tab's text without an editor or its content", async () => {
      const { result } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.markDirty());
      result.current.codeEditorRef.current = new FakeEditor(null);
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      expect(fetchFileContent).toHaveBeenLastCalledWith(CH, "a.ts", 0);

      act(() => result.current.markDirty());
      result.current.codeEditorRef.current = null;
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      vi.mocked(fetchFileContent).mockClear();
      act(() => result.current.switchToTab("0:a.ts"));
      expect(fetchFileContent).toHaveBeenCalledWith(CH, "a.ts", 0);
    });

    it("shows a binary file as binary", async () => {
      const { result } = await mount();
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "", binary: true });
      act(() => result.current.switchToTab("0:a.bin"));
      expect(result.current.loading).toBe(true);
      await flush();
      expect(result.current.isBinary).toBe(true);
      expect(result.current.fileContent).toBeNull();
      expect(result.current.loading).toBe(false);
    });

    it.each([
      ["a missing file as deleted", new FileNotFoundError("x"), null],
      ["the error for a failed read", new Error("denied"), "denied"],
      ["a generic error for a non-Error failure", "bad", "Failed to load file"],
    ])("shows %s", async (_name, err, message) => {
      const { result } = await mount();
      vi.mocked(fetchFileContent).mockRejectedValueOnce(err);
      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      expect(result.current.error).toBe(message);
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(message === null);
      expect(result.current.loading).toBe(false);
    });

    it("drops a fetch that settles after a later tab was opened", async () => {
      const { result } = await mount();
      const ok = deferred<{ content: string; binary: boolean }>();
      const fail = deferred<{ content: string; binary: boolean }>();
      const last = deferred<{ content: string; binary: boolean }>();
      vi.mocked(fetchFileContent).mockReturnValueOnce(ok.promise).mockReturnValueOnce(fail.promise).mockReturnValueOnce(last.promise);
      act(() => result.current.switchToTab("0:a.ts"));
      act(() => result.current.switchToTab("0:b.ts"));
      act(() => result.current.switchToTab("0:c.ts"));
      ok.resolve({ content: "a", binary: false });
      fail.reject(new Error("b"));
      await flush();
      expect(result.current.fileContent).toBeNull();
      expect(result.current.error).toBeNull();
      expect(result.current.loading).toBe(true);
      last.resolve({ content: "c", binary: false });
      await flush();
      expect(result.current.fileContent).toBe("c");
      expect(result.current.loading).toBe(false);
    });
  });

  describe("opening files", () => {
    it("opens files as kept tabs when preview tabs are off", async () => {
      withConfig({ preview_tabs: false });
      const { result } = await mount();
      act(() => result.current.openFile("0:a.ts"));
      act(() => result.current.openFile("0:b.ts"));
      await flush();
      expect(result.current.openTabs).toEqual(["0:a.ts", "0:b.ts"]);
      expect(result.current.previewTab).toBeNull();
      act(() => result.current.openFile("0:b.ts"));
      act(() => result.current.openFile("0:a.ts"));
      expect(result.current.openTabs).toEqual(["0:a.ts", "0:b.ts"]);
      expect(result.current.selectedPath).toBe("0:a.ts");
    });

    it("opens a single click in the preview tab, replacing the last preview", async () => {
      const { result } = await mount();
      act(() => result.current.openFile("0:a.ts"));
      act(() => result.current.openFile("0:b.ts"));
      await flush();
      expect(result.current.openTabs).toEqual(["0:b.ts"]);
      expect(result.current.previewTab).toBe("0:b.ts");
      expect(result.current.selectedPath).toBe("0:b.ts");
      act(() => result.current.openFile("0:b.ts"));
      expect(result.current.openTabs).toEqual(["0:b.ts"]);
    });

    it("selects a kept tab without making it the preview", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      act(() => result.current.openFile("0:b.ts"));
      await flush();
      act(() => result.current.openFile("0:a.ts"));
      expect(result.current.selectedPath).toBe("0:a.ts");
      expect(result.current.previewTab).toBe("0:b.ts");
      act(() => result.current.openFile("0:a.ts"));
      expect(result.current.openTabs).toEqual(["0:a.ts", "0:b.ts"]);
    });

    it("doesn't add a tab twice when opened twice from the same render", async () => {
      const { result } = await mount();
      const { openFile } = result.current;
      act(() => {
        openFile("0:a.ts");
        openFile("0:a.ts");
      });
      expect(result.current.openTabs).toEqual(["0:a.ts"]);
    });

    it("promotes a file to a kept tab", async () => {
      const { result } = await mount();
      act(() => result.current.openFile("0:a.ts"));
      act(() => result.current.promoteFile("0:a.ts"));
      expect(result.current.previewTab).toBeNull();
      expect(result.current.openTabs).toEqual(["0:a.ts"]);
      act(() => result.current.promoteFile("0:b.ts"));
      expect(result.current.openTabs).toEqual(["0:a.ts", "0:b.ts"]);
      expect(result.current.selectedPath).toBe("0:b.ts");
    });

    it("scrolls to a line once the file loads", async () => {
      const { result } = await mount();
      const editor = new FakeEditor("x");
      result.current.codeEditorRef.current = editor;
      act(() => result.current.openFileAtLine("0:a.ts", 7));
      expect(editor.scrollToLine).not.toHaveBeenCalled();
      await flush();
      expect(editor.scrollToLine).toHaveBeenCalledWith(7);
      expect(result.current.previewTab).toBe("0:a.ts");
    });

    it("scrolls at once when the file is already on screen, and pins with pin", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts");
      act(() => result.current.openFileAtLine("0:a.ts", 3, true));
      expect(editor.scrollToLine).toHaveBeenCalledWith(3);
      editor.scrollToLine.mockClear();
      act(() => result.current.openFileAtLine("0:a.ts", null));
      expect(editor.scrollToLine).not.toHaveBeenCalled();
    });

    it("waits for an editor before scrolling, and only for the file asked for", async () => {
      const { result } = await mount();
      act(() => result.current.openFileAtLine("0:a.ts", 4));
      await flush();
      const editor = new FakeEditor("x");
      result.current.codeEditorRef.current = editor;
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      expect(editor.scrollToLine).not.toHaveBeenCalled();
      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      expect(editor.scrollToLine).toHaveBeenCalledWith(4);
    });
  });

  describe("closing and moving tabs", () => {
    async function threeTabs() {
      const hook = await mountWithOpen("0:a.ts");
      act(() => hook.result.current.promoteFile("0:b.ts"));
      act(() => hook.result.current.promoteFile("0:c.ts"));
      await flush();
      return hook;
    }

    it("closes nothing for an empty list", async () => {
      const { result } = await threeTabs();
      const tabs = result.current.openTabs;
      act(() => result.current.closeTabs([]));
      expect(result.current.openTabs).toBe(tabs);
    });

    it("moves the selection to the next tab and drops what closed tabs held", async () => {
      const { result } = await threeTabs();
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      act(() => result.current.markDirty());
      act(() => watchSocket().open());
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "agent", binary: false });
      act(() => watchSocket().receive(JSON.stringify({ type: "file", root: 0, path: "b.ts" })));
      await flush();
      expect(result.current.pendingRefresh.has("0:b.ts")).toBe(true);
      act(() => result.current.closeTabs(["0:b.ts"]));
      expect(result.current.openTabs).toEqual(["0:a.ts", "0:c.ts"]);
      expect(result.current.selectedPath).toBe("0:c.ts");
      expect(result.current.dirtyTabs.size).toBe(0);
      expect(result.current.pendingRefresh.size).toBe(0);
    });

    it("keeps the selection when it stays open, and clears it when no tab is left", async () => {
      const { result } = await threeTabs();
      const stop = vi.fn();
      act(() => result.current.closeTab("0:a.ts", { stopPropagation: stop } as unknown as React.MouseEvent));
      expect(stop).toHaveBeenCalled();
      expect(result.current.selectedPath).toBe("0:c.ts");
      act(() => result.current.closeTabs(["0:b.ts", "0:c.ts"]));
      expect(result.current.openTabs).toEqual([]);
      expect(result.current.selectedPath).toBeNull();
      expect(result.current.fileContent).toBeNull();
    });

    it("closes the preview tab and saves the selected tab first with auto-save on", async () => {
      withConfig({ auto_save_on_blur: true });
      const { result } = await mountWithOpen("0:a.ts", "x");
      act(() => result.current.markDirty());
      act(() => result.current.closeTabs(["0:zz.ts"]));
      expect(saveFileContent).not.toHaveBeenCalled();
      act(() => result.current.openFile("0:p.ts"));
      await flush();
      act(() => result.current.switchToTab("0:a.ts"));
      act(() => result.current.closeTabs(["0:a.ts", "0:p.ts"]));
      expect(saveFileContent).toHaveBeenCalled();
      expect(result.current.previewTab).toBeNull();
      expect(result.current.openTabs).toEqual([]);
    });

    it("moves a tab in the strip", async () => {
      const { result } = await threeTabs();
      act(() => result.current.moveTab("0:a.ts", "0:c.ts", true));
      expect(result.current.openTabs).toEqual(["0:b.ts", "0:c.ts", "0:a.ts"]);
    });

    it("unpins a clean tab into the preview, closing the old preview", async () => {
      const { result } = await threeTabs();
      act(() => result.current.unpinFile("0:nope.ts"));
      expect(result.current.previewTab).toBeNull();
      act(() => result.current.unpinFile("0:a.ts"));
      expect(result.current.previewTab).toBe("0:a.ts");
      act(() => result.current.unpinFile("0:a.ts"));
      act(() => result.current.unpinFile("0:b.ts"));
      expect(result.current.previewTab).toBe("0:b.ts");
      expect(result.current.openTabs).toEqual(["0:b.ts", "0:c.ts"]);
    });

    it("doesn't unpin with preview tabs off", async () => {
      withConfig({ preview_tabs: false });
      const { result } = await mountWithOpen("0:a.ts");
      act(() => result.current.unpinFile("0:a.ts"));
      expect(result.current.previewTab).toBeNull();
    });
  });

  describe("refreshing the tree", () => {
    it("lists every root and expanded dir, and replaces the open file's changed text", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "old");
      act(() => result.current.toggleDir("1:src"));
      await flush();
      act(() => result.current.markDirty());
      vi.mocked(fetchFiles).mockClear();
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "new", binary: false });
      await act(() => result.current.refreshTree());
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 1);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "src", 1);
      expect(editor.replaceContent).toHaveBeenCalledWith("new");
      expect(result.current.dirtyTabs.size).toBe(0);

      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "newer", binary: false });
      await act(() => result.current.refreshTree());
      expect(editor.content).toBe("newer");
    });

    it("lists the first root when the roots can't be read, with no tab open", async () => {
      const { result } = await mount();
      vi.mocked(fetchRoots).mockRejectedValueOnce(new Error("down"));
      vi.mocked(fetchFiles).mockClear();
      await act(() => result.current.refreshTree());
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
      expect(fetchFileContent).not.toHaveBeenCalled();
    });

    it("reloads a media tab with a new version", async () => {
      const { result } = await mount();
      act(() => result.current.switchToTab("0:a.png"));
      await flush();
      await act(() => result.current.refreshTree());
      await flush();
      expect(buildFileUrl).toHaveBeenLastCalledWith(CH, "a.png", 0, 1);
      expect(result.current.imageURL).toBe("url:0:a.png?t=1");
    });

    it.each([
      ["the text is unchanged", "disk", { content: "disk", binary: false }],
      ["the editor has no document", null, { content: "new", binary: false }],
      ["the file is binary", "disk", { content: "", binary: true }],
    ])("leaves the editor alone when %s", async (_name, content, fetched) => {
      const { result, editor } = await mountWithOpen("0:a.ts", "disk");
      editor.content = content;
      vi.mocked(fetchFileContent).mockResolvedValueOnce(fetched);
      await act(() => result.current.refreshTree());
      expect(editor.replaceContent).not.toHaveBeenCalled();
    });

    it("leaves the editor alone when there's none, the tab changed, or the read fails", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "disk");
      result.current.codeEditorRef.current = null;
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "new", binary: false });
      await act(() => result.current.refreshTree());

      result.current.codeEditorRef.current = editor;
      const late = deferred<{ content: string; binary: boolean }>();
      vi.mocked(fetchFileContent).mockReturnValueOnce(late.promise);
      let done!: Promise<void>;
      act(() => {
        done = result.current.refreshTree();
      });
      await flush();
      act(() => result.current.switchToTab("0:b.ts"));
      late.resolve({ content: "new", binary: false });
      await act(() => done);

      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      vi.mocked(fetchFileContent).mockRejectedValueOnce(new Error("gone"));
      await act(() => result.current.refreshTree());
      expect(editor.replaceContent).not.toHaveBeenCalled();
    });
  });

  describe("creating and deleting", () => {
    it("creates a file in the selected dir's root and opens it", async () => {
      const { result } = await mount();
      act(() => result.current.handleCreateFile("  "));
      expect(saveFileContent).not.toHaveBeenCalled();
      act(() => result.current.setSelectedDir("1:x"));
      act(() => result.current.handleCreateFile(" new.ts "));
      await flush();
      expect(saveFileContent).toHaveBeenCalledWith(CH, "new.ts", "", 1);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 1);
      expect(result.current.openTabs).toEqual(["1:new.ts"]);
      expect(result.current.selectedPath).toBe("1:new.ts");
    });

    it("creates a file at a key's root and path, once in the tabs", async () => {
      const { result } = await mount();
      act(() => result.current.handleCreateFile("2:dir/f.ts"));
      await flush();
      act(() => result.current.handleCreateFile("2:dir/f.ts"));
      await flush();
      expect(saveFileContent).toHaveBeenCalledWith(CH, "dir/f.ts", "", 2);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "dir", 2);
      expect(result.current.openTabs).toEqual(["2:dir/f.ts"]);
    });

    it.each([
      [new Error("exists"), "exists"],
      ["bad", "Failed to create file"],
    ])("shows why a file couldn't be created (%s)", async (err, message) => {
      const { result } = await mount();
      vi.mocked(saveFileContent).mockRejectedValueOnce(err);
      act(() => result.current.handleCreateFile("a.ts"));
      await flush();
      expect(result.current.error).toBe(message);
    });

    it("creates a directory in the selected dir's root or a key's root", async () => {
      const { result } = await mount();
      act(() => result.current.handleCreateDirPath(""));
      expect(createDir).not.toHaveBeenCalled();
      act(() => result.current.handleCreateDirPath("d"));
      act(() => result.current.handleCreateDirPath("1:p/q"));
      await flush();
      expect(createDir).toHaveBeenCalledWith(CH, "d", 0);
      expect(createDir).toHaveBeenCalledWith(CH, "p/q", 1);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "p", 1);
    });

    it.each([
      [new Error("denied"), "denied"],
      [42, "Failed to create directory"],
    ])("shows why a directory couldn't be created (%s)", async (err, message) => {
      const { result } = await mount();
      vi.mocked(createDir).mockRejectedValueOnce(err);
      act(() => result.current.handleCreateDirPath("d"));
      await flush();
      expect(result.current.error).toBe(message);
    });

    it("deletes the selected file, moving to its neighbour, then the last one", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      act(() => result.current.promoteFile("0:d/b.ts"));
      await flush();
      act(() => result.current.markDirty());
      act(() => result.current.handleDeleteFilePath("0:d/b.ts"));
      await flush();
      expect(deleteFile).toHaveBeenCalledWith(CH, "d/b.ts", 0);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "d", 0);
      expect(result.current.openTabs).toEqual(["0:a.ts"]);
      expect(result.current.selectedPath).toBe("0:a.ts");
      expect(result.current.dirtyTabs.size).toBe(0);

      act(() => result.current.handleDeleteFilePath("0:a.ts"));
      await flush();
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
      expect(result.current.openTabs).toEqual([]);
      expect(result.current.selectedPath).toBeNull();
    });

    it("deletes a file that isn't selected, leaving the selection", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      act(() => result.current.handleDeleteFilePath("0:other.ts"));
      await flush();
      expect(result.current.selectedPath).toBe("0:a.ts");
      expect(result.current.openTabs).toEqual(["0:a.ts"]);
    });

    it.each([
      [new Error("busy"), "busy"],
      [null, "Failed to delete file"],
    ])("shows why a file couldn't be deleted (%s)", async (err, message) => {
      const { result } = await mount();
      vi.mocked(deleteFile).mockRejectedValueOnce(err);
      act(() => result.current.handleDeleteFilePath("0:a.ts"));
      await flush();
      expect(result.current.error).toBe(message);
    });

    it("deletes a directory, closing the tabs inside it", async () => {
      const { result } = await mountWithOpen("0:keep.ts");
      act(() => result.current.promoteFile("0:d/x.ts"));
      act(() => result.current.promoteFile("0:d"));
      await flush();
      act(() => result.current.handleDeleteDirPath("0:d"));
      await flush();
      expect(deleteFile).toHaveBeenCalledWith(CH, "d", 0);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
      expect(result.current.openTabs).toEqual(["0:keep.ts"]);
      expect(result.current.selectedPath).toBe("0:keep.ts");

      act(() => result.current.promoteFile("0:p/q/y.ts"));
      await flush();
      act(() => result.current.switchToTab("0:keep.ts"));
      act(() => result.current.handleDeleteDirPath("0:p/q"));
      await flush();
      expect(fetchFiles).toHaveBeenCalledWith(CH, "p", 0);
      expect(result.current.selectedPath).toBe("0:keep.ts");

      act(() => result.current.promoteFile("0:z/w.ts"));
      act(() => result.current.closeTab("0:keep.ts"));
      await flush();
      act(() => result.current.handleDeleteDirPath("0:z"));
      await flush();
      expect(result.current.openTabs).toEqual([]);
      expect(result.current.selectedPath).toBeNull();
    });

    it("deletes a directory with no tab selected", async () => {
      const { result } = await mount();
      act(() => result.current.handleDeleteDirPath("0:d"));
      await flush();
      expect(result.current.selectedPath).toBeNull();
    });

    it.each([
      [new Error("busy"), "busy"],
      [undefined, "Failed to delete directory"],
    ])("shows why a directory couldn't be deleted (%s)", async (err, message) => {
      const { result } = await mount();
      vi.mocked(deleteFile).mockRejectedValueOnce(err);
      act(() => result.current.handleDeleteDirPath("0:d"));
      await flush();
      expect(result.current.error).toBe(message);
    });
  });

  describe("renaming", () => {
    it("does nothing for an empty or unchanged name", async () => {
      const { result } = await mount();
      await act(() => result.current.handleRenamePath("0:a.ts", " / "));
      await act(() => result.current.handleRenamePath("0:a.ts", "/a.ts/"));
      expect(renameFile).not.toHaveBeenCalled();
    });

    it("moves the open tab and everything kept for it to the new name", async () => {
      const { result, editor } = await mountWithOpen("0:d/a.ts", "x");
      act(() => result.current.markDirty());
      editor.content = "unsaved";
      act(() => result.current.saveTabView("0:d/a.ts", { anchor: 1, head: 1, top: 0 }));
      act(() => result.current.toggleDir("0:d"));
      act(() => result.current.setSelectedDir("0:d"));
      await act(() => result.current.handleRenamePath("0:d/a.ts", "e/b.ts"));
      expect(renameFile).toHaveBeenCalledWith(CH, "d/a.ts", "e/b.ts", 0);
      expect(result.current.selectedPath).toBe("0:e/b.ts");
      expect(result.current.fileContent).toBe("unsaved");
      expect(result.current.openTabs).toEqual(["0:e/b.ts"]);
      expect(result.current.dirtyTabs.has("0:e/b.ts")).toBe(true);
      expect(result.current.tabViewFor("0:e/b.ts")).toEqual({ anchor: 1, head: 1, top: 0 });
      expect(fetchFiles).toHaveBeenCalledWith(CH, "d", 0);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "e", 0);
    });

    it("renames a directory holding the preview and a deleted tab, in the same parent", async () => {
      const { result } = await mountWithOpen("0:keep.ts");
      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => result.current.openFile("0:d/p.ts"));
      await flush();
      expect(result.current.deletedTabs.has("0:d/p.ts")).toBe(true);
      act(() => result.current.switchToTab("0:keep.ts"));
      await flush();
      vi.mocked(fetchFiles).mockClear();
      await act(() => result.current.handleRenamePath("0:d", "e"));
      expect(result.current.previewTab).toBe("0:e/p.ts");
      expect(result.current.openTabs).toEqual(["0:keep.ts", "0:e/p.ts"]);
      expect(result.current.selectedPath).toBe("0:keep.ts");
      expect(result.current.deletedTabs.size).toBe(0);
      expect(fetchFiles).toHaveBeenCalledTimes(1);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
    });

    it("hands nothing to the editor it can't read", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "x");
      editor.content = null;
      await act(() => result.current.handleRenamePath("0:a.ts", "b.ts"));
      expect(result.current.selectedPath).toBe("0:b.ts");
      expect(result.current.fileContent).toBe("x");
      result.current.codeEditorRef.current = null;
      await act(() => result.current.handleRenamePath("0:b.ts", "c.ts"));
      expect(result.current.selectedPath).toBe("0:c.ts");
    });

    it("renames with nothing selected and rejects with the reason", async () => {
      const { result } = await mount();
      await act(() => result.current.handleRenamePath("0:a.ts", "b.ts"));
      expect(result.current.selectedPath).toBeNull();
      vi.mocked(renameFile).mockRejectedValueOnce(new Error("taken"));
      await expect(result.current.handleRenamePath("0:a.ts", "b.ts")).rejects.toThrow("taken");
    });

    it("keeps unrelated deleted tabs marked when another path is renamed", async () => {
      const { result } = await mountWithOpen("0:a.ts");
      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => result.current.promoteFile("0:other.ts"));
      await flush();
      expect(result.current.deletedTabs.has("0:other.ts")).toBe(true);
      await act(() => result.current.handleRenamePath("0:a.ts", "renamed.ts"));
      expect(result.current.deletedTabs.has("0:other.ts")).toBe(true);
    });
  });

  describe("copying", () => {
    it("copies beside itself as a copy, counting past taken names", async () => {
      const { result } = await mount();
      vi.mocked(copyFile).mockRejectedValueOnce(new CopyTargetExistsError("x"));
      let key = "";
      await act(async () => {
        key = await result.current.handleCopyPath("1:d/a.txt", false, "1:d");
      });
      expect(copyFile).toHaveBeenNthCalledWith(1, CH, "d/a.txt", "d/a copy.txt", 1, 1);
      expect(copyFile).toHaveBeenNthCalledWith(2, CH, "d/a.txt", "d/a copy 2.txt", 1, 1);
      expect(key).toBe("1:d/a copy 2.txt");
      expect(fetchFiles).toHaveBeenCalledWith(CH, "d", 1);
      expect(result.current.selectedDir).toBe("1:d");
    });

    it("keeps the name in another directory, at a root's top level", async () => {
      const { result } = await mount();
      let key = "";
      await act(async () => {
        key = await result.current.handleCopyPath("0:d/a.txt", false, "1:");
      });
      expect(copyFile).toHaveBeenCalledWith(CH, "d/a.txt", "a.txt", 0, 1);
      expect(key).toBe("1:a.txt");
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 1);
    });

    it("gives up once every copy name is taken, and on other errors", async () => {
      const { result } = await mount();
      vi.mocked(copyFile).mockRejectedValue(new CopyTargetExistsError("taken"));
      await expect(result.current.handleCopyPath("0:dir", true, "0:")).rejects.toThrow("taken");
      expect(copyFile).toHaveBeenCalledTimes(100);
      expect(copyFile).toHaveBeenLastCalledWith(CH, "dir", "dir copy 100", 0, 0);
      vi.mocked(copyFile).mockReset().mockRejectedValue(new Error("denied"));
      await expect(result.current.handleCopyPath("0:dir", true, "0:")).rejects.toThrow("denied");
      expect(copyFile).toHaveBeenCalledTimes(1);
    });
  });

  describe("tab views", () => {
    it("restores saved views and drops a closed tab's", async () => {
      localStorage.setItem("loop-editor-tab-views", JSON.stringify({ [CH]: { "0:a.ts": { anchor: 2, head: 3, top: 1 } } }));
      persistTabs({ tabs: ["0:a.ts", "0:b.ts"], selected: "0:a.ts" });
      const { result } = await mount();
      expect(result.current.tabViewFor("0:a.ts")).toEqual({ anchor: 2, head: 3, top: 1 });
      act(() => result.current.saveTabView("0:b.ts", { anchor: 5, head: 5, top: 4 }));
      expect(JSON.parse(localStorage.getItem("loop-editor-tab-views")!)[CH]["0:b.ts"]).toEqual({ anchor: 5, head: 5, top: 4 });
      act(() => result.current.closeTab("0:a.ts"));
      expect(result.current.tabViewFor("0:a.ts")).toBeUndefined();
      expect(JSON.parse(localStorage.getItem("loop-editor-tab-views")!)[CH]).toEqual({ "0:b.ts": { anchor: 5, head: 5, top: 4 } });
    });
  });

  describe("refreshing from disk", () => {
    const fileChange = (path: string, root = 0) => JSON.stringify({ type: "file", root, path });

    it("asks to watch the open tabs and expanded dirs, as they change", async () => {
      const { result } = await mount();
      watchSocket().open();
      expect(JSON.parse(watchSocket().sent[0] as string)).toEqual({ type: "watch", files: [], dirs: [] });
      act(() => result.current.showTree());
      await flush();
      act(() => result.current.promoteFile("0:a.ts"));
      await flush();
      expect(JSON.parse(watchSocket().sent.at(-1) as string)).toEqual({ type: "watch", files: [{ root: 0, path: "a.ts" }], dirs: [{ root: 0, path: "" }] });
    });

    it("doesn't watch without a channel", async () => {
      await mount(undefined, "");
      expect(sockets).toHaveLength(0);
    });

    it("re-lists an expanded dir that changed and ignores other messages", async () => {
      const { result } = await mount();
      act(() => result.current.toggleDir("1:src"));
      await flush();
      vi.mocked(fetchFiles).mockClear();
      act(() => {
        watchSocket().receive("not json");
        watchSocket().receive(JSON.stringify({ type: "dir", root: 1, path: "other" }));
        watchSocket().receive(JSON.stringify({ type: "dir", root: 1, path: "src" }));
        watchSocket().receive(JSON.stringify({ type: "dir", root: 0, path: "" }));
      });
      expect(fetchFiles).toHaveBeenCalledTimes(2);
      expect(fetchFiles).toHaveBeenCalledWith(CH, "src", 1);
      expect(fetchFiles).toHaveBeenCalledWith(CH, ".", 0);
    });

    it("replaces a clean tab's changed text and marks it agent-edited", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "disk");
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "agent", binary: false });
      vi.mocked(fetchDiff).mockClear();
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(editor.replaceContent).toHaveBeenCalledWith("agent");
      expect(result.current.agentEditedTabs.has("0:a.ts")).toBe(true);
      expect(fetchDiff).toHaveBeenCalled();

      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "agent2", binary: false });
      editor.content = "agent2";
      const edited = result.current.agentEditedTabs;
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(editor.replaceContent).toHaveBeenCalledTimes(1);
      expect(result.current.agentEditedTabs).toBe(edited);
      act(() => result.current.clearAgentEdited("0:a.ts"));
      expect(result.current.agentEditedTabs.size).toBe(0);
      act(() => result.current.clearAgentEdited("0:a.ts"));
    });

    it("hands the text over when the editor has none or isn't there", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "disk");
      editor.content = null;
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "v1", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.fileContent).toBe("v1");
      result.current.codeEditorRef.current = null;
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "v2", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.fileContent).toBe("v2");
    });

    it("ignores unchanged bytes, binary files and closed tabs", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "disk");
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "x", binary: true });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      act(() => watchSocket().receive(fileChange("closed.ts")));
      await flush();
      expect(editor.replaceContent).not.toHaveBeenCalled();
      expect(result.current.agentEditedTabs.size).toBe(0);
    });

    it("offers the disk version for a dirty tab, resolved by Replace or Keep", async () => {
      const { result, editor } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.markDirty());
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "agent", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.pendingRefresh.get("0:a.ts")).toBe("agent");
      expect(editor.replaceContent).not.toHaveBeenCalled();

      act(() => result.current.setComparing("0:a.ts"));
      expect(result.current.comparing).toBe("0:a.ts");
      act(() => result.current.markDirty());
      expect(result.current.pendingRefresh.has("0:a.ts")).toBe(true);
      act(() => result.current.dismissPendingRefresh("0:a.ts"));
      expect(result.current.pendingRefresh.size).toBe(0);
      expect(result.current.comparing).toBeNull();
      act(() => result.current.dismissPendingRefresh("0:a.ts"));
      act(() => result.current.setComparing("0:a.ts"));
      expect(result.current.comparing).toBeNull();

      act(() => result.current.acceptPendingRefresh("0:a.ts"));
      expect(editor.replaceContent).not.toHaveBeenCalled();
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "agent2", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      act(() => result.current.acceptPendingRefresh("0:a.ts"));
      expect(editor.replaceContent).toHaveBeenCalledWith("agent2");
      expect(result.current.dirtyTabs.size).toBe(0);
      expect(result.current.pendingRefresh.size).toBe(0);
    });

    it("drops a pending version when editing or saving", async () => {
      const { result } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.markDirty());
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "agent", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      act(() => result.current.saveFile());
      await flush();
      expect(result.current.pendingRefresh.size).toBe(0);

      act(() => result.current.markDirty());
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "agent2", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.pendingRefresh.size).toBe(1);
      act(() => result.current.markDirty());
      expect(result.current.pendingRefresh.size).toBe(0);
    });

    it("accepts a pending version without an editor, or for a tab not on screen", async () => {
      const { result } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.markDirty());
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "agent", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      result.current.codeEditorRef.current = null;
      act(() => result.current.acceptPendingRefresh("0:a.ts"));
      expect(result.current.fileContent).toBe("agent");

      result.current.codeEditorRef.current = new FakeEditor("mine");
      act(() => result.current.markDirty());
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "agent2", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      act(() => result.current.switchToTab("0:b.ts"));
      await flush();
      act(() => result.current.acceptPendingRefresh("0:a.ts"));
      expect(result.current.pendingRefresh.size).toBe(0);
      expect(result.current.fileContent).toBe("agent2");
    });

    it("handles duplicate acceptPendingRefresh calls in the same batch and Cmd+S with no selection", async () => {
      const { result } = await mount();
      // Cmd+S with nothing selected is a no-op.
      act(() => {
        window.dispatchEvent(new KeyboardEvent("keydown", { key: "s", metaKey: true }));
      });
      expect(saveFileContent).not.toHaveBeenCalled();

      act(() => result.current.promoteFile("0:a.ts"));
      await flush();
      result.current.codeEditorRef.current = new FakeEditor("mine");
      act(() => result.current.markDirty());
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "agent", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.pendingRefresh.has("0:a.ts")).toBe(true);
      expect(result.current.dirtyTabs.has("0:a.ts")).toBe(true);

      // Calling acceptPendingRefresh twice in the same batch exercises the
      // idempotent guards in both setPendingRefresh and setDirtyTabs.
      act(() => {
        result.current.acceptPendingRefresh("0:a.ts");
        result.current.acceptPendingRefresh("0:a.ts");
      });
      expect(result.current.pendingRefresh.size).toBe(0);
      expect(result.current.dirtyTabs.size).toBe(0);
    });

    it("forgets a background tab's unsaved text when the file changes under it", async () => {
      const { result } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.promoteFile("0:b.ts"));
      await flush();
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "agent", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.agentEditedTabs.has("0:a.ts")).toBe(true);
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "agent", binary: false });
      act(() => result.current.switchToTab("0:a.ts"));
      await flush();
      expect(result.current.fileContent).toBe("agent");
      expect(result.current.agentEditedTabs.size).toBe(0);
    });

    it("marks an open tab deleted when its file goes, keeping its text", async () => {
      const { result } = await mountWithOpen("0:a.ts", "disk");
      act(() => result.current.promoteFile("0:b.ts"));
      await flush();
      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(true);
      vi.mocked(fetchFileContent).mockClear();
      act(() => result.current.switchToTab("0:a.ts"));
      expect(result.current.fileContent).toBe("disk");
      expect(fetchFileContent).not.toHaveBeenCalled();

      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(true);

      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "back", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.deletedTabs.size).toBe(0);
    });

    it("drops a pending version for a file that goes, and ignores closed tabs and other errors", async () => {
      const { result } = await mountWithOpen("0:a.ts", "mine");
      act(() => result.current.markDirty());
      vi.mocked(fetchFileContent).mockResolvedValueOnce({ content: "agent", binary: false });
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.pendingRefresh.size).toBe(0);
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(true);

      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => watchSocket().receive(fileChange("closed.ts")));
      await flush();
      expect(result.current.deletedTabs.has("0:closed.ts")).toBe(false);

      vi.mocked(fetchFileContent).mockRejectedValueOnce(new Error("io"));
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(warn).toHaveBeenCalledWith("[loop] refreshing file from disk:", expect.any(Error));
    });

    it("marks a background tab deleted before its text was read", async () => {
      const { result } = await mountWithOpen("0:a.ts", "disk");
      act(() => result.current.promoteFile("0:b.ts"));
      await flush();
      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      vi.mocked(fetchFileContent).mockRejectedValueOnce(new FileNotFoundError("x"));
      act(() => watchSocket().receive(fileChange("a.ts")));
      await flush();
      expect(result.current.deletedTabs.has("0:a.ts")).toBe(true);
    });

    it("reloads a changed media tab when shown, and only marks it otherwise", async () => {
      const { result } = await mount();
      act(() => result.current.promoteFile("0:a.png"));
      act(() => result.current.promoteFile("0:b.png"));
      await flush();
      vi.mocked(buildFileUrl).mockClear();
      act(() => watchSocket().receive(fileChange("a.png")));
      expect(result.current.agentEditedTabs.has("0:a.png")).toBe(true);
      expect(buildFileUrl).not.toHaveBeenCalled();
      act(() => watchSocket().receive(fileChange("b.png")));
      await flush();
      expect(buildFileUrl).toHaveBeenCalledWith(CH, "b.png", 0, 1);
      expect(result.current.imageURL).toBe("url:0:b.png?t=1");
    });

    it("re-reads the open file on focus", async () => {
      const { result } = await mount();
      act(() => {
        window.dispatchEvent(new Event("focus"));
      });
      expect(fetchFileContent).not.toHaveBeenCalled();
      act(() => result.current.switchToTab("0:a.png"));
      await flush();
      act(() => {
        window.dispatchEvent(new Event("focus"));
      });
      expect(buildFileUrl).toHaveBeenLastCalledWith(CH, "a.png", 0, 1);

      const { result: r2, editor } = await mountWithOpen("0:a.ts", "disk");
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "ext", binary: false });
      vi.mocked(fetchDiff).mockClear();
      act(() => {
        window.dispatchEvent(new Event("focus"));
      });
      await flush();
      expect(editor.replaceContent).toHaveBeenCalledWith("ext");
      expect(fetchDiff).toHaveBeenCalled();
      expect(r2.current.agentEditedTabs.has("0:a.ts")).toBe(true);
    });
  });

  describe("agent edits", () => {
    function chatBus() {
      const listeners = new Set<ChatEventListener>();
      const subscribe = (l: ChatEventListener) => {
        listeners.add(l);
        return () => {
          listeners.delete(l);
        };
      };
      const emit = (type: string, data: unknown) => {
        const event: WSEvent = { type, channel_id: CH, data, timestamp: 0 };
        for (const l of listeners) l(event);
      };
      return { listeners, subscribe, emit };
    }

    it("re-reads an open tab after an Edit or Write lands", async () => {
      const bus = chatBus();
      const { result, editor, unmount } = await mountWithOpen("1:src/a.ts", "disk", { subscribeChatEvents: bus.subscribe });
      vi.mocked(fetchFileContent).mockResolvedValue({ content: "agent", binary: false });
      vi.mocked(fetchFileContent).mockClear();

      act(() => {
        bus.emit("tool.use", { tool_use_id: "t1", tool_name: "Edit", input: "/x/src/a.ts" });
      });
      expect(fetchFileContent).not.toHaveBeenCalled();
      act(() => {
        bus.emit("tool.result", { output: "" });
        bus.emit("tool.result", { tool_use_id: "zz", output: "" });
        bus.emit("tool.result", { tool_use_id: "t1", output: "ok" });
        bus.emit("tool.result", { tool_use_id: "t1", output: "ok" });
      });
      expect(fetchFileContent).toHaveBeenCalledTimes(1);
      expect(fetchFileContent).toHaveBeenCalledWith(CH, "src/a.ts", 1);
      await flush();
      expect(editor.replaceContent).toHaveBeenCalledWith("agent");
      expect(result.current.agentEditedTabs.has("1:src/a.ts")).toBe(true);

      unmount();
      expect(bus.listeners.size).toBe(0);
    });

    it("re-reads at once without a call id, and skips failed, other and unknown tools", async () => {
      const bus = chatBus();
      await mountWithOpen("1:src/a.ts", "disk", { subscribeChatEvents: bus.subscribe });
      vi.mocked(fetchFileContent).mockClear();
      act(() => {
        bus.emit("tool.use", { tool_use_id: "t2", tool_name: "Write", input: "/x/src/a.ts" });
        bus.emit("tool.result", { tool_use_id: "t2", output: "", is_error: true });
        bus.emit("tool.use", { tool_name: "Read", input: "/x/src/a.ts" });
        bus.emit("tool.use", { tool_name: "Write", input: "/elsewhere/a.ts" });
        bus.emit("tool.use", { tool_name: "Write", input: "/x/src/closed.ts" });
        bus.emit("message.created", {});
      });
      expect(fetchFileContent).not.toHaveBeenCalled();
      act(() => {
        bus.emit("tool.use", { tool_name: "Write", input: "/x/src/a.ts" });
      });
      expect(fetchFileContent).toHaveBeenCalledWith(CH, "src/a.ts", 1);
    });
  });
});
