import { EditorSelection } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { type ComponentProps, createRef } from "react";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../../ThemeContext";
import { CodeEditor, type CodeEditorHandle, formatSize, getLangExtension, isHtmlFile, isMarkdownFile, previewSource } from "./CodeEditor";
import { emptyGitLineChanges } from "./editorGitGutter";
import type { WalkthroughCard } from "./editorWalkthrough";

// pdf.js doesn't run in jsdom; the stub shows which URL the viewer was given.
vi.mock("./PdfViewer", () => ({ default: ({ url }: { url: string }) => <div data-testid="pdf-viewer">{url}</div> }));

type Props = ComponentProps<typeof CodeEditor>;

const baseProps = (): Props => ({
  fileContent: "line one\nline two\nline three\n",
  isBinary: false,
  binarySize: 0,
  selectedRelPath: "main.go",
  selectedPath: "0:main.go",
  loading: false,
  error: null,
  previewMode: "editor",
  onDocChanged: vi.fn(),
  onPreviewUpdate: vi.fn(),
  editorMenu: null,
  onEditorMenuClose: vi.fn(),
  onEditorContextMenu: vi.fn(),
  previewHtml: "",
});

const renderEditor = (overrides: Partial<Props> = {}) => {
  const ref = createRef<CodeEditorHandle>();
  let props: Props = { ...baseProps(), ...overrides };
  const ui = (p: Props) => (
    <ThemeProvider initialTheme="dark">
      <CodeEditor ref={ref} {...p} />
    </ThemeProvider>
  );
  const result = render(ui(props));
  const rerender = (next: Partial<Props>) => {
    props = { ...props, ...next };
    result.rerender(ui(props));
  };
  const view = () => {
    const dom = result.container.querySelector<HTMLElement>(".cm-editor");
    return dom ? EditorView.findFromDOM(dom) : null;
  };
  return { ...result, ref, props: () => props, rerender, view };
};

describe("getLangExtension", () => {
  it.each(["a.js", "a.ts", "a.tsx", "a.go", "a.py", "a.json", "a.md", "a.css", "a.html", "a.yaml", "a.rs", "a.sql", "a.toml", "a.sh", "Dockerfile"])("gives a language for %s", (name) => {
    expect(getLangExtension(name)).not.toBeNull();
  });

  it("gives none for an unknown file", () => {
    expect(getLangExtension("notes.txt")).toBeNull();
  });
});

describe("file kinds", () => {
  it.each([
    ["README.md", true, false],
    ["page.MDX", true, false],
    ["index.html", false, true],
    ["old.HTM", false, true],
    ["main.go", false, false],
  ])("%s: markdown %s, html %s", (path, md, html) => {
    expect(isMarkdownFile(path)).toBe(md);
    expect(isHtmlFile(path)).toBe(html);
  });

  it("previews markdown rendered, HTML as its source, and nothing else", () => {
    expect(previewSource("a.md", "# Hi")).toContain("<h1");
    expect(previewSource("a.html", "<p>x</p>")).toBe("<p>x</p>");
    expect(previewSource("a.go", "package main")).toBe("");
  });

  it.each([
    [512, "512 B"],
    [2048, "2.0 KB"],
    [3 * 1024 * 1024, "3.0 MB"],
  ])("formats %d bytes as %s", (bytes, want) => {
    expect(formatSize(bytes)).toBe(want);
  });
});

describe("CodeEditor", () => {
  // jsdom's Range has no layout; CodeMirror measures text through it.
  beforeAll(() => {
    Range.prototype.getClientRects = () => ({ length: 0, item: () => null, [Symbol.iterator]: [][Symbol.iterator] }) as unknown as DOMRectList;
    Range.prototype.getBoundingClientRect = () => new DOMRect();
  });
  afterAll(() => {
    delete (Range.prototype as Partial<Range>).getClientRects;
    delete (Range.prototype as Partial<Range>).getBoundingClientRect;
  });

  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("states without a text editor", () => {
    it("shows loading, the error and the empty state", () => {
      renderEditor({ fileContent: null, selectedPath: null, selectedRelPath: null, loading: true, error: "boom" });
      expect(screen.getByText("Loading...")).toBeTruthy();
      expect(screen.getByText("boom")).toBeTruthy();
      expect(screen.queryByText("Select a file")).toBeNull();
    });

    it("asks to select a file when none is open", () => {
      const { view } = renderEditor({ fileContent: null, selectedPath: null, selectedRelPath: null });
      expect(screen.getByText("Select a file")).toBeTruthy();
      expect(view()).toBeNull();
    });

    it("shows a binary file's size", () => {
      const { view } = renderEditor({ fileContent: null, isBinary: true, binarySize: 2048 });
      expect(screen.getByText("Binary file (2.0 KB)")).toBeTruthy();
      expect(view()).toBeNull();
    });

    it("shows an image, and reports when it fails to load", () => {
      const onMediaError = vi.fn();
      const { container } = renderEditor({ fileContent: null, isBinary: true, selectedRelPath: "a.png", imageURL: "blob:img", onMediaError });
      expect(screen.queryByText(/Binary file/)).toBeNull();
      const img = container.querySelector("img")!;
      expect(img.getAttribute("src")).toBe("blob:img");
      fireEvent.error(img);
      expect(onMediaError).toHaveBeenCalledTimes(1);
    });

    it("plays a video", () => {
      const onMediaError = vi.fn();
      const { container } = renderEditor({ fileContent: null, isBinary: true, selectedRelPath: "a.mp4", imageURL: "blob:vid", onMediaError });
      const video = container.querySelector("video")!;
      expect(video.getAttribute("src")).toBe("blob:vid");
      fireEvent.error(video);
      expect(onMediaError).toHaveBeenCalledTimes(1);
    });

    it("opens a PDF in the PDF viewer", async () => {
      renderEditor({ fileContent: null, isBinary: true, selectedRelPath: "doc.pdf", imageURL: "blob:pdf" });
      expect((await screen.findByTestId("pdf-viewer")).textContent).toBe("blob:pdf");
    });

    it("treats a media URL without a path as an image", () => {
      const { container } = renderEditor({ fileContent: null, isBinary: true, selectedRelPath: null, imageURL: "blob:img" });
      expect(container.querySelector("img")).toBeTruthy();
    });
  });

  describe("the text editor", () => {
    it("opens the file and reports the initial preview, empty for code", () => {
      const onPreviewUpdate = vi.fn();
      const { view } = renderEditor({ onPreviewUpdate });
      expect(view()?.state.doc.toString()).toBe("line one\nline two\nline three\n");
      expect(onPreviewUpdate).toHaveBeenCalledWith("");
    });

    it("reports an empty preview for a file without a path", () => {
      const onPreviewUpdate = vi.fn();
      renderEditor({ selectedRelPath: null, onPreviewUpdate });
      expect(onPreviewUpdate).toHaveBeenCalledWith("");
    });

    it("tears the editor down when the file goes and rebuilds it for the next", () => {
      const onSelectionChange = vi.fn();
      const { view, rerender } = renderEditor({ onSelectionChange });
      expect(view()).not.toBeNull();
      rerender({ fileContent: null });
      expect(view()).toBeNull();
      expect(onSelectionChange).toHaveBeenLastCalledWith(null);
      rerender({ fileContent: "next", selectedPath: "0:next.go" });
      expect(view()?.state.doc.toString()).toBe("next");
    });

    it("reports user edits and the selection, not replaced content", () => {
      const onDocChanged = vi.fn();
      const onSelectionChange = vi.fn();
      const { view, ref } = renderEditor({ onDocChanged, onSelectionChange });
      act(() => view()!.dispatch({ changes: { from: 0, insert: "x" } }));
      expect(onDocChanged).toHaveBeenCalledTimes(1);
      act(() => ref.current!.replaceContent("brand new\n"));
      expect(onDocChanged).toHaveBeenCalledTimes(1);
      expect(view()?.state.doc.toString()).toBe("brand new\n");
      act(() => ref.current!.replaceContent("brand new\n"));
      act(() => view()!.dispatch({ selection: EditorSelection.single(0, 5) }));
      expect(onSelectionChange).toHaveBeenLastCalledWith({ start_line: 1, end_line: 1, text: "brand" });
    });

    it("updates a markdown preview a moment after an edit", async () => {
      const onPreviewUpdate = vi.fn();
      const { view } = renderEditor({ fileContent: "# One\n", selectedRelPath: "README.md", onPreviewUpdate });
      expect(onPreviewUpdate).toHaveBeenLastCalledWith(expect.stringContaining("One</h1>"));
      act(() => view()!.dispatch({ changes: { from: 2, to: 5, insert: "Two" } }));
      act(() => view()!.dispatch({ changes: { from: 2, to: 5, insert: "Six" } }));
      await act(() => vi.advanceTimersByTimeAsync(300));
      expect(onPreviewUpdate).toHaveBeenCalledTimes(2);
      expect(onPreviewUpdate).toHaveBeenLastCalledWith(expect.stringContaining("Six</h1>"));
    });

    it("passes an HTML file's source to its preview after an edit", async () => {
      const onPreviewUpdate = vi.fn();
      const { view } = renderEditor({ fileContent: "<p>a</p>", selectedRelPath: "index.html", onPreviewUpdate });
      act(() => view()!.dispatch({ changes: { from: 3, to: 4, insert: "b" } }));
      await act(() => vi.advanceTimersByTimeAsync(300));
      expect(onPreviewUpdate).toHaveBeenLastCalledWith("<p>b</p>");
    });

    it("doesn't update a preview for code or a file without a path", async () => {
      const onPreviewUpdate = vi.fn();
      const { view } = renderEditor({ selectedRelPath: null, onPreviewUpdate });
      act(() => view()!.dispatch({ changes: { from: 0, insert: "x" } }));
      await act(() => vi.advanceTimersByTimeAsync(300));
      expect(onPreviewUpdate).toHaveBeenCalledTimes(1);
    });

    it("follows the theme, compare text, indent and git changes without rebuilding", () => {
      const { view, rerender, container } = renderEditor();
      const first = view();
      rerender({ compareWith: "line one\nline 2\nline three\n", compareSource: "head" });
      expect(container.querySelector("[data-testid=compare-reject]")?.textContent).toBe("Revert");
      rerender({ tabSize: 2, insertSpaces: false });
      expect(view()?.state.tabSize).toBe(2);
      const changes = emptyGitLineChanges();
      changes.changed.set(1, "added");
      rerender({ gitChanges: changes });
      expect(container.querySelector(".cm-gitChange-added")).toBeTruthy();
      expect(screen.getByTestId("git-overview-ruler")).toBeTruthy();
      rerender({ gitChanges: null });
      expect(container.querySelector(".cm-gitChange-added")).toBeNull();
      expect(view()).toBe(first);
    });

    it("seeds the git gutter from the changes it opens with", () => {
      const changes = emptyGitLineChanges();
      changes.changed.set(2, "modified");
      const { container } = renderEditor({ gitChanges: changes });
      expect(container.querySelector(".cm-gitChange-modified")).toBeTruthy();
    });

    it("leaves the reconfiguring effects alone while no file is open", () => {
      const { rerender, view } = renderEditor({ fileContent: null });
      rerender({ gitChanges: emptyGitLineChanges(), compareWith: "x", tabSize: 2 });
      expect(view()).toBeNull();
    });

    it("jumps to a line from the overview ruler", () => {
      const changes = emptyGitLineChanges();
      changes.changed.set(3, "added");
      const { view } = renderEditor({ gitChanges: changes });
      const ruler = screen.getByTestId("git-overview-ruler");
      vi.spyOn(ruler, "getBoundingClientRect").mockReturnValue({ top: 0, height: 100 } as DOMRect);
      fireEvent.click(ruler, { clientY: 75 });
      const v = view()!;
      expect(v.state.doc.lineAt(v.state.selection.main.head).number).toBe(3);
    });

    it("ignores overview ruler clicks when no editor view is mounted", () => {
      const changes = emptyGitLineChanges();
      changes.changed.set(2, "added");
      const { view } = renderEditor({ isBinary: true, gitChanges: changes });
      expect(view()).toBeNull();
      const ruler = screen.getByTestId("git-overview-ruler");
      vi.spyOn(ruler, "getBoundingClientRect").mockReturnValue({ top: 0, height: 100 } as DOMRect);
      fireEvent.click(ruler, { clientY: 50 });
      expect(view()).toBeNull();
    });
  });

  describe("the imperative handle", () => {
    it("does nothing without a file", () => {
      const { ref } = renderEditor({ fileContent: null });
      const h = ref.current!;
      expect(h.getContent()).toBeNull();
      expect(h.getSelection()).toBeNull();
      h.replaceContent("x");
      h.appendNewlineIfMissing();
      h.selectAll();
      h.openSearch();
      h.replaceSelection("x");
      h.scrollToLine(2);
      expect(h.getContent()).toBeNull();
    });

    it("reads, selects, replaces and searches the document", () => {
      const { ref, view, container } = renderEditor({ fileContent: "abc" });
      const h = ref.current!;
      expect(h.getContent()).toBe("abc");
      act(() => h.appendNewlineIfMissing());
      expect(h.getContent()).toBe("abc\n");
      act(() => h.appendNewlineIfMissing());
      expect(h.getContent()).toBe("abc\n");
      act(() => h.selectAll());
      expect(h.getSelection()).toEqual({ from: 0, to: 4, text: "abc\n" });
      act(() => h.replaceSelection("xyz"));
      expect(h.getContent()).toBe("xyz");
      act(() => h.openSearch());
      expect(container.querySelector(".cm-search")).toBeTruthy();
      expect(view()).not.toBeNull();
    });

    it("leaves an empty document without a newline", () => {
      const { ref } = renderEditor({ fileContent: "" });
      act(() => ref.current!.appendNewlineIfMissing());
      expect(ref.current!.getContent()).toBe("");
    });

    it("moves the cursor to a line, clamped to the document", () => {
      const { ref, view } = renderEditor();
      const line = () => {
        const v = view()!;
        return v.state.doc.lineAt(v.state.selection.main.head).number;
      };
      act(() => ref.current!.scrollToLine(2));
      expect(line()).toBe(2);
      act(() => ref.current!.scrollToLine(99));
      expect(line()).toBe(4);
      act(() => ref.current!.scrollToLine(-3));
      expect(line()).toBe(1);
    });
  });

  describe("the tab's view", () => {
    it("restores the saved selection and reports changes, settled", async () => {
      const onTabViewChange = vi.fn();
      const tabViewFor = vi.fn(() => ({ anchor: 5, head: 8, top: 9 }));
      const { view } = renderEditor({ tabViewFor, onTabViewChange });
      expect(tabViewFor).toHaveBeenCalledWith("0:main.go");
      expect(view()?.state.selection.main).toMatchObject({ anchor: 5, head: 8 });
      act(() => view()!.dispatch({ selection: EditorSelection.cursor(2) }));
      act(() => view()!.dispatch({ selection: EditorSelection.cursor(3) }));
      expect(onTabViewChange).not.toHaveBeenCalled();
      await act(() => vi.advanceTimersByTimeAsync(250));
      expect(onTabViewChange).toHaveBeenCalledExactlyOnceWith("0:main.go", { anchor: 3, head: 3, top: 9 });
    });

    it("starts at the top without a saved view and reports a pending change on close", () => {
      const onTabViewChange = vi.fn();
      const { view, unmount } = renderEditor({ tabViewFor: () => undefined, onTabViewChange });
      expect(view()?.state.selection.main.head).toBe(0);
      act(() => view()!.dispatch({ selection: EditorSelection.cursor(4) }));
      unmount();
      expect(onTabViewChange).toHaveBeenCalledExactlyOnceWith("0:main.go", { anchor: 4, head: 4, top: 0 });
    });

    it("reports nothing for a file without a path key", async () => {
      const onTabViewChange = vi.fn();
      const tabViewFor = vi.fn();
      const { view } = renderEditor({ selectedPath: null, tabViewFor, onTabViewChange });
      act(() => view()!.dispatch({ selection: EditorSelection.cursor(4) }));
      await act(() => vi.advanceTimersByTimeAsync(250));
      expect(tabViewFor).not.toHaveBeenCalled();
      expect(onTabViewChange).not.toHaveBeenCalled();
    });

    it("tracks the line at the top once the restore's own scrolls have landed", async () => {
      const onTabViewChange = vi.fn();
      const { view, container } = renderEditor({ tabViewFor: () => ({ anchor: 0, head: 0, top: 0 }), onTabViewChange });
      const scroller = container.querySelector<HTMLElement>(".cm-editor")!.parentElement!;
      const v = view()!;
      vi.spyOn(v, "lineBlockAtHeight").mockReturnValue({ from: 9 } as ReturnType<EditorView["lineBlockAtHeight"]>);
      // Still restoring: the scroll isn't the user's.
      fireEvent.scroll(scroller);
      await act(() => vi.advanceTimersByTimeAsync(300));
      expect(onTabViewChange).not.toHaveBeenCalled();
      await act(() => vi.advanceTimersToNextFrame());
      await act(() => vi.advanceTimersToNextFrame());
      fireEvent.scroll(scroller);
      fireEvent.scroll(scroller);
      await act(() => vi.advanceTimersByTimeAsync(250));
      expect(onTabViewChange).toHaveBeenCalledExactlyOnceWith("0:main.go", { anchor: 0, head: 0, top: 9 });
    });

    it("scrolls back to the saved top line", () => {
      const scrollIntoView = vi.spyOn(EditorView, "scrollIntoView");
      renderEditor({ tabViewFor: () => ({ anchor: 0, head: 0, top: 9 }) });
      expect(scrollIntoView).toHaveBeenCalledWith(9, { y: "start", yMargin: 0 });
    });
  });

  describe("previews", () => {
    it("shows the markdown preview beside the editor and syncs their scroll", async () => {
      const { container } = renderEditor({ fileContent: "# Hi\n", selectedRelPath: "README.md", previewMode: "both", previewHtml: "<h1>Hi</h1>" });
      const preview = container.querySelector<HTMLElement>(".readme-content")!;
      expect(preview.innerHTML).toBe("<h1>Hi</h1>");
      const scroller = container.querySelector<HTMLElement>(".cm-editor")!.parentElement!;
      for (const [el, height] of [
        [preview, 300],
        [scroller, 600],
      ] as const) {
        Object.defineProperty(el, "scrollHeight", { configurable: true, value: height });
        Object.defineProperty(el, "clientHeight", { configurable: true, value: 100 });
      }

      // Preview -> editor; the editor's echo scroll is ignored until the next frame.
      preview.scrollTop = 100;
      fireEvent.scroll(preview);
      expect(scroller.scrollTop).toBe(250);
      preview.scrollTop = 0;
      fireEvent.scroll(scroller);
      expect(preview.scrollTop).toBe(0);
      await act(() => vi.advanceTimersToNextFrame());

      // Editor -> preview; the preview's echo is ignored the same way.
      scroller.scrollTop = 500;
      fireEvent.scroll(scroller);
      expect(preview.scrollTop).toBe(200);
      scroller.scrollTop = 0;
      fireEvent.scroll(preview);
      expect(scroller.scrollTop).toBe(0);
      await act(() => vi.advanceTimersToNextFrame());
    });

    it("hides the editor and the ruler in preview-only mode", () => {
      const changes = emptyGitLineChanges();
      changes.changed.set(1, "added");
      const { container } = renderEditor({ fileContent: "# Hi\n", selectedRelPath: "README.md", previewMode: "preview", previewHtml: "<h1>Hi</h1>", gitChanges: changes });
      expect(container.querySelector<HTMLElement>(".cm-editor")!.parentElement!.style.display).toBe("none");
      expect(screen.queryByTestId("git-overview-ruler")).toBeNull();
    });

    it("shows no markdown preview in editor mode or before it has rendered", () => {
      const { container, rerender } = renderEditor({ fileContent: "# Hi\n", selectedRelPath: "README.md", previewMode: "editor", previewHtml: "<h1>Hi</h1>" });
      expect(container.querySelector(".readme-content")).toBeNull();
      rerender({ previewMode: "both", previewHtml: "" });
      expect(container.querySelector(".readme-content")).toBeNull();
    });

    it("renders an HTML preview in a sandboxed frame with the base URL", () => {
      const { rerender } = renderEditor({ fileContent: "<p>x</p>", selectedRelPath: "index.html", previewMode: "both", previewHtml: "<p>x</p>", htmlBaseURL: "http://files/" });
      const frame = screen.getByTestId("html-preview");
      expect(frame.getAttribute("sandbox")).toBe("allow-scripts");
      expect(frame.getAttribute("srcdoc")).toContain('<base href="http://files/">');
      rerender({ previewMode: "preview", htmlBaseURL: undefined });
      expect(screen.getByTestId("html-preview").getAttribute("srcdoc")).toContain('<base href="">');
      rerender({ previewMode: "editor" });
      expect(screen.queryByTestId("html-preview")).toBeNull();
    });
  });

  describe("the walkthrough", () => {
    const card = (start: number): WalkthroughCard => ({ walkthroughTitle: "Tour", index: 0, count: 2, step: { path: "main.go", start_line: start, end_line: start } });

    it("shows the step's card, scrolls to it and forwards the card's buttons", async () => {
      const prev = vi.fn();
      const next = vi.fn();
      const close = vi.fn();
      const ask = vi.fn();
      const togglePlay = vi.fn();
      const pause = vi.fn();
      const scrollIntoView = vi.spyOn(EditorView, "scrollIntoView");
      const { container, rerender } = renderEditor({ walkthrough: { ...card(2), index: 1, count: 3 }, walkthroughHandlers: { prev, next, close, ask, togglePlay, pause } });
      await act(() => vi.advanceTimersToNextFrame());
      expect(scrollIntoView).toHaveBeenCalledWith(9, { y: "start", yMargin: 16 });
      const button = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>(".cm-walkthrough-btn")).find((b) => b.textContent === label)!;
      button("Prev").click();
      button("Next").click();
      button("Play").click();
      button("Close").click();
      button("Ask about this step").click();
      expect([prev, next, togglePlay, close].map((f) => f.mock.calls.length)).toEqual([1, 1, 1, 1]);
      expect(ask).toHaveBeenCalledWith({ start_line: 2, end_line: 2, text: "line two" });
      rerender({ walkthrough: null });
      expect(container.querySelector(".cm-walkthrough-card")).toBeNull();
    });

    it("pauses the walkthrough on a user edit, and ignores the card without handlers", () => {
      const pause = vi.fn();
      const handlers = { prev: vi.fn(), next: vi.fn(), close: vi.fn(), ask: vi.fn(), togglePlay: vi.fn(), pause };
      const { view, rerender, container } = renderEditor({ walkthrough: card(2), walkthroughHandlers: handlers });
      act(() => view()!.dispatch({ changes: { from: 0, insert: "x" }, userEvent: "input.type" }));
      expect(pause).toHaveBeenCalledTimes(1);
      rerender({ walkthroughHandlers: undefined });
      const buttons = Array.from(container.querySelectorAll<HTMLButtonElement>(".cm-walkthrough-btn"));
      for (const b of buttons) b.click();
      act(() => view()!.dispatch({ changes: { from: 0, insert: "y" }, userEvent: "input.type" }));
      expect(pause).toHaveBeenCalledTimes(1);
      expect(handlers.close).not.toHaveBeenCalled();
    });

    it("scrolls again once a card that wasn't drawn yet is in view", async () => {
      const scrollIntoView = vi.spyOn(EditorView, "scrollIntoView");
      const { view } = renderEditor({ walkthrough: card(3) });
      const v = view()!;
      const query = vi.spyOn(v.dom, "querySelector").mockReturnValue(null);
      await act(() => vi.advanceTimersToNextFrame());
      await act(() => vi.advanceTimersToNextFrame());
      await act(() => vi.advanceTimersToNextFrame());
      expect(query).toHaveBeenCalledWith(".cm-walkthrough-card");
      expect(scrollIntoView.mock.calls.filter(([, o]) => o?.yMargin === 16)).toHaveLength(2);
    });
  });

  describe("the context menu", () => {
    const menuAt = { x: 10, y: 10 };
    const item = (label: string) => screen.getByRole("menuitem", { name: label });

    it("offers Select All and Find without a selection, and runs them", async () => {
      const onEditorMenuClose = vi.fn();
      // The menu reads the editor as it opens, after the file is in it.
      const { view, rerender, container } = renderEditor({ onEditorMenuClose });
      rerender({ editorMenu: menuAt });
      expect(screen.getAllByRole("menuitem").map((b) => b.textContent)).toEqual(["Select All", "Find..."]);
      fireEvent.click(item("Select All"));
      expect(view()?.state.selection.main).toMatchObject({ from: 0, to: 29 });
      expect(onEditorMenuClose).toHaveBeenCalled();
      rerender({ editorMenu: null });
      rerender({ editorMenu: { x: 11, y: 11 } });
      fireEvent.click(item("Find..."));
      expect(container.querySelector(".cm-search")).toBeTruthy();
    });

    it("copies, cuts and asks about the selection", async () => {
      const writeText = vi.fn();
      Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
      const onAskAgent = vi.fn();
      const { view, rerender } = renderEditor({ onAskAgent });
      act(() => view()!.dispatch({ selection: EditorSelection.single(0, 4) }));
      rerender({ editorMenu: menuAt });
      expect(screen.getAllByRole("menuitem").map((b) => b.textContent)).toEqual(["Copy", "Cut", "Ask the agent about this", "Select All", "Find..."]);
      fireEvent.click(item("Ask the agent about this"));
      expect(onAskAgent).toHaveBeenCalledWith({ start_line: 1, end_line: 1, text: "line" });
      rerender({ editorMenu: null });
      rerender({ editorMenu: { x: 12, y: 12 } });
      fireEvent.click(item("Copy"));
      expect(writeText).toHaveBeenLastCalledWith("line");
      rerender({ editorMenu: null });
      rerender({ editorMenu: { x: 13, y: 13 } });
      fireEvent.click(item("Cut"));
      expect(writeText).toHaveBeenCalledTimes(2);
      expect(view()?.state.doc.toString()).toBe(" one\nline two\nline three\n");
    });

    it("leaves out Ask without a selection to ask about", () => {
      renderEditor({ onAskAgent: vi.fn(), editorMenu: menuAt });
      expect(screen.queryByRole("menuitem", { name: "Ask the agent about this" })).toBeNull();
    });

    it("offers Select All and Find that do nothing while no file is open", async () => {
      const onEditorMenuClose = vi.fn();
      renderEditor({ fileContent: null, editorMenu: menuAt, onEditorMenuClose, onAskAgent: vi.fn() });
      expect(screen.getAllByRole("menuitem").map((b) => b.textContent)).toEqual(["Select All", "Find..."]);
      fireEvent.click(item("Select All"));
      expect(onEditorMenuClose).toHaveBeenCalledTimes(1);
    });

    it("runs Find with no file open", async () => {
      const onEditorMenuClose = vi.fn();
      renderEditor({ fileContent: null, editorMenu: menuAt, onEditorMenuClose });
      fireEvent.click(item("Find..."));
      expect(onEditorMenuClose).toHaveBeenCalledTimes(1);
    });

    it("forwards the editor's context menu event", () => {
      const onEditorContextMenu = vi.fn();
      const { container } = renderEditor({ onEditorContextMenu });
      fireEvent.contextMenu(container.querySelector(".cm-content")!);
      expect(onEditorContextMenu).toHaveBeenCalledTimes(1);
    });
  });
});
