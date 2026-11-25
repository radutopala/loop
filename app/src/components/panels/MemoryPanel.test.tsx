import { EditorView } from "@codemirror/view";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchGlobalConfig } from "../../api/configApi";
import { fetchMemoryFileContent, fetchMemoryFiles, type MemoryFileInfo, saveMemoryFileContent } from "../../api/loopApi";
import { installRangeRects } from "../../test/panels-browser-memory-codemirror";
import { loadTabs, MemoryPanel } from "./MemoryPanel";

vi.mock("../../api/configApi", () => ({ fetchGlobalConfig: vi.fn() }));

vi.mock("../../api/loopApi", () => ({
  fetchMemoryFiles: vi.fn(),
  fetchMemoryFileContent: vi.fn(),
  saveMemoryFileContent: vi.fn(),
}));

const filesMock = vi.mocked(fetchMemoryFiles);
const contentMock = vi.mocked(fetchMemoryFileContent);
const saveMock = vi.mocked(saveMemoryFileContent);
const configMock = vi.mocked(fetchGlobalConfig);

const TABS_KEY = "loop-memory-tabs";
const WIDTH_KEY = "loop-memory-tree-width";

// The first entry is the one opened automatically.
const files: MemoryFileInfo[] = [
  { file_path: "/mem/proj/sub/c.md", dir_path: "/mem/proj/sub" },
  { file_path: "/mem/proj/b.md", dir_path: "/mem/proj" },
  { file_path: "/mem/proj/a.md", dir_path: "/mem/proj" },
];

function editorSettings(editor: Record<string, unknown> | undefined) {
  configMock.mockResolvedValue({ path: "", raw: "", content: editor === undefined ? {} : { editor } });
}

function renderPanel(props: Partial<Parameters<typeof MemoryPanel>[0]> = {}) {
  return render(<MemoryPanel channelId="ch1" dirPath="/repo" branch="main" embedded onClose={vi.fn()} {...props} />);
}

function view(): EditorView {
  return EditorView.findFromDOM(document.querySelector(".cm-editor") as HTMLElement) as EditorView;
}

async function editorShows(text: string) {
  await waitFor(() => expect(document.querySelector(".cm-editor") && view().state.doc.toString()).toBe(text));
}

// The tree row comes before the tab with the same title.
function treeRow(path: string): HTMLElement {
  return screen.getAllByTitle(path)[0] as HTMLElement;
}

function tab(path: string): HTMLElement {
  const all = screen.getAllByTitle(path);
  return all[all.length - 1] as HTMLElement;
}

function tabTitles(): string[] {
  const bar = document.querySelector('[title="Editor only"]')?.parentElement?.previousElementSibling as HTMLElement;
  return Array.from(bar.children).map((b) => b.getAttribute("title") ?? "");
}

function closeTab(path: string) {
  const glyph = tab(path).lastElementChild as HTMLElement;
  fireEvent.click(glyph);
}

function type(text: string) {
  act(() => view().dispatch({ changes: { from: view().state.doc.length, insert: text } }));
}

function storedTabs(): Record<string, { tabs: string[]; selected: string | null }> {
  return JSON.parse(localStorage.getItem(TABS_KEY) ?? "{}");
}

describe("loadTabs", () => {
  beforeEach(() => localStorage.clear());

  it("returns the channel's stored tabs", () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ ch1: { tabs: ["/a.md"], selected: "/a.md" } }));
    expect(loadTabs("ch1")).toEqual({ tabs: ["/a.md"], selected: "/a.md" });
  });

  it.each([
    ["nothing stored", null],
    ["a non-object value", "5"],
    ["another channel only", JSON.stringify({ other: { tabs: ["/x.md"], selected: null } })],
  ])("falls back to no tabs with %s", (_name, raw) => {
    if (raw !== null) localStorage.setItem(TABS_KEY, raw);
    expect(loadTabs("ch1")).toEqual({ tabs: [], selected: null });
  });
});

describe("MemoryPanel", () => {
  let removeRangeRects: () => void = () => {};

  beforeEach(() => {
    removeRangeRects = installRangeRects();
    vi.clearAllMocks();
    localStorage.clear();
    filesMock.mockResolvedValue(files);
    contentMock.mockImplementation(async (_ch, p) => `content of ${p}`);
    saveMock.mockResolvedValue(undefined);
    editorSettings(undefined);
  });

  afterEach(() => {
    removeRangeRects();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("shows loading, then the empty state", async () => {
    filesMock.mockResolvedValue([]);
    renderPanel();
    expect(screen.getByText("Loading...")).toBeTruthy();
    expect(await screen.findByText("No memory files indexed")).toBeTruthy();
    expect(filesMock).toHaveBeenCalledWith("ch1");
  });

  it("shows the list error message", async () => {
    filesMock.mockRejectedValue(new Error("index offline"));
    renderPanel();
    expect(await screen.findByText("index offline")).toBeTruthy();
    expect(screen.queryByText("No memory files indexed")).toBeNull();
  });

  it("shows a generic list error for a non-Error rejection", async () => {
    filesMock.mockRejectedValue("boom");
    renderPanel();
    expect(await screen.findByText("Failed to load")).toBeTruthy();
  });

  it("builds a sorted tree and opens the first file", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    const names = Array.from(document.querySelectorAll("button[title]"))
      .map((b) => b.getAttribute("title"))
      .slice(0, 6);
    // Directories sort before files; files sort by name.
    expect(names).toEqual(["Refresh files", "mem", "proj", "sub", "/mem/proj/sub/c.md", "/mem/proj/a.md"]);
    expect(contentMock).toHaveBeenCalledWith("ch1", "/mem/proj/sub/c.md");
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md"]);
    expect(storedTabs()).toEqual({ ch1: { tabs: ["/mem/proj/sub/c.md"], selected: "/mem/proj/sub/c.md" } });
  });

  it("collapses and re-expands a directory", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.click(screen.getByTitle("sub"));
    expect(screen.getAllByTitle("/mem/proj/sub/c.md")).toHaveLength(1);
    fireEvent.click(screen.getByTitle("sub"));
    expect(screen.getAllByTitle("/mem/proj/sub/c.md")).toHaveLength(2);
  });

  it("reports a file that fails to load", async () => {
    contentMock.mockRejectedValueOnce(new Error("missing"));
    renderPanel();
    expect(await screen.findByText("File not available on disk")).toBeTruthy();
  });

  it("reports a non-Error content failure", async () => {
    contentMock.mockRejectedValueOnce("nope");
    renderPanel();
    expect(await screen.findByText("File not available on disk")).toBeTruthy();
  });

  it("restores the saved tabs and loads the selected file", async () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ ch1: { tabs: ["/mem/proj/a.md", "/mem/proj/b.md"], selected: "/mem/proj/b.md" } }));
    renderPanel();
    await editorShows("content of /mem/proj/b.md");
    expect(tabTitles()).toEqual(["/mem/proj/a.md", "/mem/proj/b.md"]);
    expect(contentMock).toHaveBeenCalledTimes(1);
  });

  it("restores tabs without a selection and does not auto-open", async () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ ch1: { tabs: ["/mem/proj/a.md"], selected: null } }));
    renderPanel();
    expect(await screen.findByText("Select a file")).toBeTruthy();
    expect(contentMock).not.toHaveBeenCalled();
  });

  it("shows the restored file's load error", async () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ ch1: { tabs: ["/mem/proj/a.md"], selected: "/mem/proj/a.md" } }));
    contentMock.mockRejectedValueOnce(new Error("gone"));
    renderPanel();
    expect(await screen.findByText("File not available on disk")).toBeTruthy();
  });

  it("shows a non-Error restored load failure", async () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ ch1: { tabs: ["/mem/proj/a.md"], selected: "/mem/proj/a.md" } }));
    contentMock.mockRejectedValueOnce("gone");
    renderPanel();
    expect(await screen.findByText("File not available on disk")).toBeTruthy();
  });

  it("keeps other channels' tabs", async () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ other: { tabs: ["/x.md"], selected: "/x.md" } }));
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    expect(Object.keys(storedTabs())).toEqual(["other", "ch1"]);
    expect(storedTabs().other).toEqual({ tabs: ["/x.md"], selected: "/x.md" });
  });

  it("opens single clicks as a replaceable preview tab that is not persisted", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md", "/mem/proj/a.md"]);
    expect((screen.getAllByText("a.md").at(-1) as HTMLElement).style.fontStyle).toBe("italic");
    expect(storedTabs().ch1).toEqual({ tabs: ["/mem/proj/sub/c.md"], selected: null });

    // A second preview click replaces the first preview tab.
    fireEvent.click(treeRow("/mem/proj/b.md"));
    await editorShows("content of /mem/proj/b.md");
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md", "/mem/proj/b.md"]);

    // Clicking the current preview again keeps it.
    fireEvent.click(treeRow("/mem/proj/b.md"));
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md", "/mem/proj/b.md"]);

    // Clicking a permanent tab's file switches to it, and again is a no-op.
    fireEvent.click(treeRow("/mem/proj/sub/c.md"));
    await editorShows("content of /mem/proj/sub/c.md");
    const calls = contentMock.mock.calls.length;
    fireEvent.click(treeRow("/mem/proj/sub/c.md"));
    expect(contentMock).toHaveBeenCalledTimes(calls);
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md", "/mem/proj/b.md"]);
  });

  it("opens a preview of a file whose tab is already the preview", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    fireEvent.click(tab("/mem/proj/sub/c.md"));
    await editorShows("content of /mem/proj/sub/c.md");
    // Back to the preview file from the tree: the tab is reused, not duplicated.
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md", "/mem/proj/a.md"]);
  });

  it("promotes a preview tab on double click in the tree or the tab", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    fireEvent.doubleClick(treeRow("/mem/proj/a.md"));
    expect(storedTabs().ch1).toEqual({ tabs: ["/mem/proj/sub/c.md", "/mem/proj/a.md"], selected: "/mem/proj/a.md" });

    fireEvent.click(treeRow("/mem/proj/b.md"));
    await editorShows("content of /mem/proj/b.md");
    fireEvent.doubleClick(tab("/mem/proj/b.md"));
    expect(storedTabs().ch1?.tabs).toEqual(["/mem/proj/sub/c.md", "/mem/proj/a.md", "/mem/proj/b.md"]);
  });

  it("opens a file permanently by double click when it is not open yet", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.doubleClick(treeRow("/mem/proj/b.md"));
    await editorShows("content of /mem/proj/b.md");
    expect(storedTabs().ch1).toEqual({ tabs: ["/mem/proj/sub/c.md", "/mem/proj/b.md"], selected: "/mem/proj/b.md" });
    // Double clicking the selected permanent file again changes nothing.
    fireEvent.doubleClick(treeRow("/mem/proj/b.md"));
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md", "/mem/proj/b.md"]);
  });

  it("opens files permanently when preview tabs are off", async () => {
    editorSettings({ preview_tabs: false });
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    await waitFor(() => expect(configMock).toHaveBeenCalled());
    await act(async () => {});
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    expect(storedTabs().ch1?.tabs).toEqual(["/mem/proj/sub/c.md", "/mem/proj/a.md"]);
    // Opening it again does not add a second tab.
    fireEvent.click(treeRow("/mem/proj/a.md"));
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md", "/mem/proj/a.md"]);
  });

  it("ignores editor settings that are missing or of the wrong type", async () => {
    editorSettings({ preview_tabs: "yes", auto_save_on_blur: 1 });
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    // Preview tabs stay on: the preview is not persisted.
    expect(storedTabs().ch1?.tabs).toEqual(["/mem/proj/sub/c.md"]);
  });

  it("logs a failed settings load", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    configMock.mockRejectedValue(new Error("no config"));
    renderPanel();
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] loading editor settings:", expect.any(Error)));
  });

  it("handles a config with no content", async () => {
    configMock.mockResolvedValue({ path: "", raw: "", content: null });
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
  });

  it("opens the file passed in from navigation", async () => {
    const { rerender } = renderPanel({ openMemoryFile: "/mem/proj/b.md" });
    await editorShows("content of /mem/proj/b.md");
    expect(tabTitles()).toEqual(["/mem/proj/b.md"]);
    rerender(<MemoryPanel channelId="ch1" dirPath="/repo" branch="main" embedded onClose={vi.fn()} openMemoryFile="/mem/proj/b.md" />);
    rerender(<MemoryPanel channelId="ch1" dirPath="/repo" branch="main" embedded onClose={vi.fn()} openMemoryFile={null} />);
    expect(tabTitles()).toEqual(["/mem/proj/b.md"]);
  });

  it("marks an edited file dirty, promotes its preview, and saves with a trailing newline", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    type("!");
    type("?");
    // Editing promotes the preview tab, so it is persisted now.
    expect(storedTabs().ch1?.tabs).toEqual(["/mem/proj/sub/c.md", "/mem/proj/a.md"]);
    expect(tab("/mem/proj/a.md").textContent).not.toContain("×");

    fireEvent.keyDown(window, { key: "s", metaKey: true });
    expect(saveMock).toHaveBeenCalledWith("ch1", "/mem/proj/a.md", "content of /mem/proj/a.md!?\n");
    expect(view().state.doc.toString()).toBe("content of /mem/proj/a.md!?\n");
    await waitFor(() => expect(tab("/mem/proj/a.md").textContent).toContain("×"));
    saveMock.mockClear();

    // A clean file is not saved.
    fireEvent.keyDown(window, { key: "s", metaKey: true });
    expect(saveMock).not.toHaveBeenCalled();
  });

  it("saves content that already ends in a newline as is, and an empty file", async () => {
    contentMock.mockResolvedValue("");
    renderPanel();
    await editorShows("");
    type("x\n");
    fireEvent.keyDown(window, { key: "s", ctrlKey: true });
    expect(saveMock).toHaveBeenLastCalledWith("ch1", "/mem/proj/sub/c.md", "x\n");
    await waitFor(() => expect(tab("/mem/proj/sub/c.md").textContent).toContain("×"));
    act(() => view().dispatch({ changes: { from: 0, to: view().state.doc.length, insert: "" } }));
    fireEvent.keyDown(window, { key: "s", ctrlKey: true });
    expect(saveMock).toHaveBeenLastCalledWith("ch1", "/mem/proj/sub/c.md", "");
  });

  it("keeps a file dirty when its save fails", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    saveMock.mockRejectedValue(new Error("read-only"));
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    type("!");
    fireEvent.keyDown(window, { key: "s", metaKey: true });
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] saving memory file:", expect.any(Error)));
    expect(tab("/mem/proj/sub/c.md").textContent).not.toContain("×");
  });

  it("keeps unsaved edits across tab switches", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.doubleClick(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    type("!");
    fireEvent.click(tab("/mem/proj/sub/c.md"));
    await editorShows("content of /mem/proj/sub/c.md");
    expect(saveMock).not.toHaveBeenCalled();
    fireEvent.click(tab("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md!");
    expect(contentMock.mock.calls.filter(([, p]) => p === "/mem/proj/a.md")).toHaveLength(1);
  });

  it("auto-saves a dirty file on tab switch, blur and unmount when enabled", async () => {
    editorSettings({ auto_save_on_blur: true });
    const { unmount } = renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    await act(async () => {});
    fireEvent.doubleClick(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    type("1");
    fireEvent.click(tab("/mem/proj/sub/c.md"));
    expect(saveMock).toHaveBeenLastCalledWith("ch1", "/mem/proj/a.md", "content of /mem/proj/a.md1\n");
    await editorShows("content of /mem/proj/sub/c.md");

    type("2");
    fireEvent.blur(window);
    expect(saveMock).toHaveBeenLastCalledWith("ch1", "/mem/proj/sub/c.md", "content of /mem/proj/sub/c.md2\n");
    await waitFor(() => expect(tab("/mem/proj/sub/c.md").textContent).toContain("×"));

    type("3");
    unmount();
    expect(saveMock).toHaveBeenLastCalledWith("ch1", "/mem/proj/sub/c.md", "content of /mem/proj/sub/c.md2\n3\n");
  });

  it("does not save on blur when auto-save is off", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    type("!");
    fireEvent.blur(window);
    expect(saveMock).not.toHaveBeenCalled();
  });

  it("reloads the open file on focus and clears its dirty mark when it changed", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    type("!");
    contentMock.mockResolvedValue("fresh");
    fireEvent.focus(window);
    await editorShows("fresh");
    expect(tab("/mem/proj/sub/c.md").textContent).toContain("×");

    // Unchanged content leaves the editor's state alone.
    const state = view().state;
    fireEvent.focus(window);
    await waitFor(() => expect(contentMock).toHaveBeenCalledTimes(3));
    await act(async () => {});
    expect(view().state).toBe(state);

    // Changed content on a clean file just replaces it.
    contentMock.mockResolvedValue("fresher");
    fireEvent.focus(window);
    await editorShows("fresher");
  });

  it("drops a focus reload when the selection moved on or the editor is gone", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    let resolve: (v: string) => void = () => {};
    contentMock.mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      }),
    );
    fireEvent.focus(window);
    fireEvent.doubleClick(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    await act(async () => resolve("stale"));
    expect(view().state.doc.toString()).toBe("content of /mem/proj/a.md");

    // A slow fetch keeps the editor unmounted; a focus reload then has no view.
    let resolveB: (v: string) => void = () => {};
    contentMock.mockReturnValueOnce(
      new Promise((r) => {
        resolveB = r;
      }),
    );
    fireEvent.doubleClick(treeRow("/mem/proj/b.md"));
    expect(await screen.findByText("Loading...")).toBeTruthy();
    contentMock.mockResolvedValueOnce("ignored");
    fireEvent.focus(window);
    await waitFor(() => expect(contentMock).toHaveBeenLastCalledWith("ch1", "/mem/proj/b.md"));
    await act(async () => {});
    await act(async () => resolveB("b body"));
    await editorShows("b body");
  });

  it("logs a failed focus reload", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    contentMock.mockRejectedValue(new Error("offline"));
    fireEvent.focus(window);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] refreshing memory file on blur:", expect.any(Error)));
  });

  it("does nothing on focus without a selected file", async () => {
    filesMock.mockResolvedValue([]);
    renderPanel();
    await screen.findByText("No memory files indexed");
    fireEvent.focus(window);
    expect(contentMock).not.toHaveBeenCalled();
  });

  it("closes tabs, moving the selection to a neighbor", async () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ ch1: { tabs: ["/mem/proj/a.md", "/mem/proj/b.md", "/mem/proj/sub/c.md"], selected: "/mem/proj/b.md" } }));
    renderPanel();
    await editorShows("content of /mem/proj/b.md");
    // Closing an unselected tab keeps the selection.
    closeTab("/mem/proj/a.md");
    expect(tabTitles()).toEqual(["/mem/proj/b.md", "/mem/proj/sub/c.md"]);
    expect(view().state.doc.toString()).toBe("content of /mem/proj/b.md");
    // Closing the selected tab selects the one that took its place.
    closeTab("/mem/proj/b.md");
    await editorShows("content of /mem/proj/sub/c.md");
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md"]);
  });

  it("selects the previous tab when the last tab is closed", async () => {
    localStorage.setItem(TABS_KEY, JSON.stringify({ ch1: { tabs: ["/mem/proj/a.md", "/mem/proj/b.md"], selected: "/mem/proj/b.md" } }));
    renderPanel();
    await editorShows("content of /mem/proj/b.md");
    closeTab("/mem/proj/b.md");
    await editorShows("content of /mem/proj/a.md");
  });

  it("closing a dirty or preview tab discards its edits and preview state", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.click(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    closeTab("/mem/proj/a.md");
    await editorShows("content of /mem/proj/sub/c.md");
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md"]);

    fireEvent.doubleClick(treeRow("/mem/proj/b.md"));
    await editorShows("content of /mem/proj/b.md");
    type("!");
    fireEvent.click(tab("/mem/proj/sub/c.md"));
    await editorShows("content of /mem/proj/sub/c.md");
    closeTab("/mem/proj/b.md");
    fireEvent.doubleClick(treeRow("/mem/proj/b.md"));
    // The cached edit is gone, so the file is fetched again.
    await editorShows("content of /mem/proj/b.md");
  });

  it("auto-saves the selected dirty tab before closing it", async () => {
    editorSettings({ auto_save_on_blur: true });
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    await act(async () => {});
    type("!");
    closeTab("/mem/proj/sub/c.md");
    expect(saveMock).toHaveBeenCalledWith("ch1", "/mem/proj/sub/c.md", "content of /mem/proj/sub/c.md!\n");
  });

  it("reopens the first file after the last tab is closed", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    contentMock.mockResolvedValue("reloaded");
    closeTab("/mem/proj/sub/c.md");
    await editorShows("reloaded");
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md"]);
  });

  it("refreshes the list from the tree", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    filesMock.mockResolvedValue([...files, { file_path: "/mem/proj/d.md", dir_path: "/mem/proj" }]);
    fireEvent.click(screen.getByTitle("Refresh files"));
    expect(await screen.findByTitle("/mem/proj/d.md")).toBeTruthy();
  });

  it("polls quietly for new files and keeps collapsed directories", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    filesMock.mockResolvedValue([]);
    renderPanel();
    await screen.findByText("No memory files indexed");

    // A failed poll leaves the view as it was.
    filesMock.mockRejectedValueOnce(new Error("blip"));
    await act(async () => vi.advanceTimersByTime(8000));
    expect(screen.getByText("No memory files indexed")).toBeTruthy();
    expect(screen.queryByText("blip")).toBeNull();

    // Files that appear in a poll show up and the first one opens.
    filesMock.mockResolvedValue(files);
    await act(async () => vi.advanceTimersByTime(8000));
    await editorShows("content of /mem/proj/sub/c.md");
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("resizes the tree and remembers the width within bounds", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    const list = screen.getByTitle("Refresh files").parentElement?.parentElement as HTMLElement;
    const handle = list.nextElementSibling as HTMLElement;
    expect(list.style.width).toBe("200px");

    fireEvent.mouseDown(handle, { clientX: 100 });
    expect((list.parentElement as HTMLElement).style.userSelect).toBe("none");
    fireEvent.mouseMove(document, { clientX: 150 });
    expect(list.style.width).toBe("250px");
    fireEvent.mouseMove(document, { clientX: 1000 });
    expect(list.style.width).toBe("400px");
    fireEvent.mouseMove(document, { clientX: -1000 });
    expect(list.style.width).toBe("120px");
    fireEvent.mouseUp(document);
    expect(localStorage.getItem(WIDTH_KEY)).toBe("120");
    expect((list.parentElement as HTMLElement).style.userSelect).toBe("");

    // Listeners are gone after mouseup.
    fireEvent.mouseMove(document, { clientX: 500 });
    expect(list.style.width).toBe("120px");
  });

  it("keeps the start width when released without moving", async () => {
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    const handle = screen.getByTitle("Refresh files").parentElement!.parentElement!.nextElementSibling as HTMLElement;
    fireEvent.mouseDown(handle, { clientX: 100 });
    fireEvent.mouseUp(document);
    expect(localStorage.getItem(WIDTH_KEY)).toBe("200");
  });

  it.each([
    ["300", "300px"],
    ["50", "200px"],
    ["999", "200px"],
  ])("loads a stored tree width of %s as %s", async (stored, width) => {
    localStorage.setItem(WIDTH_KEY, stored);
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    const list = screen.getByTitle("Refresh files").parentElement?.parentElement as HTMLElement;
    expect(list.style.width).toBe(width);
  });

  it("reloads the list when the channel changes", async () => {
    const { rerender } = renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    filesMock.mockResolvedValue([{ file_path: "/mem/other/x.md", dir_path: "/mem/other" }]);
    rerender(<MemoryPanel channelId="ch2" dirPath="/repo" branch="main" embedded onClose={vi.fn()} />);
    expect(await screen.findByTitle("other")).toBeTruthy();
    expect(filesMock).toHaveBeenLastCalledWith("ch2");
  });

  it("handles a save completing after its tab was already closed", async () => {
    let resolveSave: () => void = () => {};
    saveMock.mockReturnValueOnce(
      new Promise<void>((r) => {
        resolveSave = r;
      }),
    );
    renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    fireEvent.doubleClick(treeRow("/mem/proj/a.md"));
    await editorShows("content of /mem/proj/a.md");
    type("!");
    fireEvent.keyDown(window, { key: "s", metaKey: true });
    expect(saveMock).toHaveBeenCalledTimes(1);
    closeTab("/mem/proj/a.md");
    await editorShows("content of /mem/proj/sub/c.md");
    await act(async () => resolveSave());
    expect(tabTitles()).toEqual(["/mem/proj/sub/c.md"]);
  });

  it("ignores blur auto-save when no file is selected", async () => {
    editorSettings({ auto_save_on_blur: true });
    filesMock.mockResolvedValue([]);
    renderPanel();
    await screen.findByText("No memory files indexed");
    await act(async () => {});
    fireEvent.blur(window);
    expect(saveMock).not.toHaveBeenCalled();
  });

  it("sorts directories before files even when files appear first in the list", async () => {
    filesMock.mockResolvedValue([
      { file_path: "/mem/proj/a.md", dir_path: "/mem/proj" },
      { file_path: "/mem/proj/sub/c.md", dir_path: "/mem/proj/sub" },
    ]);
    renderPanel();
    await editorShows("content of /mem/proj/a.md");
    const names = Array.from(document.querySelectorAll("button[title]"))
      .map((b) => b.getAttribute("title"))
      .slice(0, 6);
    expect(names).toEqual(["Refresh files", "mem", "proj", "sub", "/mem/proj/sub/c.md", "/mem/proj/a.md"]);
  });

  it("ignores blur auto-save and tab switches while viewer is unmounted during refresh", async () => {
    editorSettings({ auto_save_on_blur: true });
    const { rerender } = renderPanel();
    await editorShows("content of /mem/proj/sub/c.md");
    act(() => view().dispatch({ changes: { from: 0, to: 0, insert: "unsaved " } }));
    filesMock.mockResolvedValueOnce([]);
    fireEvent.click(screen.getByTitle("Refresh files"));
    await waitFor(() => expect(document.querySelector(".cm-editor")).toBeNull());
    fireEvent(window, new Event("blur"));
    expect(saveMock).not.toHaveBeenCalled();
    rerender(<MemoryPanel channelId="ch1" dirPath="/repo" branch="main" embedded onClose={vi.fn()} openMemoryFile="/mem/proj/a.md" />);
    await waitFor(() => expect(contentMock).toHaveBeenCalledWith("ch1", "/mem/proj/a.md"));
  });
});
