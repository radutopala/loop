import { EditorView } from "@codemirror/view";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { createRef, type MutableRefObject } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider, useTheme } from "../../ThemeContext";
import { MemoryFileViewer, type MemoryFileViewerProps } from "./MemoryFileViewer";

function LightThemeSwitcher() {
  const { setThemeName } = useTheme();
  return (
    <button type="button" onClick={() => setThemeName("light")}>
      light
    </button>
  );
}

function makeProps(overrides: Partial<MemoryFileViewerProps> = {}): MemoryFileViewerProps {
  return {
    selectedPath: "/mem/a.md",
    fileContent: "# Title",
    contentError: null,
    openTabs: ["/mem/a.md", "/mem/b.md"],
    dirtyTabs: new Set(),
    previewTab: null,
    viewRef: createRef<EditorView | null>() as MutableRefObject<EditorView | null>,
    onSwitchToTab: vi.fn(),
    onCloseTab: vi.fn(),
    onSetPreviewTab: vi.fn(),
    onMarkDirty: vi.fn(),
    onSaveAllDirty: vi.fn(),
    ...overrides,
  };
}

function editorHost(container: HTMLElement): HTMLDivElement {
  return container.querySelector(".cm-editor")?.parentElement as HTMLDivElement;
}

// Sets the scroll geometry jsdom leaves at zero.
function setScroll(el: HTMLElement, top: number, height: number, client: number) {
  Object.defineProperty(el, "scrollHeight", { value: height, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: client, configurable: true });
  el.scrollTop = top;
}

describe("MemoryFileViewer", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("asks for a file when nothing is selected", () => {
    const { container } = render(<MemoryFileViewer {...makeProps({ selectedPath: null, fileContent: null, openTabs: [] })} />);
    expect(screen.getByText("Select a file")).toBeTruthy();
    expect(screen.queryByTitle("Editor only")).toBeNull();
    expect(container.querySelector(".cm-editor")).toBeNull();
  });

  it("shows loading and missing-file states", () => {
    const props = makeProps({ fileContent: null });
    const { rerender } = render(<MemoryFileViewer {...props} />);
    expect(screen.getByText("Loading...")).toBeTruthy();
    rerender(<MemoryFileViewer {...props} contentError="gone" />);
    expect(screen.getByText("File not available on disk")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("mounts an editor with the content and publishes it on the ref", () => {
    const props = makeProps();
    const { container } = render(<MemoryFileViewer {...props} />);
    const view = props.viewRef.current as EditorView;
    expect(view.state.doc.toString()).toBe("# Title");
    expect(EditorView.findFromDOM(container.querySelector(".cm-editor") as HTMLElement)).toBe(view);
  });

  it("replaces a view already on the ref and drops it when the content goes away", () => {
    const stale = { destroy: vi.fn() } as unknown as EditorView;
    const viewRef = { current: stale } as MutableRefObject<EditorView | null>;
    const props = makeProps({ viewRef });
    const { rerender } = render(<MemoryFileViewer {...props} />);
    expect(stale.destroy).toHaveBeenCalled();
    const mounted = viewRef.current as EditorView;
    expect(mounted).not.toBe(stale);

    rerender(<MemoryFileViewer {...props} fileContent={null} />);
    expect(viewRef.current).toBeNull();

    // A view left on the ref while there is no content is destroyed too.
    const other = { destroy: vi.fn() } as unknown as EditorView;
    viewRef.current = other;
    rerender(<MemoryFileViewer {...props} fileContent={null} selectedPath="/mem/b.md" />);
    expect(other.destroy).toHaveBeenCalled();
    expect(viewRef.current).toBeNull();
  });

  it("rebuilds the editor when the selected file changes and cleans up on unmount", () => {
    const props = makeProps();
    const { rerender, unmount } = render(<MemoryFileViewer {...props} />);
    const first = props.viewRef.current as EditorView;
    rerender(<MemoryFileViewer {...props} selectedPath="/mem/b.md" fileContent="other" />);
    const second = props.viewRef.current as EditorView;
    expect(second).not.toBe(first);
    expect(second.state.doc.toString()).toBe("other");
    unmount();
    expect(props.viewRef.current).toBeNull();
  });

  it("marks the file dirty and refreshes the preview after an edit", () => {
    const props = makeProps();
    render(<MemoryFileViewer {...props} />);
    fireEvent.click(screen.getByTitle("Editor + Preview"));
    expect(screen.getByRole("heading", { name: "Title" })).toBeTruthy();
    vi.useFakeTimers();
    const view = props.viewRef.current as EditorView;
    act(() => view.dispatch({ changes: { from: view.state.doc.length, insert: "!" } }));
    act(() => view.dispatch({ changes: { from: view.state.doc.length, insert: "?" } }));
    expect(props.onMarkDirty).toHaveBeenCalledTimes(2);
    act(() => vi.advanceTimersByTime(300));
    expect(screen.getByRole("heading", { name: "Title!?" })).toBeTruthy();
    // A selection change is not an edit.
    act(() => view.dispatch({ selection: { anchor: 0 } }));
    expect(props.onMarkDirty).toHaveBeenCalledTimes(2);
  });

  it("switches between editor, split and preview modes", () => {
    const { container } = render(<MemoryFileViewer {...makeProps()} />);
    const host = editorHost(container);
    expect(container.querySelector(".readme-content")).toBeNull();
    fireEvent.click(screen.getByText("Split"));
    expect(container.querySelector(".readme-content")).not.toBeNull();
    expect(host.style.display).toBe("");
    fireEvent.click(screen.getByText("Preview"));
    expect(host.style.display).toBe("none");
    fireEvent.click(screen.getByText("Edit"));
    expect(container.querySelector(".readme-content")).toBeNull();
  });

  it("renders no preview for empty content", () => {
    const { container } = render(<MemoryFileViewer {...makeProps({ fileContent: "" })} />);
    fireEvent.click(screen.getByText("Split"));
    expect(container.querySelector(".readme-content")).toBeNull();
  });

  it("wires tab clicks, double clicks and close", () => {
    const props = makeProps({ previewTab: "/mem/b.md", dirtyTabs: new Set(["/mem/a.md"]), openTabs: ["/mem/a.md", "/mem/b.md", "/mem/dir/"] });
    render(<MemoryFileViewer {...props} />);
    const active = screen.getByTitle("/mem/a.md");
    const preview = screen.getByTitle("/mem/b.md");
    // A path ending in a slash falls back to the full path as its name.
    expect(screen.getByTitle("/mem/dir/").textContent).toContain("/mem/dir/");

    fireEvent.click(active);
    expect(props.onSwitchToTab).not.toHaveBeenCalled();
    fireEvent.click(preview);
    expect(props.onSwitchToTab).toHaveBeenCalledWith("/mem/b.md");

    fireEvent.doubleClick(active);
    expect(props.onSetPreviewTab).not.toHaveBeenCalled();
    fireEvent.doubleClick(preview);
    expect(props.onSetPreviewTab).toHaveBeenCalledWith(null);

    // Dirty and preview tabs show italic names; the clean one does not.
    expect((screen.getByText("a.md") as HTMLElement).style.fontStyle).toBe("italic");
    expect((screen.getByText("b.md") as HTMLElement).style.fontStyle).toBe("italic");
    expect((screen.getByText("/mem/dir/") as HTMLElement).style.fontStyle).toBe("");

    // The dirty tab shows a dot instead of the close glyph.
    expect(active.textContent).not.toContain("×");
    const close = screen.getAllByText("×")[0] as HTMLElement;
    fireEvent.mouseEnter(close);
    expect(close.style.opacity).toBe("1");
    fireEvent.mouseLeave(close);
    expect(close.style.opacity).toBe("0.5");
    fireEvent.click(close);
    expect(props.onCloseTab).toHaveBeenCalledWith("/mem/b.md", expect.anything());
  });

  it("highlights hover on inactive tabs only", () => {
    render(<MemoryFileViewer {...makeProps()} />);
    const active = screen.getByTitle("/mem/a.md");
    const inactive = screen.getByTitle("/mem/b.md");
    const activeBg = active.style.backgroundColor;
    fireEvent.mouseEnter(active);
    fireEvent.mouseLeave(active);
    expect(active.style.backgroundColor).toBe(activeBg);
    fireEvent.mouseEnter(inactive);
    expect(inactive.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(inactive);
    expect(inactive.style.backgroundColor).toBe("transparent");
  });

  it("saves on Cmd+S or Ctrl+S only", () => {
    const props = makeProps();
    const { unmount } = render(<MemoryFileViewer {...props} />);
    fireEvent.keyDown(window, { key: "s" });
    fireEvent.keyDown(window, { key: "a", metaKey: true });
    expect(props.onSaveAllDirty).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "s", metaKey: true });
    fireEvent.keyDown(window, { key: "s", ctrlKey: true });
    expect(props.onSaveAllDirty).toHaveBeenCalledTimes(2);
    unmount();
    fireEvent.keyDown(window, { key: "s", metaKey: true });
    expect(props.onSaveAllDirty).toHaveBeenCalledTimes(2);
  });

  it("reconfigures the editor theme when the theme changes", () => {
    const props = makeProps();
    render(
      <ThemeProvider>
        <LightThemeSwitcher />
        <MemoryFileViewer {...props} />
      </ThemeProvider>,
    );
    const view = props.viewRef.current as EditorView;
    const before = view.dom.className;
    fireEvent.click(screen.getByText("light"));
    expect(props.viewRef.current).toBe(view);
    expect(view.dom.className).not.toBe(before);
  });

  it("skips the theme reconfigure while no editor is mounted", () => {
    const props = makeProps({ fileContent: null });
    render(
      <ThemeProvider>
        <LightThemeSwitcher />
        <MemoryFileViewer {...props} />
      </ThemeProvider>,
    );
    fireEvent.click(screen.getByText("light"));
    expect(props.viewRef.current).toBeNull();
  });

  it("syncs preview scroll from the editor and back", () => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      frames.push(cb);
      return frames.length;
    });
    // CodeMirror queues its own measure frames, which jsdom cannot run; only
    // the frames a scroll handler queues are kept and run.
    const ours: FrameRequestCallback[] = [];
    const scroll = (el: HTMLElement) => {
      const mark = frames.length;
      fireEvent.scroll(el);
      ours.push(...frames.splice(mark));
    };
    const flush = () => act(() => ours.splice(0).forEach((cb) => cb(0)));

    const { container } = render(<MemoryFileViewer {...makeProps()} />);
    const editor = editorHost(container);

    // Editor-only mode: no preview to move.
    setScroll(editor, 50, 200, 100);
    scroll(editor);
    flush();

    fireEvent.click(screen.getByText("Split"));
    const preview = container.querySelector(".readme-content") as HTMLDivElement;
    setScroll(preview, 0, 400, 200);
    scroll(editor);
    expect(preview.scrollTop).toBe(100);
    // While the editor drives the sync, a preview scroll is ignored.
    setScroll(editor, 0, 200, 100);
    scroll(preview);
    expect(editor.scrollTop).toBe(0);
    flush();

    setScroll(preview, 150, 400, 200);
    scroll(preview);
    expect(editor.scrollTop).toBe(75);
    // While the preview drives the sync, an editor scroll is ignored.
    preview.scrollTop = 0;
    scroll(editor);
    expect(preview.scrollTop).toBe(0);
    flush();
  });
});
