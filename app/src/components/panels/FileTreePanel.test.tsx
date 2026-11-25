import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_EDITOR_CONFIG, type FileEntry, type FileSearchResult, type GrepResult, grepFiles, type RootEntry, searchFiles } from "../../api/loopApi";
import type { EditorStateApi } from "../../hooks/useEditorState";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors } from "../../theme";
import { FileTreePanel } from "./FileTreePanel";

vi.mock("../../api/loopApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/loopApi")>()),
  searchFiles: vi.fn(),
  grepFiles: vi.fn(),
  fetchReadme: vi.fn(() => new Promise(() => {})),
}));

const css = (color: string) => {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
};

const roots: RootEntry[] = [
  { index: 0, path: "/w/app", display_path: "~/w/app", name: "app" },
  { index: 1, path: "/w/lib", name: "lib" },
];

const tree = new Map<string, FileEntry[]>([
  [
    "0:",
    [
      { name: "src", type: "dir" },
      { name: "main.go", type: "file" },
    ],
  ],
  ["0:src", [{ name: "a.ts", type: "file" }]],
]);

type Api = EditorStateApi & { [K in keyof EditorStateApi]: EditorStateApi[K] };

const makeState = (overrides: Partial<EditorStateApi> = {}): Api =>
  ({
    roots,
    expandedDirs: new Set(["0:"]),
    dirContents: tree,
    selectedDir: "0:",
    selectedPath: null,
    previewTab: null,
    loadDir: vi.fn(() => Promise.resolve()),
    refreshTree: vi.fn(() => Promise.resolve()),
    showTree: vi.fn(),
    toggleDir: vi.fn(),
    revealDir: vi.fn(),
    revealRequest: null,
    setSelectedDir: vi.fn(),
    addExtraDir: vi.fn(() => Promise.resolve()),
    handleCreateFile: vi.fn(),
    handleDeleteFilePath: vi.fn(),
    handleCreateDirPath: vi.fn(),
    handleDeleteDirPath: vi.fn(),
    handleRenamePath: vi.fn(() => Promise.resolve()),
    handleCopyPath: vi.fn(() => Promise.resolve("0:src/main copy.go")),
    openFile: vi.fn(),
    openFileAtLine: vi.fn(),
    promoteFile: vi.fn(),
    editorConfig: { ...DEFAULT_EDITOR_CONFIG, follow_active_file: false },
    ...overrides,
  }) as unknown as Api;

const ui = (state: Api, channelId = "ch", dirPath = "/w/app") => (
  <ThemeProvider initialTheme="dark">
    <FileTreePanel channelId={channelId} dirPath={dirPath} branch="main" editorState={state} onClose={() => {}} />
  </ThemeProvider>
);

const mount = (overrides: Partial<EditorStateApi> = {}, channelId = "ch", dirPath = "/w/app") => {
  const state = makeState(overrides);
  const r = render(ui(state, channelId, dirPath));
  return { state, rerender: (next: Api, ch = channelId) => r.rerender(ui(next, ch, dirPath)) };
};

const row = (path: string) => document.querySelector<HTMLElement>(`[data-testid="file-tree-entry"][data-path="${path}"]`)!;
const rootRow = (index: number) => document.querySelector<HTMLElement>(`[data-testid="file-tree-root"][data-root="${index}"]`)!;
const search = () => screen.getByTestId("file-tree-search") as HTMLInputElement;
const menuItem = (label: string) => within(screen.getByTestId("context-menu")).getByRole("menuitem", { name: new RegExp(`^${label}($|[^ ])`) });
const menuLabels = () =>
  within(screen.getByTestId("context-menu"))
    .getAllByRole("menuitem")
    .map((b) => b.dataset.label);

const hit = (over: Partial<FileSearchResult> = {}): FileSearchResult => ({ root_index: 0, rel_path: "src/a.ts", name: "a.ts", ...over });

describe("FileTreePanel", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.mocked(searchFiles).mockReset();
    vi.mocked(grepFiles).mockReset();
  });
  afterEach(() => {
    vi.restoreAllMocks();
    delete (window as { loopAPI?: unknown }).loopAPI;
  });

  describe("the tree", () => {
    it("shows the tree on mount and lists the roots with the first one's entries", () => {
      const { state } = mount();
      expect(state.showTree).toHaveBeenCalled();
      expect(screen.getAllByTestId("file-tree-root").map((r) => r.textContent)).toEqual(["app", "lib"]);
      expect(rootRow(0).getAttribute("aria-expanded")).toBe("true");
      expect(rootRow(1).getAttribute("aria-expanded")).toBe("false");
      expect(screen.getAllByTestId("file-tree-entry").map((r) => r.dataset.path)).toEqual(["0:src", "0:main.go"]);
    });

    it("shows the channel's directory as the root before the roots load", () => {
      mount({ roots: [] });
      expect(rootRow(0).textContent).toBe("app");
    });

    it("names a fallback root after the whole path when it has no last segment", () => {
      mount({ roots: [] }, "ch", "/");
      expect(rootRow(0).textContent).toBe("/");
    });

    it("shows an expanded root that isn't loaded yet as empty", () => {
      mount({ expandedDirs: new Set(["1:"]) });
      expect(screen.queryAllByTestId("file-tree-entry")).toHaveLength(0);
    });

    it("collapses an expanded root on click", () => {
      const { state } = mount();
      fireEvent.click(rootRow(0));
      expect(state.setSelectedDir).toHaveBeenCalledWith("0:");
      expect(state.toggleDir).toHaveBeenCalledWith("0:");
      expect(state.loadDir).not.toHaveBeenCalled();
    });

    it("expands a collapsed root on click and loads it once", () => {
      const { state, rerender } = mount();
      fireEvent.click(rootRow(1));
      expect(state.toggleDir).toHaveBeenCalledWith("1:");
      expect(state.loadDir).toHaveBeenCalledExactlyOnceWith(".", 1);
      const loaded = makeState({ expandedDirs: new Set(), dirContents: new Map([["1:", []]]) });
      rerender(loaded);
      fireEvent.click(rootRow(1));
      expect(loaded.toggleDir).toHaveBeenCalledWith("1:");
      expect(loaded.loadDir).not.toHaveBeenCalled();
    });

    it("highlights an unselected root on hover only", () => {
      mount({ selectedDir: "0:src" });
      fireEvent.mouseEnter(rootRow(0));
      const hover = document.createElement("div");
      hover.style.backgroundColor = darkColors.hoverBg;
      expect(rootRow(0).style.backgroundColor).toBe(hover.style.backgroundColor);
      fireEvent.mouseLeave(rootRow(0));
      expect(rootRow(0).style.backgroundColor).toBe("transparent");
    });

    it("keeps the selected root's highlight on hover", () => {
      mount();
      const before = rootRow(0).style.backgroundColor;
      fireEvent.mouseEnter(rootRow(0));
      fireEvent.mouseLeave(rootRow(0));
      expect(rootRow(0).style.backgroundColor).toBe(before);
    });

    it("selects and toggles a directory, opens a file on click and keeps it on double-click", () => {
      const { state } = mount();
      fireEvent.click(row("0:src"));
      expect(state.setSelectedDir).toHaveBeenCalledWith("0:src");
      expect(state.toggleDir).toHaveBeenCalledWith("0:src");
      fireEvent.click(row("0:main.go"));
      expect(state.openFile).toHaveBeenCalledWith("0:main.go");
      fireEvent.doubleClick(row("0:main.go"));
      expect(state.promoteFile).toHaveBeenCalledWith("0:main.go");
    });

    it("refreshes the tree", () => {
      const { state } = mount();
      const refresh = screen.getByRole("button", { name: "Refresh files" });
      fireEvent.mouseEnter(refresh);
      expect(refresh.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(refresh);
      expect(refresh.style.color).toBe(css(darkColors.textDim));
      fireEvent.click(refresh);
      expect(state.refreshTree).toHaveBeenCalled();
    });
  });

  describe("follow active file", () => {
    it("reveals the active file's directory and scrolls to it when on", () => {
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const { state } = mount({ editorConfig: DEFAULT_EDITOR_CONFIG, selectedPath: "0:main.go" });
      expect(state.revealDir).toHaveBeenCalledWith("0:");
      expect(scroll).toHaveBeenCalledWith({ block: "nearest" });
      expect(scroll.mock.contexts[0]).toBe(row("0:main.go"));
      expect(screen.getByTestId("file-tree-auto-reveal").getAttribute("aria-pressed")).toBe("true");
    });

    it("leaves the tree alone when off", () => {
      const { state } = mount({ selectedPath: "0:main.go" });
      expect(state.revealDir).not.toHaveBeenCalled();
    });

    it("waits for the revealed file to show before scrolling to it", () => {
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const { rerender } = mount({ editorConfig: DEFAULT_EDITOR_CONFIG, selectedPath: "0:src/a.ts" });
      expect(scroll).not.toHaveBeenCalled();
      rerender(makeState({ editorConfig: DEFAULT_EDITOR_CONFIG, selectedPath: "0:src/a.ts", expandedDirs: new Set(["0:", "0:src"]) }));
      expect(scroll.mock.contexts[0]).toBe(row("0:src/a.ts"));
    });

    it("stores the channel's choice against the config, and forgets it when it matches again", () => {
      mount();
      const toggle = screen.getByTestId("file-tree-auto-reveal");
      expect(toggle.dataset.followSource).toBe("default");
      fireEvent.mouseEnter(toggle);
      expect(toggle.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(toggle);
      expect(toggle.style.color).toBe(css(darkColors.textDim));
      fireEvent.click(toggle);
      expect(localStorage.getItem("loop.editor.followActive.ch")).toBe("1");
      expect(toggle.getAttribute("aria-pressed")).toBe("true");
      expect(toggle.dataset.followSource).toBe("channel");
      // On, the hover leaves its active colour alone.
      fireEvent.mouseEnter(toggle);
      fireEvent.mouseLeave(toggle);
      expect(toggle.style.color).toBe(css(darkColors.active));
      fireEvent.click(toggle);
      expect(localStorage.getItem("loop.editor.followActive.ch")).toBeNull();
      expect(toggle.getAttribute("aria-pressed")).toBe("false");
    });

    it("reads another channel's stored choice after the channel changes", () => {
      localStorage.setItem("loop.editor.followActive.other", "1");
      const { rerender } = mount();
      expect(screen.getByTestId("file-tree-auto-reveal").getAttribute("aria-pressed")).toBe("false");
      rerender(makeState(), "other");
      expect(screen.getByTestId("file-tree-auto-reveal").getAttribute("aria-pressed")).toBe("true");
    });
  });

  describe("reveal from a tab", () => {
    it("closes the search and scrolls to the file", async () => {
      vi.mocked(searchFiles).mockResolvedValue([hit()]);
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const { rerender } = mount();
      fireEvent.change(search(), { target: { value: "a" } });
      await screen.findByTestId("file-search-result");
      rerender(makeState({ revealRequest: { path: "0:main.go", seq: 1 } }));
      expect(search().value).toBe("");
      await waitFor(() => expect(scroll.mock.contexts).toContain(row("0:main.go")));
    });
  });

  describe("new files and directories", () => {
    it("starts a new file in the selected directory and creates it on Enter", () => {
      const { state } = mount({ selectedDir: "0:src" });
      const add = screen.getByRole("button", { name: "New file" });
      fireEvent.mouseEnter(add);
      expect(add.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(add);
      expect(add.style.color).toBe(css(darkColors.textDim));
      fireEvent.click(add);
      const input = screen.getByPlaceholderText("path/to/file.ext") as HTMLInputElement;
      expect(input.value).toBe("src/");
      fireEvent.change(input, { target: { value: "src/b.ts" } });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(state.handleCreateFile).toHaveBeenCalledWith("src/b.ts");
      expect(screen.queryByPlaceholderText("path/to/file.ext")).toBeNull();
    });

    it("starts a new file at a root with an empty name, and drops it on Escape or blur", () => {
      const { state } = mount();
      fireEvent.click(screen.getByRole("button", { name: "New file" }));
      const input = screen.getByPlaceholderText("path/to/file.ext") as HTMLInputElement;
      expect(input.value).toBe("");
      fireEvent.keyDown(input, { key: "a" });
      fireEvent.keyDown(input, { key: "Escape" });
      expect(screen.queryByPlaceholderText("path/to/file.ext")).toBeNull();
      fireEvent.click(screen.getByRole("button", { name: "New file" }));
      fireEvent.blur(screen.getByPlaceholderText("path/to/file.ext"));
      expect(screen.queryByPlaceholderText("path/to/file.ext")).toBeNull();
      expect(state.handleCreateFile).not.toHaveBeenCalled();
    });

    it("creates a directory from a directory's menu, and drops one on Escape or blur", () => {
      const { state } = mount();
      fireEvent.contextMenu(row("0:src"));
      fireEvent.click(menuItem("New directory here"));
      const input = screen.getByPlaceholderText("path/to/directory") as HTMLInputElement;
      expect(input.value).toBe("src/");
      fireEvent.change(input, { target: { value: "src/x" } });
      fireEvent.keyDown(input, { key: "a" });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(state.handleCreateDirPath).toHaveBeenCalledWith("src/x");
      fireEvent.contextMenu(rootRow(0));
      fireEvent.click(menuItem("New directory here"));
      expect((screen.getByPlaceholderText("path/to/directory") as HTMLInputElement).value).toBe("");
      fireEvent.keyDown(screen.getByPlaceholderText("path/to/directory"), { key: "Escape" });
      expect(screen.queryByPlaceholderText("path/to/directory")).toBeNull();
      fireEvent.contextMenu(row("0:src"));
      fireEvent.click(menuItem("New directory here"));
      fireEvent.blur(screen.getByPlaceholderText("path/to/directory"));
      expect(screen.queryByPlaceholderText("path/to/directory")).toBeNull();
    });

    it("starts a new file from a directory's menu", () => {
      mount();
      fireEvent.contextMenu(row("0:src"));
      fireEvent.click(menuItem("New file here"));
      expect((screen.getByPlaceholderText("path/to/file.ext") as HTMLInputElement).value).toBe("src/");
    });
  });

  describe("add directory", () => {
    it("is offered only in the desktop app", () => {
      mount();
      expect(screen.queryByRole("button", { name: "Add directory" })).toBeNull();
    });

    it("adds the picked directory and does nothing when the dialog is cancelled", async () => {
      const showOpenDirectoryDialog = vi.fn().mockResolvedValueOnce(null).mockResolvedValueOnce("/w/extra");
      window.loopAPI = { showOpenDirectoryDialog } as unknown as typeof window.loopAPI;
      const { state } = mount();
      const add = screen.getByRole("button", { name: "Add directory" });
      fireEvent.mouseEnter(add);
      expect(add.style.color).toBe(css(darkColors.textLight));
      fireEvent.mouseLeave(add);
      expect(add.style.color).toBe(css(darkColors.textDim));
      await act(async () => fireEvent.click(add));
      expect(state.addExtraDir).not.toHaveBeenCalled();
      await act(async () => fireEvent.click(add));
      expect(state.addExtraDir).toHaveBeenCalledWith("/w/extra");
      expect(screen.queryByTestId("file-tree-error")).toBeNull();
    });

    it.each([
      ["its message", new Error("not a directory"), "not a directory"],
      ["a fallback", "nope", "Failed to add directory"],
    ])("shows %s when adding fails", async (_name, err, want) => {
      window.loopAPI = { showOpenDirectoryDialog: vi.fn().mockResolvedValue("/w/x") } as unknown as typeof window.loopAPI;
      mount({ addExtraDir: vi.fn(() => Promise.reject(err)) });
      await act(async () => fireEvent.click(screen.getByRole("button", { name: "Add directory" })));
      expect(screen.getByTestId("file-tree-error").textContent).toBe(want);
    });
  });

  describe("name search", () => {
    const typeQuery = async (q: string, results: FileSearchResult[]) => {
      vi.mocked(searchFiles).mockResolvedValue(results);
      fireEvent.change(search(), { target: { value: q } });
      if (results.length) await screen.findAllByTestId("file-search-result");
      else await screen.findByText("Nothing matches");
    };
    const resultRows = () => screen.getAllByTestId("file-search-result");

    it("lists matches in place of the tree, with where they live", async () => {
      mount({ selectedPath: "0:src/a.ts" });
      await typeQuery("a", [hit(), hit({ root_index: 1, rel_path: "pkg", name: "pkg", is_dir: true })]);
      expect(searchFiles).toHaveBeenCalledWith("ch", "a", 50, true);
      expect(screen.queryAllByTestId("file-tree-root")).toHaveLength(0);
      expect(resultRows().map((r) => r.textContent)).toEqual(["a.tsapp/src", "pkg/lib"]);
      expect(resultRows()[0]!.style.background).toBe(css(darkColors.selectedBg).replace("color", "background"));
    });

    it("says when nothing matches", async () => {
      mount();
      await typeQuery("zzz", []);
      expect(screen.getByText("Nothing matches")).toBeTruthy();
    });

    it("moves the selection with the arrows and hover, and opens it on Enter", async () => {
      const { state } = mount();
      await typeQuery("a", [hit(), hit({ rel_path: "b.ts", name: "b.ts" })]);
      expect(resultRows()[0]!.getAttribute("aria-selected")).toBe("true");
      fireEvent.keyDown(search(), { key: "ArrowDown" });
      expect(resultRows()[1]!.getAttribute("aria-selected")).toBe("true");
      fireEvent.keyDown(search(), { key: "Enter" });
      expect(state.openFile).toHaveBeenCalledWith("0:b.ts");
      fireEvent.mouseEnter(resultRows()[0]!);
      fireEvent.keyDown(search(), { key: "Enter", metaKey: true });
      expect(state.promoteFile).toHaveBeenCalledWith("0:src/a.ts");
      fireEvent.keyDown(search(), { key: "Enter", ctrlKey: true });
      expect(state.promoteFile).toHaveBeenCalledTimes(2);
    });

    it("opens a match on click and keeps it on double-click", async () => {
      const { state } = mount();
      await typeQuery("a", [hit()]);
      fireEvent.click(resultRows()[0]!);
      expect(state.openFile).toHaveBeenCalledWith("0:src/a.ts");
      fireEvent.doubleClick(resultRows()[0]!);
      expect(state.promoteFile).toHaveBeenCalledWith("0:src/a.ts");
    });

    it("opens a directory in the tree, closing the search onto it", async () => {
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const { state } = mount();
      await typeQuery("src", [hit({ rel_path: "src", name: "src", is_dir: true })]);
      fireEvent.click(resultRows()[0]!);
      expect(state.revealDir).toHaveBeenCalledWith("0:src");
      expect(search().value).toBe("");
      await waitFor(() => expect(scroll.mock.contexts).toContain(row("0:src")));
    });

    it("does nothing on Enter or arrows without results", async () => {
      const { state } = mount();
      fireEvent.keyDown(search(), { key: "ArrowDown" });
      fireEvent.keyDown(search(), { key: "Enter" });
      await typeQuery("zzz", []);
      fireEvent.keyDown(search(), { key: "Enter" });
      expect(state.openFile).not.toHaveBeenCalled();
      expect(state.promoteFile).not.toHaveBeenCalled();
    });

    it("clears the query on Escape, and lets Escape through when it's empty", async () => {
      const outer = vi.fn();
      document.addEventListener("keydown", outer);
      try {
        mount();
        await typeQuery("a", [hit()]);
        fireEvent.keyDown(search(), { key: "Escape" });
        expect(search().value).toBe("");
        expect(outer).not.toHaveBeenCalled();
        fireEvent.keyDown(search(), { key: "Escape" });
        expect(outer).toHaveBeenCalledTimes(1);
      } finally {
        document.removeEventListener("keydown", outer);
      }
    });

    it("opens a match's menu", async () => {
      mount();
      await typeQuery("a", [hit({ rel_path: ".", name: ".", is_dir: true }), hit()]);
      fireEvent.contextMenu(resultRows()[0]!);
      // "." is the root itself: no Copy, Rename or Delete.
      expect(menuLabels()).toEqual(["Copy relative path", "Copy absolute path", "New file here", "New directory here"]);
      fireEvent.click(menuItem("New file here"));
      expect((screen.getByPlaceholderText("path/to/file.ext") as HTMLInputElement).value).toBe("");
      fireEvent.contextMenu(resultRows()[1]!);
      expect(menuLabels()).toContain("Rename");
    });
  });

  describe("content search", () => {
    const result = (over: Partial<GrepResult> = {}): GrepResult => ({
      files: [
        {
          root_index: 0,
          rel_path: "src/a.ts",
          matches: [
            { line: 3, column: 5, length: 3, text: "    foo bar" },
            { line: 9, column: 1, length: 3, text: "foo" },
          ],
        },
        { root_index: 1, rel_path: "b.go", matches: [{ line: 1, column: 1, length: 3, text: "foo()" }] },
      ],
      truncated: false,
      engine: "ripgrep",
      ...over,
    });
    const contentOn = () => fireEvent.click(screen.getByTestId("file-tree-search-contents"));
    const matches = () => screen.getAllByTestId("content-search-match");

    it("switches the search to file contents with its own options", () => {
      mount();
      expect(search().placeholder).toBe("Search files and directories");
      expect(screen.queryByTestId("file-tree-search-regex")).toBeNull();
      contentOn();
      expect(search().placeholder).toBe("Search in file contents");
      expect(screen.getByTestId("file-tree-search-contents").getAttribute("aria-pressed")).toBe("true");
      const regex = screen.getByTestId("file-tree-search-regex");
      const matchCase = screen.getByTestId("file-tree-search-case");
      fireEvent.click(regex);
      fireEvent.click(matchCase);
      expect(regex.getAttribute("aria-pressed")).toBe("true");
      expect(matchCase.getAttribute("aria-pressed")).toBe("true");
      fireEvent.click(regex);
      fireEvent.click(matchCase);
      expect(regex.getAttribute("aria-pressed")).toBe("false");
      expect(matchCase.getAttribute("aria-pressed")).toBe("false");
    });

    it("asks for a longer query, then shows the search running", async () => {
      vi.mocked(grepFiles).mockReturnValue(new Promise(() => {}));
      mount();
      contentOn();
      fireEvent.change(search(), { target: { value: "f" } });
      expect(screen.getByText("Type at least 2 characters")).toBeTruthy();
      fireEvent.change(search(), { target: { value: "fo" } });
      expect(screen.getByTestId("content-search-status").textContent).toBe("Searching…");
    });

    it("searches with the options and lists the matches by file", async () => {
      vi.mocked(grepFiles).mockResolvedValue(result({ truncated: true }));
      mount({ selectedPath: "0:src/a.ts" });
      contentOn();
      fireEvent.click(screen.getByTestId("file-tree-search-regex"));
      fireEvent.change(search(), { target: { value: "foo" } });
      await screen.findAllByTestId("content-search-match");
      expect(grepFiles).toHaveBeenLastCalledWith("ch", "foo", { regex: true, caseSensitive: false });
      const files = screen.getAllByTestId("content-search-file");
      expect(files.map((f) => f.dataset.path)).toEqual(["0:src/a.ts", "1:b.go"]);
      expect(files[0]!.firstElementChild!.textContent).toBe("a.tsapp/src2");
      expect((files[0]!.firstElementChild as HTMLElement).style.color).toBe(css(darkColors.active));
      expect((files[1]!.firstElementChild as HTMLElement).style.color).toBe(css(darkColors.textLight));
      expect(matches().map((m) => [m.dataset.line, m.querySelector("mark")?.textContent, m.textContent])).toEqual([
        ["3", "foo", "3foo bar"],
        ["9", "foo", "9foo"],
        ["1", "foo", "1foo()"],
      ]);
      expect(screen.getByTestId("content-search-summary").textContent).toBe("3 matches in 2 files; more not shown, refine the search");
    });

    it("opens a match at its line from the keyboard, a click, or keeps its file on double-click", async () => {
      vi.mocked(grepFiles).mockResolvedValue(result());
      const { state } = mount();
      contentOn();
      fireEvent.change(search(), { target: { value: "foo" } });
      await screen.findAllByTestId("content-search-match");
      expect(matches()[0]!.getAttribute("aria-selected")).toBe("true");
      fireEvent.keyDown(search(), { key: "ArrowUp" });
      expect(matches()[2]!.getAttribute("aria-selected")).toBe("true");
      fireEvent.keyDown(search(), { key: "Enter" });
      expect(state.openFileAtLine).toHaveBeenLastCalledWith("1:b.go", 1);
      fireEvent.mouseEnter(matches()[1]!);
      expect(matches()[1]!.getAttribute("aria-selected")).toBe("true");
      fireEvent.click(matches()[1]!);
      expect(state.openFileAtLine).toHaveBeenLastCalledWith("0:src/a.ts", 9);
      fireEvent.doubleClick(matches()[1]!);
      expect(state.promoteFile).toHaveBeenCalledWith("0:src/a.ts");
    });

    it("says when nothing matches and opens nothing on Enter", async () => {
      vi.mocked(grepFiles).mockResolvedValue(result({ files: [] }));
      const { state } = mount();
      contentOn();
      fireEvent.change(search(), { target: { value: "zzz" } });
      await waitFor(() => expect(screen.getByTestId("content-search-status").textContent).toBe("Nothing matches"));
      fireEvent.keyDown(search(), { key: "Enter" });
      expect(state.openFileAtLine).not.toHaveBeenCalled();
    });

    it("shows why the search failed", async () => {
      vi.mocked(grepFiles).mockRejectedValue(new Error("bad pattern"));
      mount();
      contentOn();
      fireEvent.change(search(), { target: { value: "((" } });
      expect((await screen.findByTestId("content-search-error")).textContent).toBe("bad pattern");
    });
  });

  describe("the context menu", () => {
    beforeEach(() => {
      Object.defineProperty(navigator, "clipboard", { value: { writeText: vi.fn(() => Promise.resolve()) }, configurable: true });
    });

    it("copies a file's relative and absolute paths", () => {
      mount();
      fireEvent.contextMenu(row("0:main.go"));
      expect(menuLabels()).toEqual(["Copy relative path", "Copy absolute path", "Copy", "Rename", "Delete"]);
      fireEvent.click(menuItem("Copy relative path"));
      expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("main.go");
      fireEvent.contextMenu(row("0:main.go"));
      fireEvent.click(menuItem("Copy absolute path"));
      expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("~/w/app/main.go");
    });

    it("uses the root's path without a display path, and the channel's directory without a root", async () => {
      vi.mocked(searchFiles).mockResolvedValue([hit({ root_index: 1, rel_path: "x.go", name: "x.go" }), hit({ root_index: 7, rel_path: "y.go", name: "y.go" })]);
      mount();
      fireEvent.change(search(), { target: { value: "go" } });
      const results = await screen.findAllByTestId("file-search-result");
      fireEvent.contextMenu(results[0]!);
      fireEvent.click(menuItem("Copy absolute path"));
      expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("/w/lib/x.go");
      fireEvent.contextMenu(results[1]!);
      fireEvent.click(menuItem("Copy absolute path"));
      expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith("/w/app/y.go");
    });

    it("offers no copy, rename or delete on a root", () => {
      mount();
      fireEvent.contextMenu(rootRow(0));
      expect(menuLabels()).toEqual(["Copy relative path", "Copy absolute path", "New file here", "New directory here"]);
    });

    it.each([
      ["a file", "0:main.go", "Delete main.go?", "handleDeleteFilePath"],
      ["a directory", "0:src", "Delete src and everything in it?", "handleDeleteDirPath"],
    ] as const)("deletes %s after asking", (_name, path, question, handler) => {
      const { state } = mount();
      fireEvent.contextMenu(row(path));
      fireEvent.click(menuItem("Delete"));
      const confirm = screen.getByTestId("context-menu-confirm");
      expect(confirm.textContent).toContain(question);
      fireEvent.click(within(confirm).getByRole("button", { name: "Yes" }));
      expect(state[handler]).toHaveBeenCalledWith(path);
    });

    it("closes on Escape", () => {
      mount();
      fireEvent.contextMenu(row("0:main.go"));
      fireEvent.keyDown(document, { key: "Escape" });
      expect(screen.queryByTestId("context-menu")).toBeNull();
    });
  });

  describe("rename", () => {
    const startRename = (path: string) => {
      fireEvent.contextMenu(row(path));
      fireEvent.click(menuItem("Rename"));
      return screen.getByTestId("file-tree-rename") as HTMLInputElement;
    };

    it("selects the name without its extension and renames on Enter", async () => {
      const { state } = mount({ expandedDirs: new Set(["0:", "0:src"]) });
      const input = startRename("0:src/a.ts");
      expect(document.activeElement).toBe(input);
      expect([input.value, input.selectionStart, input.selectionEnd]).toEqual(["src/a.ts", 4, 5]);
      fireEvent.change(input, { target: { value: "src/b.ts" } });
      fireEvent.keyDown(input, { key: "a" });
      await act(async () => fireEvent.keyDown(input, { key: "Enter" }));
      expect(state.handleRenamePath).toHaveBeenCalledWith("0:src/a.ts", "src/b.ts");
      expect(screen.queryByTestId("file-tree-rename")).toBeNull();
    });

    it("selects the whole name when it has no extension", () => {
      mount();
      const input = startRename("0:src");
      expect([input.selectionStart, input.selectionEnd]).toEqual([0, 3]);
    });

    it.each([
      ["its message", new Error("exists"), "exists"],
      ["a fallback", 1, "Failed to rename"],
    ])("keeps the input and shows %s when renaming fails, until Escape", async (_name, err, want) => {
      mount({ handleRenamePath: vi.fn(() => Promise.reject(err)) });
      const input = startRename("0:main.go");
      await act(async () => fireEvent.keyDown(input, { key: "Enter" }));
      expect(screen.getByTestId("file-tree-error").textContent).toBe(want);
      expect(screen.getByTestId("file-tree-rename")).toBeTruthy();
      fireEvent.keyDown(input, { key: "Escape" });
      expect(screen.queryByTestId("file-tree-rename")).toBeNull();
      expect(screen.queryByTestId("file-tree-error")).toBeNull();
    });
  });

  describe("copy and paste", () => {
    it("pastes a copied file beside a file and into a directory, from the menu", async () => {
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const { state } = mount({ handleCopyPath: vi.fn(() => Promise.resolve("0:main.go")) });
      fireEvent.contextMenu(row("0:main.go"));
      expect(menuLabels()).not.toContain("Paste");
      fireEvent.click(menuItem("Copy"));
      fireEvent.contextMenu(row("0:main.go"));
      expect(menuItem("Paste").textContent).toBe("Pastemain.go");
      await act(async () => fireEvent.click(menuItem("Paste")));
      expect(state.handleCopyPath).toHaveBeenLastCalledWith("0:main.go", false, "0:");
      expect(scroll.mock.contexts).toContain(row("0:main.go"));
      fireEvent.contextMenu(row("0:src"));
      await act(async () => fireEvent.click(menuItem("Paste")));
      expect(state.handleCopyPath).toHaveBeenLastCalledWith("0:main.go", false, "0:src");
      fireEvent.contextMenu(rootRow(0));
      expect(menuLabels()).toContain("Paste");
    });

    it.each([
      ["its message", new Error("disk full"), "disk full"],
      ["a fallback", null, "Failed to paste"],
    ])("shows %s when pasting fails", async (_name, err, want) => {
      mount({ handleCopyPath: vi.fn(() => Promise.reject(err)) });
      fireEvent.contextMenu(row("0:src"));
      fireEvent.click(menuItem("Copy"));
      fireEvent.contextMenu(row("0:main.go"));
      await act(async () => fireEvent.click(menuItem("Paste")));
      expect(screen.getByTestId("file-tree-error").textContent).toBe(want);
    });

    it("copies the focused row with Cmd/Ctrl+C and pastes it with Cmd/Ctrl+V", async () => {
      const { state } = mount();
      fireEvent.keyDown(row("0:src"), { key: "v", metaKey: true });
      expect(state.handleCopyPath).not.toHaveBeenCalled();
      fireEvent.keyDown(row("0:src"), { key: "C", ctrlKey: true });
      await act(async () => fireEvent.keyDown(row("0:main.go"), { key: "v", metaKey: true }));
      expect(state.handleCopyPath).toHaveBeenLastCalledWith("0:src", true, "0:");
      await act(async () => fireEvent.keyDown(rootRow(1), { key: "v", ctrlKey: true }));
      expect(state.handleCopyPath).toHaveBeenLastCalledWith("0:src", true, "1:");
    });

    it("leaves other keys, a root, selected text and the space between rows alone", () => {
      const { state } = mount();
      fireEvent.keyDown(row("0:main.go"), { key: "c" });
      fireEvent.keyDown(row("0:main.go"), { key: "c", metaKey: true, shiftKey: true });
      fireEvent.keyDown(row("0:main.go"), { key: "c", metaKey: true, altKey: true });
      fireEvent.keyDown(row("0:main.go"), { key: "x", metaKey: true });
      fireEvent.keyDown(rootRow(0), { key: "c", metaKey: true });
      fireEvent.keyDown(row("0:main.go").parentElement!.parentElement!, { key: "c", metaKey: true });
      vi.spyOn(window, "getSelection").mockReturnValue({ toString: () => "main" } as Selection);
      fireEvent.keyDown(row("0:main.go"), { key: "c", metaKey: true });
      fireEvent.keyDown(row("0:src"), { key: "v", metaKey: true });
      expect(state.handleCopyPath).not.toHaveBeenCalled();
      fireEvent.contextMenu(row("0:src"));
      expect(menuLabels()).not.toContain("Paste");
    });

    it("copies when the selection is empty", async () => {
      const { state } = mount();
      vi.spyOn(window, "getSelection").mockReturnValue(null);
      fireEvent.keyDown(row("0:main.go"), { key: "c", metaKey: true });
      await act(async () => fireEvent.keyDown(row("0:src"), { key: "v", metaKey: true }));
      expect(state.handleCopyPath).toHaveBeenCalledWith("0:main.go", false, "0:src");
    });
  });
});
