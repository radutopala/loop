import { EditorView } from "@codemirror/view";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDir, fetchFileContent, saveFileContent } from "../../api/loopApi";
import { ThemeProvider, useTheme } from "../../ThemeContext";
import { type ColorPalette, darkColors } from "../../theme";
import { NotesPanel } from "./NotesPanel";

vi.mock("../../api/loopApi", () => ({
  createDir: vi.fn(),
  fetchFileContent: vi.fn(),
  saveFileContent: vi.fn(),
}));

const fetchMock = vi.mocked(fetchFileContent);
const saveMock = vi.mocked(saveFileContent);
const mkdirMock = vi.mocked(createDir);

function LightThemeSwitcher() {
  const { setThemeName } = useTheme();
  return (
    <button type="button" onClick={() => setThemeName("light")}>
      light
    </button>
  );
}

function editorHost(): HTMLDivElement {
  return screen.getByTestId("notes-panel").children[1]!.children[0] as HTMLDivElement;
}

function viewOf(): EditorView {
  return EditorView.findFromDOM(editorHost().querySelector(".cm-editor") as HTMLElement) as EditorView;
}

async function renderLoaded(content = "# Hello") {
  fetchMock.mockResolvedValue({ content } as never);
  const utils = render(<NotesPanel channelId="ch1" />);
  await waitFor(() => expect(editorHost().querySelector(".cm-editor")).not.toBeNull());
  return utils;
}

function type(text: string) {
  act(() => viewOf().dispatch({ changes: { from: viewOf().state.doc.length, insert: text } }));
}

// Sets the scroll geometry jsdom leaves at zero.
function setScroll(el: HTMLElement, top: number, height: number, client: number) {
  Object.defineProperty(el, "scrollHeight", { value: height, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: client, configurable: true });
  el.scrollTop = top;
}

describe("NotesPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    saveMock.mockResolvedValue(undefined as never);
    mkdirMock.mockResolvedValue(undefined as never);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("loads the notes into the editor and shows the preview modes", async () => {
    await renderLoaded("# Hello");
    expect(fetchMock).toHaveBeenCalledWith("ch1", ".loop/NOTES.md");
    expect(viewOf().state.doc.toString()).toBe("# Hello");
    expect(screen.getByText(".loop/NOTES.md")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Hello" })).toBeNull();

    fireEvent.click(screen.getByTitle("Editor + Preview"));
    expect(screen.getByRole("heading", { name: "Hello" })).toBeTruthy();
    expect(editorHost().style.display).toBe("");

    fireEvent.click(screen.getByTitle("Preview only"));
    expect(editorHost().style.display).toBe("none");
    expect(screen.getByRole("heading", { name: "Hello" })).toBeTruthy();

    fireEvent.click(screen.getByTitle("Editor only"));
    expect(screen.queryByRole("heading", { name: "Hello" })).toBeNull();
  });

  it("starts empty when the notes file cannot be read", async () => {
    fetchMock.mockRejectedValue(new Error("404"));
    render(<NotesPanel channelId="ch1" />);
    await waitFor(() => expect(editorHost().querySelector(".cm-editor")).not.toBeNull());
    expect(viewOf().state.doc.toString()).toBe("");
    // An empty preview renders nothing even in split mode.
    fireEvent.click(screen.getByTitle("Editor + Preview"));
    expect(document.querySelector(".readme-content")).toBeNull();
  });

  it("debounces a save and updates the preview after an edit", async () => {
    await renderLoaded("a");
    fireEvent.click(screen.getByTitle("Editor + Preview"));
    vi.useFakeTimers();
    type("b");
    expect(screen.getByText("●")).toBeTruthy();
    type("c");
    act(() => vi.advanceTimersByTime(300));
    expect(document.querySelector(".readme-content")?.textContent?.trim()).toBe("abc");
    expect(saveMock).not.toHaveBeenCalled();
    await act(async () => vi.advanceTimersByTime(1500));
    expect(saveMock).toHaveBeenCalledTimes(1);
    expect(saveMock).toHaveBeenCalledWith("ch1", ".loop/NOTES.md", "abc");
    expect(screen.getByText("Saved")).toBeTruthy();
  });

  it("shows Saving while the save is in flight", async () => {
    await renderLoaded("a");
    saveMock.mockReturnValue(new Promise(() => {}));
    vi.useFakeTimers();
    type("b");
    await act(async () => vi.advanceTimersByTime(1500));
    expect(screen.getByText("Saving...")).toBeTruthy();
  });

  it("creates the .loop directory and retries when the first save fails", async () => {
    await renderLoaded("a");
    saveMock.mockRejectedValueOnce(new Error("no dir"));
    vi.useFakeTimers();
    type("b");
    await act(async () => vi.advanceTimersByTime(1500));
    expect(mkdirMock).toHaveBeenCalledWith("ch1", ".loop");
    expect(saveMock).toHaveBeenCalledTimes(2);
    expect(screen.getByText("Saved")).toBeTruthy();
  });

  it("reports a save that fails after the retry", async () => {
    await renderLoaded("a");
    saveMock.mockRejectedValue(new Error("disk full"));
    vi.useFakeTimers();
    type("b");
    await act(async () => vi.advanceTimersByTime(1500));
    const label = screen.getByText("Save failed");
    expect(label.style.color).toBe("rgb(239, 68, 68)");
  });

  it("falls back to a default error color when the palette has none", async () => {
    const palette = { ...darkColors, error: undefined } as unknown as ColorPalette;
    fetchMock.mockResolvedValue({ content: "a" } as never);
    saveMock.mockRejectedValue(new Error("disk full"));
    mkdirMock.mockRejectedValue(new Error("denied"));
    render(
      <ThemeProvider customThemes={{ bare: palette }} initialTheme="bare">
        <NotesPanel channelId="ch1" />
      </ThemeProvider>,
    );
    await waitFor(() => expect(editorHost().querySelector(".cm-editor")).not.toBeNull());
    vi.useFakeTimers();
    type("b");
    await act(async () => vi.advanceTimersByTime(1500));
    expect(screen.getByText("Save failed").style.color).toBe("rgb(248, 113, 113)");
  });

  it("flushes a pending save on unmount", async () => {
    const { unmount } = await renderLoaded("a");
    vi.useFakeTimers();
    type("b");
    unmount();
    expect(saveMock).toHaveBeenCalledWith("ch1", ".loop/NOTES.md", "ab");
    await act(async () => vi.advanceTimersByTime(2000));
    expect(saveMock).toHaveBeenCalledTimes(1);
  });

  it("does nothing on unmount without pending edits", async () => {
    const { unmount } = await renderLoaded("a");
    unmount();
    expect(saveMock).not.toHaveBeenCalled();
  });

  it("does not build an editor when unmounted before the notes load", async () => {
    let resolve: (v: { content: string }) => void = () => {};
    fetchMock.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      }) as never,
    );
    const { unmount, container } = render(<NotesPanel channelId="ch1" />);
    unmount();
    await act(async () => resolve({ content: "late" }));
    expect(container.querySelector(".cm-editor")).toBeNull();
  });

  it("reconfigures the editor theme when the theme changes", async () => {
    fetchMock.mockResolvedValue({ content: "a" } as never);
    render(
      <ThemeProvider>
        <LightThemeSwitcher />
        <NotesPanel channelId="ch1" />
      </ThemeProvider>,
    );
    await waitFor(() => expect(editorHost().querySelector(".cm-editor")).not.toBeNull());
    const view = viewOf();
    const before = view.dom.className;
    fireEvent.click(screen.getByText("light"));
    expect(viewOf()).toBe(view);
    expect(view.dom.className).not.toBe(before);
  });

  it("syncs preview scroll from the editor and back", async () => {
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
    await renderLoaded("# Hello");
    const editor = editorHost();

    // Editor-only mode: no preview to move.
    setScroll(editor, 50, 200, 100);
    scroll(editor);
    flush();

    fireEvent.click(screen.getByTitle("Editor + Preview"));
    const preview = document.querySelector(".readme-content") as HTMLDivElement;
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
    // Once the frame has run, editor scrolls drive the preview again.
    scroll(editor);
    expect(preview.scrollTop).toBe(150);
    setScroll(editor, 100, 200, 100);
    scroll(editor);
    expect(preview.scrollTop).toBe(200);
  });
});
