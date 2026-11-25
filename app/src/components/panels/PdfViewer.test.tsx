import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../../ThemeContext";
import PdfViewer from "./PdfViewer";

// pdf.js needs a worker and a real canvas; the stub hands out fake documents
// whose pages report a viewport, draw through a controllable render task and
// fill a text layer.
const pdf = vi.hoisted(() => {
  class PasswordException extends Error {}
  class RenderingCancelledException extends Error {}
  const textLayers: { render: ReturnType<typeof vi.fn>; cancel: ReturnType<typeof vi.fn>; container: HTMLElement }[] = [];
  class TextLayer {
    render: ReturnType<typeof vi.fn>;
    cancel = vi.fn();
    container: HTMLElement;
    constructor({ container }: { container: HTMLElement }) {
      this.container = container;
      this.render = vi.fn(() => pdf.textRender());
      textLayers.push(this);
    }
  }
  return {
    PasswordException,
    RenderingCancelledException,
    TextLayer,
    textLayers,
    textRender: (): Promise<void> => Promise.resolve(),
    getDocument: vi.fn(),
  };
});

vi.mock("pdfjs-dist", () => ({
  GlobalWorkerOptions: {},
  PixelsPerInch: { PDF_TO_CSS_UNITS: 1 },
  getDocument: pdf.getDocument,
  PasswordException: pdf.PasswordException,
  RenderingCancelledException: pdf.RenderingCancelledException,
  TextLayer: pdf.TextLayer,
}));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "pdf.worker.js" }));

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (v: T) => void;
  reject: (e: unknown) => void;
}
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// A page 200x100 at scale 1 (CSS units are 1 in the stub).
function fakePage(opts: { render?: () => Promise<void>; width?: number } = {}) {
  const width = opts.width ?? 200;
  const tasks: { cancel: ReturnType<typeof vi.fn> }[] = [];
  const page = {
    getViewport: ({ scale }: { scale: number }) => ({ width: width * scale, height: 100 * scale, userUnit: 1 }),
    render: vi.fn(() => {
      const task = { promise: opts.render ? opts.render() : Promise.resolve(), cancel: vi.fn() };
      tasks.push(task);
      return task;
    }),
    streamTextContent: vi.fn(() => "text-stream"),
    tasks,
  };
  return page;
}

function fakeDoc(numPages: number, getPage?: (n: number) => Promise<unknown>) {
  const pages = Array.from({ length: numPages }, () => fakePage());
  return {
    numPages,
    pages,
    getPage: vi.fn((n: number) => (getPage ? getPage(n) : Promise.resolve(pages[n - 1]))),
    loadingTask: { destroy: vi.fn(() => Promise.resolve()) },
  };
}

function loadingTask(promise: Promise<unknown>) {
  return { promise, destroy: vi.fn(() => Promise.resolve()) };
}

// IntersectionObserver / ResizeObserver stand-ins that record their callbacks
// so a test can report pages entering view and the pane resizing.
let observers: { cb: (entries: { isIntersecting: boolean }[]) => void; el?: Element; disconnect: ReturnType<typeof vi.fn<() => void>> }[];
let resizers: { cb: () => void; disconnect: ReturnType<typeof vi.fn<() => void>> }[];

function showPages(visible = true) {
  act(() => {
    for (const o of observers) o.cb([{ isIntersecting: visible }]);
  });
}

const renderViewer = (url = "/f.pdf") =>
  render(
    <ThemeProvider initialTheme="dark">
      <PdfViewer url={url} />
    </ThemeProvider>,
  );

// Waits for the document, then lets the effects it triggers (fit-width) settle.
async function loaded(indicator: string) {
  await screen.findByText(indicator);
  await act(async () => {});
}

const zoomLabel = () => screen.getByText(/^\d+%$/).textContent;
const scroller = () => screen.getByTestId("pdf-toolbar").parentElement!.lastElementChild as HTMLDivElement;
const pages = () => Array.from(document.querySelectorAll<HTMLDivElement>("[data-pdf-page]"));

describe("PdfViewer", () => {
  let warn: ReturnType<typeof vi.spyOn>;
  let drawImage: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    observers = [];
    resizers = [];
    pdf.textLayers.length = 0;
    pdf.textRender = () => Promise.resolve();
    pdf.getDocument.mockReset();
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        entry: (typeof observers)[number];
        constructor(cb: (entries: { isIntersecting: boolean }[]) => void) {
          this.entry = { cb, disconnect: vi.fn() };
          observers.push(this.entry);
        }
        observe(el: Element) {
          this.entry.el = el;
        }
        disconnect() {
          this.entry.disconnect();
        }
      },
    );
    vi.stubGlobal(
      "ResizeObserver",
      class {
        entry: (typeof resizers)[number];
        constructor(cb: () => void) {
          this.entry = { cb, disconnect: vi.fn() };
          resizers.push(this.entry);
        }
        observe() {}
        disconnect() {
          this.entry.disconnect();
        }
      },
    );
    vi.spyOn(HTMLDivElement.prototype, "clientWidth", "get").mockReturnValue(432);
    drawImage = vi.fn();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({ drawImage } as unknown as CanvasRenderingContext2D);
    warn = vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("loads the whole file, fits page 1's width and lays out every page", async () => {
    const doc = fakeDoc(3);
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    expect(screen.getByText("Loading PDF...")).toBeTruthy();
    expect(pdf.getDocument).toHaveBeenCalledWith({ url: "/f.pdf", disableRange: true });
    await loaded("Page 1 / 3");
    expect(screen.queryByText("Loading PDF...")).toBeNull();
    // (432 - 2 * 16) / 200 = 2
    expect(zoomLabel()).toBe("200%");
    expect(pages().map((p) => [p.style.width, p.style.height])).toEqual([
      ["400px", "200px"],
      ["400px", "200px"],
      ["400px", "200px"],
    ]);
    expect(screen.getByTitle("Fit page width").style.background).not.toBe("none");
  });

  it("draws a page and its text layer once it comes into view, and frees it when it leaves", async () => {
    const doc = fakeDoc(1);
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    await loaded("Page 1 / 1");
    vi.spyOn(window, "devicePixelRatio", "get").mockReturnValue(1);
    showPages();
    await act(async () => {});
    const page = doc.pages[0]!;
    expect(page.render).toHaveBeenCalledWith(expect.objectContaining({ transform: undefined }));
    expect(drawImage).toHaveBeenCalledTimes(1);
    const canvas = pages()[0]!.querySelector("canvas")!;
    expect([canvas.width, canvas.height]).toEqual([400, 200]);
    expect(pdf.textLayers[0]!.container.className).toBe("textLayer");
    const layer = pages()[0]!.querySelector(".textLayer")!;
    expect(layer.lastElementChild?.className).toBe("endOfContent");

    // Dragging a selection marks the layer until the mouse comes up.
    fireEvent.mouseDown(layer);
    expect(layer.classList.contains("selecting")).toBe(true);
    fireEvent.mouseUp(window);
    expect(layer.classList.contains("selecting")).toBe(false);

    showPages(false);
    expect([canvas.width, canvas.height]).toEqual([0, 0]);
    expect(pages()[0]!.querySelector(".textLayer")).toBeNull();
    // Selection events with no layer do nothing.
    fireEvent.mouseDown(pages()[0]!.lastElementChild!);
    fireEvent.mouseUp(window);
  });

  it("draws at the device pixel ratio, and at 1 when the ratio is unknown", async () => {
    const doc = fakeDoc(1);
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    const dpr = vi.spyOn(window, "devicePixelRatio", "get").mockReturnValue(2);
    renderViewer();
    await loaded("Page 1 / 1");
    showPages();
    await act(async () => {});
    const page = doc.pages[0]!;
    expect(page.render).toHaveBeenLastCalledWith(expect.objectContaining({ transform: [2, 0, 0, 2, 0, 0] }));
    dpr.mockReturnValue(0);
    fireEvent.click(screen.getByTitle("Zoom in"));
    await act(async () => {});
    expect(page.render).toHaveBeenLastCalledWith(expect.objectContaining({ transform: undefined }));
  });

  it("skips drawing when the canvas has no 2D context", async () => {
    vi.mocked(HTMLCanvasElement.prototype.getContext).mockReturnValue(null);
    const doc = fakeDoc(1);
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    await loaded("Page 1 / 1");
    showPages();
    await act(async () => {});
    expect(drawImage).not.toHaveBeenCalled();
    expect(pages()[0]!.querySelector(".textLayer")).not.toBeNull();
  });

  it("uses each page's own size once it's drawn", async () => {
    const doc = fakeDoc(2);
    doc.pages[1] = fakePage({ width: 100 });
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    await loaded("Page 1 / 2");
    showPages();
    await act(async () => {});
    expect(pages().map((p) => p.style.width)).toEqual(["400px", "200px"]);
  });

  it("zooms with the buttons, steps off fit-width and fits again", async () => {
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(fakeDoc(1))));
    renderViewer();
    await loaded("Page 1 / 1");
    const el = scroller();
    el.scrollTop = 100;
    el.scrollLeft = 10;
    fireEvent.click(screen.getByTitle("Zoom in"));
    expect(zoomLabel()).toBe("300%");
    // The same spot stays on screen.
    expect(el.scrollTop).toBe(150);
    expect(el.scrollLeft).toBe(15);
    expect(screen.getByTitle("Fit page width").style.background).toBe("none");
    // Resizing no longer re-fits.
    expect(resizers[0]!.disconnect).toHaveBeenCalled();
    fireEvent.click(screen.getByTitle("Zoom out"));
    fireEvent.click(screen.getByTitle("Zoom out"));
    expect(zoomLabel()).toBe("150%");
    fireEvent.click(screen.getByTitle("Fit page width"));
    expect(zoomLabel()).toBe("200%");
  });

  it("re-fits when the pane resizes", async () => {
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(fakeDoc(1))));
    renderViewer();
    await loaded("Page 1 / 1");
    vi.mocked(Object.getOwnPropertyDescriptor(HTMLDivElement.prototype, "clientWidth")!.get!).mockReturnValue(232);
    act(() => resizers.at(-1)!.cb());
    expect(zoomLabel()).toBe("100%");
  });

  it("zooms with Ctrl/Cmd + wheel and leaves a plain wheel to scroll", async () => {
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(fakeDoc(1))));
    renderViewer();
    await loaded("Page 1 / 1");
    const el = scroller();
    const plain = new WheelEvent("wheel", { deltaY: -300, cancelable: true });
    act(() => {
      el.dispatchEvent(plain);
    });
    expect(plain.defaultPrevented).toBe(false);
    expect(zoomLabel()).toBe("200%");
    const ctrl = new WheelEvent("wheel", { deltaY: 300 * Math.log(2), ctrlKey: true, cancelable: true });
    act(() => {
      el.dispatchEvent(ctrl);
    });
    expect(ctrl.defaultPrevented).toBe(true);
    expect(zoomLabel()).toBe("100%");
    act(() => {
      el.dispatchEvent(new WheelEvent("wheel", { deltaY: -300 * Math.log(2), metaKey: true, cancelable: true }));
    });
    expect(zoomLabel()).toBe("200%");
  });

  it("shows the page at the top of the view as it scrolls", async () => {
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(fakeDoc(3))));
    renderViewer();
    await loaded("Page 1 / 3");
    pages().forEach((p, i) => {
      Object.defineProperty(p, "offsetTop", { value: i * 1000 });
    });
    vi.spyOn(HTMLDivElement.prototype, "clientHeight", "get").mockReturnValue(300);
    const el = scroller();
    el.scrollTop = 1500;
    fireEvent.scroll(el);
    expect(screen.getByTestId("pdf-page-indicator").textContent).toBe("Page 2 / 3");
  });

  it.each([
    ["a password-protected file", new pdf.PasswordException("pw"), "Password-protected PDFs are not supported"],
    ["a broken file", new Error("bad header"), "Failed to load PDF: bad header"],
    ["a non-Error failure", "gone", "Failed to load PDF: gone"],
  ])("explains why %s didn't load", async (_name, err, message) => {
    pdf.getDocument.mockReturnValue(loadingTask(Promise.reject(err)));
    renderViewer();
    expect(await screen.findByText(message)).toBeTruthy();
    expect(screen.queryByText("Loading PDF...")).toBeNull();
    expect(screen.getByTestId("pdf-page-indicator").textContent).toBe("");
  });

  it("keeps the old document on screen while a new URL loads, then frees it", async () => {
    const first = fakeDoc(1);
    const second = fakeDoc(2);
    const next = deferred<unknown>();
    pdf.getDocument.mockReturnValueOnce(loadingTask(Promise.resolve(first))).mockReturnValueOnce(loadingTask(next.promise));
    const { rerender } = renderViewer("/a.pdf");
    await loaded("Page 1 / 1");
    rerender(
      <ThemeProvider initialTheme="dark">
        <PdfViewer url="/a.pdf?v=2" />
      </ThemeProvider>,
    );
    expect(screen.getByText("Page 1 / 1")).toBeTruthy();
    expect(first.loadingTask.destroy).not.toHaveBeenCalled();
    await act(async () => next.resolve(second));
    expect(screen.getByText("Page 1 / 2")).toBeTruthy();
    expect(first.loadingTask.destroy).toHaveBeenCalledTimes(1);
  });

  it("cancels a load that is overtaken by a new URL, whatever it ends with", async () => {
    const slowPage = deferred<unknown>();
    const stale = fakeDoc(1, () => slowPage.promise);
    const failing = deferred<unknown>();
    const staleTask = loadingTask(Promise.resolve(stale));
    const failingTask = loadingTask(failing.promise);
    failingTask.destroy.mockImplementation(() => Promise.reject(new Error("destroy failed")));
    pdf.getDocument
      .mockReturnValueOnce(staleTask)
      .mockReturnValueOnce(failingTask)
      .mockReturnValueOnce(loadingTask(Promise.resolve(fakeDoc(4))));
    const view = renderViewer("/1.pdf");
    const rerenderUrl = (url: string) =>
      view.rerender(
        <ThemeProvider initialTheme="dark">
          <PdfViewer url={url} />
        </ThemeProvider>,
      );
    await act(async () => {});
    rerenderUrl("/2.pdf");
    expect(staleTask.destroy).toHaveBeenCalledTimes(1);
    rerenderUrl("/3.pdf");
    await act(async () => {});
    expect(warn).toHaveBeenCalledWith("[loop] pdf load cancel:", expect.objectContaining({ message: "destroy failed" }));
    await act(async () => {
      slowPage.resolve(fakePage());
      failing.reject(new Error("late"));
    });
    expect(await screen.findByText("Page 1 / 4")).toBeTruthy();
    expect(screen.queryByText(/Failed to load PDF/)).toBeNull();
  });

  it("frees the document on unmount and logs a failed free", async () => {
    const doc = fakeDoc(1);
    doc.loadingTask.destroy.mockImplementation(() => Promise.reject(new Error("nope")));
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    const { unmount } = renderViewer();
    await loaded("Page 1 / 1");
    unmount();
    await act(async () => {});
    expect(doc.loadingTask.destroy).toHaveBeenCalledTimes(1);
    expect(warn).toHaveBeenCalledWith("[loop] pdf destroy:", expect.objectContaining({ message: "nope" }));
  });

  it("cancels a page's render and text layer when it scrolls away mid-draw", async () => {
    const draw = deferred<void>();
    const doc = fakeDoc(1);
    doc.pages[0] = fakePage({ render: () => draw.promise });
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    await loaded("Page 1 / 1");
    showPages();
    await act(async () => {});
    showPages(false);
    expect(doc.pages[0]!.tasks[0]!.cancel).toHaveBeenCalledTimes(1);
    await act(async () => draw.resolve());
    expect(drawImage).not.toHaveBeenCalled();
    expect(pdf.textLayers).toHaveLength(0);
  });

  it("drops a page fetched after it scrolled away", async () => {
    const fetched = deferred<unknown>();
    let calls = 0;
    // The first fetch sizes the document; the page's own fetch is held.
    const doc = fakeDoc(1, () => (++calls === 1 ? Promise.resolve(fakePage()) : fetched.promise));
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    await loaded("Page 1 / 1");
    showPages();
    showPages(false);
    const page = fakePage();
    await act(async () => fetched.resolve(page));
    expect(page.render).not.toHaveBeenCalled();
  });

  it("drops a text layer that finishes after the page scrolled away", async () => {
    const text = deferred<void>();
    pdf.textRender = () => text.promise;
    const doc = fakeDoc(1);
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    await loaded("Page 1 / 1");
    showPages();
    await act(async () => {});
    expect(pdf.textLayers).toHaveLength(1);
    showPages(false);
    expect(pdf.textLayers[0]!.cancel).toHaveBeenCalledTimes(1);
    await act(async () => text.resolve());
    expect(pdf.textLayers[0]!.container.querySelector(".endOfContent")).toBeNull();
  });

  it("ignores a cancelled render, a failure after scrolling away, and logs other render failures", async () => {
    const doc = fakeDoc(1);
    let fail: unknown = new pdf.RenderingCancelledException("cancelled");
    doc.pages[0] = fakePage({ render: () => Promise.reject(fail) });
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(doc)));
    renderViewer();
    await loaded("Page 1 / 1");
    showPages();
    await act(async () => {});
    expect(warn).not.toHaveBeenCalled();

    const late = deferred<void>();
    doc.pages[0].render.mockImplementation(() => ({ promise: late.promise, cancel: vi.fn() }) as never);
    fireEvent.click(screen.getByTitle("Zoom in"));
    await act(async () => {});
    showPages(false);
    await act(async () => late.reject(new Error("after")));
    expect(warn).not.toHaveBeenCalled();

    fail = new Error("draw failed");
    doc.pages[0].render.mockImplementation(() => ({ promise: Promise.reject(fail), cancel: vi.fn() }) as never);
    showPages();
    await act(async () => {});
    expect(warn).toHaveBeenCalledWith("[loop] pdf render page 1:", fail);
  });

  it("stops observing pages on unmount", async () => {
    pdf.getDocument.mockReturnValue(loadingTask(Promise.resolve(fakeDoc(1))));
    const { unmount } = renderViewer();
    await loaded("Page 1 / 1");
    unmount();
    expect(observers[0]!.disconnect).toHaveBeenCalled();
    expect(resizers.every((r) => r.disconnect.mock.calls.length > 0)).toBe(true);
  });
});
