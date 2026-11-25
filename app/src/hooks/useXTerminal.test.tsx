import { act, renderHook, waitFor } from "@testing-library/react";
import { Terminal } from "@xterm/xterm";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type FakeFitAddon, FakeLine, type FakeResizeObserver, FakeTerminal, FakeWebLinksAddon, fakeResizeObserverClass } from "../test/hooks-a-xterm";
import { darkColors, lightColors } from "../theme";
import { isTerminalReportReply, useXTerminal } from "./useXTerminal";

vi.mock("@xterm/xterm", async () => {
  const { FakeTerminal } = await import("../test/hooks-a-xterm");
  return {
    Terminal: vi.fn(function (this: unknown, options: Record<string, unknown>) {
      return new FakeTerminal(options);
    }),
  };
});
vi.mock("@xterm/addon-fit", async () => ({ FitAddon: (await import("../test/hooks-a-xterm")).FakeFitAddon }));
vi.mock("@xterm/addon-web-links", async () => ({ WebLinksAddon: (await import("../test/hooks-a-xterm")).FakeWebLinksAddon }));

const writeText = vi.fn<(text: string) => Promise<void>>();
Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

type Options = Parameters<typeof useXTerminal>[0];

/** Makes the next terminal the hook creates a tweaked one. */
function nextTerminal(tweak: (t: FakeTerminal) => void) {
  vi.mocked(Terminal).mockImplementationOnce(function (this: unknown, options?: object) {
    const t = new FakeTerminal(options as Record<string, unknown>);
    tweak(t);
    return t as never;
  });
}

describe("isTerminalReportReply", () => {
  it.each([
    ["a cursor position report", "\x1b[12;40R", true],
    ["an OSC color report ended by BEL", "\x1b]11;rgb:0000/0000/0000\x07", true],
    ["an OSC color report ended by ST", "\x1b]4;1;rgb:ffff/0000/0000\x1b\\", true],
    ["a mode report", "\x1b[?2026;2$y", true],
    ["a device attributes report", "\x1b[?62;22c", true],
    ["typed text", "ls -la\r", false],
    ["an arrow key", "\x1b[A", false],
    ["a report with input after it", "\x1b[1;1Rx", false],
  ])("%s", (_name, data, want) => {
    expect(isTerminalReportReply(data)).toBe(want);
  });
});

describe("useXTerminal", () => {
  let observers: FakeResizeObserver[];

  beforeEach(() => {
    writeText.mockReset().mockResolvedValue();
    vi.mocked(Terminal).mockClear();
    const fake = fakeResizeObserverClass();
    observers = fake.observers;
    vi.stubGlobal("ResizeObserver", fake.ResizeObserver);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    vi.useRealTimers();
    Reflect.deleteProperty(document, "fonts");
    document.body.innerHTML = "";
  });

  function render(opts: Partial<Options> = {}) {
    const container = document.createElement("div");
    document.body.appendChild(container);
    const containerRef = { current: container };
    const onInput = vi.fn();
    const onResize = vi.fn();
    const hook = renderHook((props: Partial<Options>) => useXTerminal({ containerRef, colors: darkColors, onInput, onResize, ...props }), { initialProps: opts });
    return { ...hook, container, onInput, onResize };
  }

  async function mount(opts: Partial<Options> = {}) {
    const r = render(opts);
    await waitFor(() => expect(r.result.current.xtermRef.current).not.toBeNull());
    const term = r.result.current.xtermRef.current as unknown as FakeTerminal;
    const fit = term.addons[0] as FakeFitAddon;
    return { ...r, term, fit };
  }

  describe("setup", () => {
    it("opens a terminal in the container, fitted, with the theme and font", async () => {
      const { term, fit, container, onResize } = await mount();
      expect(term.options).toMatchObject({ cursorBlink: true, fontSize: 13, theme: { background: darkColors.sidebar, cursorAccent: "#000000", selectionForeground: undefined } });
      expect((term.options.theme as Record<string, unknown>).black).toBeUndefined();
      expect(container.querySelector(".xterm")).not.toBeNull();
      expect(fit.fits).toBe(1);
      expect(onResize).toHaveBeenCalledWith(80, 24);
      expect(observers[0]!.observed).toEqual([container]);
      expect([...document.head.querySelectorAll("style")].some((s) => s.textContent?.includes(".xterm-viewport"))).toBe(true);
    });

    it("uses a readable palette and font size on a light theme", async () => {
      const { term } = await mount({ colors: lightColors, fontSize: 15 });
      expect(term.options).toMatchObject({ fontSize: 15, theme: { cursorAccent: "#ffffff", selectionForeground: "#000000", black: "#000000", brightWhite: "#333333" } });
    });

    it("pins the scrollbar width", async () => {
      const { term } = await mount();
      const viewport = term._core!.viewport as { scrollBarWidth: number };
      expect(viewport.scrollBarWidth).toBe(8);
      viewport.scrollBarWidth = 15;
      expect(viewport.scrollBarWidth).toBe(8);
    });

    it("works with a terminal that has no internals to reach", async () => {
      nextTerminal((t) => {
        t._core = undefined;
      });
      const { term, fit } = await mount();
      expect(fit.fits).toBe(1);
      expect(term._core).toBeUndefined();
    });

    it("refits once the fonts have loaded", async () => {
      let ready!: () => void;
      Object.defineProperty(document, "fonts", { value: { ready: new Promise<void>((r) => (ready = r)) }, configurable: true });
      const { term, fit } = await mount();
      await act(async () => ready());
      expect(fit.fits).toBe(2);
      expect(term.refreshed).toEqual([[0, 23]]);
    });

    it("doesn't refit after unmount when the fonts load late", async () => {
      let ready!: () => void;
      Object.defineProperty(document, "fonts", { value: { ready: new Promise<void>((r) => (ready = r)) }, configurable: true });
      const { term, fit, unmount } = await mount();
      unmount();
      await act(async () => ready());
      expect(fit.fits).toBe(1);
      expect(term.refreshed).toEqual([]);
      expect(term.disposed).toBe(true);
    });

    it("does nothing without a container", async () => {
      const onInput = vi.fn();
      const { result } = renderHook(() => useXTerminal({ containerRef: { current: null }, colors: darkColors, onInput, onResize: vi.fn() }));
      await act(async () => {});
      expect(result.current.xtermRef.current).toBeNull();
      expect(Terminal).not.toHaveBeenCalled();
    });

    it("creates nothing when unmounted before xterm loads", async () => {
      const { unmount, result } = render();
      unmount();
      await act(async () => {});
      expect(Terminal).not.toHaveBeenCalled();
      expect(result.current.xtermRef.current).toBeNull();
    });
  });

  describe("writing", () => {
    it("queues writes until the terminal is ready, then flushes them", async () => {
      const r = render();
      r.result.current.write("early");
      r.result.current.write(new Uint8Array([65]));
      await waitFor(() => expect(r.result.current.xtermRef.current).not.toBeNull());
      const term = r.result.current.xtermRef.current as unknown as FakeTerminal;
      expect(term.written).toEqual(["early", new Uint8Array([65])]);
      expect(term.scrolledToBottom).toBe(1);
    });

    it("writes and scrolls to the bottom once ready", async () => {
      const { term, result } = await mount();
      expect(term.scrolledToBottom).toBe(0);
      result.current.write("out");
      expect(term.written).toEqual(["out"]);
      expect(term.scrolledToBottom).toBe(1);
    });

    it("clears and resets the terminal, and tolerates no terminal", async () => {
      const r = render();
      r.result.current.clear();
      await waitFor(() => expect(r.result.current.xtermRef.current).not.toBeNull());
      const term = r.result.current.xtermRef.current as unknown as FakeTerminal;
      r.result.current.clear();
      expect(term.cleared).toBe(1);
      expect(term.resets).toBe(1);
    });
  });

  describe("input", () => {
    it("sends Shift+Enter as a backslash and CR, on keydown only", async () => {
      const { term, onInput } = await mount();
      expect(term.keyHandler!({ key: "Enter", shiftKey: true, type: "keydown" } as KeyboardEvent)).toBe(false);
      expect(onInput).toHaveBeenCalledWith("\\\r");
      expect(term.keyHandler!({ key: "Enter", shiftKey: true, type: "keypress" } as KeyboardEvent)).toBe(false);
      expect(onInput).toHaveBeenCalledTimes(1);
      expect(term.keyHandler!({ key: "Enter", shiftKey: false, type: "keydown" } as KeyboardEvent)).toBe(true);
      expect(term.keyHandler!({ key: "a", shiftKey: true, type: "keydown" } as KeyboardEvent)).toBe(true);
      expect(onInput).toHaveBeenCalledTimes(1);
    });

    it("forwards typed data but drops xterm's report replies", async () => {
      const { term, onInput } = await mount();
      term.dataHandler!("\x1b[5;5R");
      expect(onInput).not.toHaveBeenCalled();
      term.dataHandler!("ls\r");
      expect(onInput).toHaveBeenCalledWith("ls\r");
    });

    it("reports terminal resizes", async () => {
      const { term, onResize } = await mount();
      term.resizeHandler!({ cols: 120, rows: 40 });
      expect(onResize).toHaveBeenLastCalledWith(120, 40);
    });
  });

  describe("links", () => {
    function links(term: FakeTerminal) {
      return term.addons.find((a) => a instanceof FakeWebLinksAddon) as FakeWebLinksAddon;
    }

    it("opens a link through the desktop bridge", async () => {
      const openExternal = vi.fn(async () => {});
      vi.stubGlobal("loopAPI", { openExternal });
      const open = vi.spyOn(window, "open").mockImplementation(() => null);
      const { term } = await mount();
      links(term).onClick(new MouseEvent("click"), "https://example.test/");
      expect(openExternal).toHaveBeenCalledWith("https://example.test/");
      expect(open).not.toHaveBeenCalled();
    });

    it("falls back to a new window in a plain browser", async () => {
      const open = vi.spyOn(window, "open").mockImplementation(() => null);
      const { term } = await mount();
      links(term).onClick(new MouseEvent("click"), "https://example.test/");
      expect(open).toHaveBeenCalledWith("https://example.test/", "_blank", "noopener,noreferrer");
    });

    it("leaves a click something else handled", async () => {
      const open = vi.spyOn(window, "open").mockImplementation(() => null);
      const { term } = await mount();
      const e = new MouseEvent("click", { cancelable: true });
      e.preventDefault();
      links(term).onClick(e, "https://example.test/");
      expect(open).not.toHaveBeenCalled();
    });
  });

  describe("image paste", () => {
    function paste(target: Element, items: object[] | null) {
      const e = new Event("paste", { bubbles: true, cancelable: true });
      Object.defineProperty(e, "clipboardData", { value: items ? { items } : null });
      target.dispatchEvent(e);
      return e;
    }
    const image = new File(["x"], "shot.png", { type: "image/png" });
    const imageItem = { kind: "file", type: "image/png", getAsFile: () => image };

    it("hands a pasted image to the latest handler and keeps it from xterm", async () => {
      const first = vi.fn();
      const second = vi.fn();
      const { container, rerender } = await mount({ onPasteImage: first });
      rerender({ onPasteImage: second });
      const afterXterm = vi.fn();
      document.addEventListener("paste", afterXterm);
      const e = paste(container.querySelector(".xterm")!, [imageItem]);
      document.removeEventListener("paste", afterXterm);
      expect(e.defaultPrevented).toBe(true);
      expect(afterXterm).not.toHaveBeenCalled();
      expect(first).not.toHaveBeenCalled();
      expect(second).toHaveBeenCalledWith(image);
    });

    it("leaves a text paste to xterm", async () => {
      const onPasteImage = vi.fn();
      const { container } = await mount({ onPasteImage });
      const e = paste(container, [{ kind: "string", type: "text/plain", getAsFile: () => null }]);
      expect(e.defaultPrevented).toBe(false);
      expect(onPasteImage).not.toHaveBeenCalled();
    });

    it("leaves every paste to xterm without a handler", async () => {
      const { container } = await mount();
      expect(paste(container, [imageItem]).defaultPrevented).toBe(false);
    });

    it("warns when the upload fails", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const err = new Error("upload failed");
      const { container } = await mount({ onPasteImage: () => Promise.reject(err) });
      paste(container, [imageItem]);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[terminal] image paste failed:", err));
    });
  });

  describe("selection", () => {
    // Cells are 10x20 px and the screen sits at the viewport's origin.
    function mouse(target: EventTarget, type: string, col: number, row: number, button = 0) {
      target.dispatchEvent(new MouseEvent(type, { bubbles: true, button, clientX: col * 10 + 5, clientY: row * 20 + 5 }));
    }
    function drag(container: Element, from: [number, number], to: [number, number]) {
      mouse(container, "mousedown", ...from);
      mouse(document, "mouseup", ...to);
    }
    const getSelection = (container: Element) => (container.querySelector(".xterm") as Element & { _xtermGetSelection: () => string })._xtermGetSelection();

    it("copies a drag across rows, read from the buffer", async () => {
      const { term, container } = await mount();
      term.lines = [new FakeLine("first line    "), new FakeLine("middle  "), new FakeLine("last line  ")];
      drag(container, [6, 0], [3, 2]);
      expect(writeText).toHaveBeenCalledWith("line\nmiddle\nlast");
      expect(getSelection(container)).toBe("line\nmiddle\nlast");
    });

    it("reads a backwards drag on one row in order, from the scrolled viewport", async () => {
      const { term, container } = await mount();
      term.buffer.active.viewportY = 1;
      term.lines = [new FakeLine("hidden"), new FakeLine("abcdefgh")];
      drag(container, [5, 0], [2, 0]);
      expect(writeText).toHaveBeenCalledWith("cdef");
    });

    it("reads a drag upwards and skips rows the buffer doesn't have", async () => {
      const { term, container } = await mount();
      term.lines = [new FakeLine("top row"), undefined, new FakeLine("bottom")];
      drag(container, [1, 2], [4, 0]);
      expect(writeText).toHaveBeenCalledWith("row\nbo");
    });

    it("clamps a drag past the grid to its edges", async () => {
      const { term, container } = await mount();
      term.cols = 4;
      term.rows = 1;
      term.lines = [new FakeLine("abcdef")];
      mouse(container, "mousedown", -3, -3);
      mouse(document, "mouseup", 50, 50);
      expect(writeText).toHaveBeenCalledWith("abcd");
    });

    it("prefers xterm's own selection", async () => {
      const { term, container } = await mount();
      term.selection = "xterm's";
      term.lines = [new FakeLine("abcdef")];
      drag(container, [0, 0], [3, 0]);
      expect(writeText).toHaveBeenCalledWith("xterm's");
      expect(getSelection(container)).toBe("xterm's");
    });

    it("copies nothing on a plain click", async () => {
      const { term, container } = await mount();
      term.lines = [new FakeLine("abcdef")];
      drag(container, [2, 0], [2, 0]);
      expect(writeText).not.toHaveBeenCalled();
      expect(getSelection(container)).toBe("");
    });

    it("ignores buttons other than the primary", async () => {
      const { term, container } = await mount();
      term.lines = [new FakeLine("abcdef")];
      mouse(container, "mousedown", 0, 0, 2);
      mouse(document, "mouseup", 3, 0, 2);
      expect(writeText).not.toHaveBeenCalled();
      // A primary release without a primary press selects nothing.
      mouse(document, "mouseup", 3, 0);
      expect(writeText).not.toHaveBeenCalled();
    });

    it("drops a drag when the cell size is unknown at either end", async () => {
      const { term, container } = await mount();
      term.lines = [new FakeLine("abcdef")];
      mouse(container, "mousedown", 0, 0);
      term._core!._charSizeService!.width = 0;
      mouse(document, "mouseup", 3, 0);
      expect(writeText).not.toHaveBeenCalled();
      mouse(container, "mousedown", 0, 0);
      term._core!._charSizeService!.width = 10;
      mouse(document, "mouseup", 3, 0);
      expect(writeText).not.toHaveBeenCalled();
    });

    it.each<[string, (t: FakeTerminal) => void]>([
      [
        "no internals",
        (t) => {
          t._core = undefined;
        },
      ],
      [
        "no cell size service",
        (t) => {
          t._core = {};
        },
      ],
    ])("selects nothing by drag with %s", async (_name, tweak) => {
      nextTerminal(tweak);
      const { term, container } = await mount();
      term.lines = [new FakeLine("abcdef")];
      drag(container, [0, 0], [3, 0]);
      expect(writeText).not.toHaveBeenCalled();
    });

    it("measures against the container when xterm renders no screen element", async () => {
      nextTerminal((t) => {
        t.renderElements = false;
      });
      const { term, container, unmount } = await mount();
      vi.spyOn(container, "getBoundingClientRect").mockReturnValue({ left: 100, top: 0 } as DOMRect);
      term.lines = [new FakeLine("abcdef")];
      mouse(container, "mousedown", 10, 0);
      mouse(document, "mouseup", 13, 0);
      expect(writeText).toHaveBeenCalledWith("abcd");
      unmount();
      await act(async () => {});
    });

    it("logs a failed copy", async () => {
      const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
      const err = new Error("denied");
      writeText.mockRejectedValue(err);
      const { term, container } = await mount();
      term.selection = "x";
      drag(container, [0, 0], [0, 0]);
      await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] copying terminal selection:", err));
    });
  });

  describe("container resizes", () => {
    it("fits once the size settles, then scrolls to the bottom", async () => {
      const { term, fit } = await mount();
      vi.useFakeTimers();
      const ro = observers[0]!;
      ro.resize({ width: 300, height: 100 });
      act(() => vi.advanceTimersByTime(20));
      ro.resize({ width: 600, height: 200 });
      act(() => vi.advanceTimersByTime(49));
      expect(fit.fits).toBe(1);
      act(() => vi.advanceTimersByTime(1));
      expect(fit.fits).toBe(2);
      const before = term.scrolledToBottom;
      act(() => vi.advanceTimersByTime(20));
      expect(term.scrolledToBottom).toBe(before + 1);
    });

    it("skips a hidden container and an empty report", async () => {
      const { fit } = await mount();
      vi.useFakeTimers();
      const ro = observers[0]!;
      ro.resize(null);
      ro.resize({ width: 0, height: 100 });
      ro.resize({ width: 100, height: 0 });
      act(() => vi.advanceTimersByTime(100));
      expect(fit.fits).toBe(1);
    });
  });

  describe("teardown", () => {
    it("disposes the terminal and removes every listener", async () => {
      const onPasteImage = vi.fn();
      const { term, container, fit, unmount } = await mount({ onPasteImage });
      term.lines = [new FakeLine("abcdef")];
      const xtermEl = container.querySelector(".xterm") as Element & { _xtermGetSelection?: () => string };
      vi.useFakeTimers();
      observers[0]!.resize({ width: 100, height: 100 });
      unmount();
      await act(async () => {
        await Promise.resolve();
      });
      expect(term.disposed).toBe(true);
      expect(observers[0]!.disconnected).toBe(true);
      expect(xtermEl._xtermGetSelection).toBeUndefined();
      act(() => vi.advanceTimersByTime(100));
      expect(fit.fits).toBe(1);
      drag(container, [0, 0], [3, 0]);
      term.selection = "x";
      document.dispatchEvent(new MouseEvent("mouseup", { button: 0 }));
      expect(writeText).not.toHaveBeenCalled();
      const e = new Event("paste", { bubbles: true, cancelable: true });
      Object.defineProperty(e, "clipboardData", { value: { items: [{ kind: "file", type: "image/png", getAsFile: () => new File([], "a.png") }] } });
      container.dispatchEvent(e);
      expect(onPasteImage).not.toHaveBeenCalled();
    });

    function drag(container: Element, from: [number, number], to: [number, number]) {
      container.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0, clientX: from[0] * 10, clientY: from[1] * 20 }));
      document.dispatchEvent(new MouseEvent("mouseup", { bubbles: true, button: 0, clientX: to[0] * 10, clientY: to[1] * 20 }));
    }
  });

  describe("runtime options", () => {
    it("switches the theme in place and colors black backgrounds on a light theme", async () => {
      const { term, rerender } = await mount();
      const stylesBefore = document.head.querySelectorAll("style").length;
      rerender({ colors: lightColors });
      expect((term.options.theme as Record<string, unknown>).black).toBe("#000000");
      const styles = document.head.querySelectorAll("style");
      expect(styles).toHaveLength(stylesBefore + 1);
      const style = styles[styles.length - 1]!;
      expect(style.textContent).toBe(`.xterm-bg-0 { background-color: ${lightColors.sidebar} !important; }`);
      rerender({ colors: darkColors });
      expect(style.textContent).toBe("");
      expect((term.options.theme as Record<string, unknown>).black).toBeUndefined();
      rerender({ colors: { ...lightColors, sidebar: "#eeeeee" } });
      expect(document.head.querySelectorAll("style")).toHaveLength(stylesBefore + 1);
      expect(style.textContent).toContain("#eeeeee");
    });

    it("changes the font size in place and refits", async () => {
      const { term, fit, rerender } = await mount({ fontSize: 12 });
      rerender({ fontSize: 16 });
      expect(term.options.fontSize).toBe(16);
      expect(fit.fits).toBe(2);
      rerender({ fontSize: undefined });
      expect(term.options.fontSize).toBe(16);
      expect(fit.fits).toBe(2);
    });
  });
});
