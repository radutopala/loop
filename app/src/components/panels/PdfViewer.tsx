// PDF viewer for editor tabs, rendered with pdf.js. CodeEditor loads it with
// React.lazy so pdf.js (and its worker) stay out of the main bundle until the
// first PDF tab opens.
import "./PdfViewer.css";
import { GlobalWorkerOptions, getDocument, PasswordException, type PDFDocumentProxy, PixelsPerInch, RenderingCancelledException, type RenderTask, TextLayer } from "pdfjs-dist";
import workerUrl from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { type CSSProperties, type RefObject, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTheme } from "../../ThemeContext";
import { logErr } from "../../utils/log";
import { canvasOutputScale, clampZoom, currentPage, fitWidthZoom, stepZoom } from "./pdfZoom";

GlobalWorkerOptions.workerSrc = workerUrl;

// Space around the pages; fit-width leaves this much on each side.
const GUTTER = 16;
const PAGE_GAP = 12;
const CSS_UNITS = PixelsPerInch.PDF_TO_CSS_UNITS;

interface PageSize {
  width: number;
  height: number;
}

export default function PdfViewer({ url }: { url: string }) {
  const { colors, fontSizes } = useTheme();
  const scrollRef = useRef<HTMLDivElement>(null);
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null);
  // Page 1's CSS size at 100%: the placeholder size for pages not yet rendered
  // and the reference width for fit-width.
  const [baseSize, setBaseSize] = useState<PageSize | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [fit, setFit] = useState(true);
  const [zoom, setZoom] = useState(1);
  const [page, setPage] = useState(1);

  // (Re)load whenever the URL changes — the editor bumps its cache-buster when
  // the file is rewritten. The old document stays on screen until the new one
  // is ready, so a refresh doesn't flash an empty pane.
  useEffect(() => {
    let cancelled = false;
    let owned = false;
    // The whole file is fetched in one streamed GET: the daemon is local, and
    // pdf.js's Range requests would need CORS headers the API doesn't send.
    const task = getDocument({ url, disableRange: true });
    task.promise
      .then(async (loaded) => {
        const first = await loaded.getPage(1);
        if (cancelled) return;
        const vp = first.getViewport({ scale: CSS_UNITS });
        owned = true;
        setBaseSize({ width: vp.width, height: vp.height });
        setDoc(loaded);
        setError(null);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof PasswordException) setError("Password-protected PDFs are not supported");
        else setError(`Failed to load PDF: ${err instanceof Error ? err.message : String(err)}`);
      });
    return () => {
      cancelled = true;
      // A loaded document belongs to the doc effect below from here on.
      if (!owned) task.destroy().catch(logErr("pdf load cancel"));
    };
  }, [url]);

  // Free a document once it's replaced or the viewer unmounts. Effect cleanups
  // run after the pages have cancelled their renders against it.
  useEffect(() => {
    if (!doc) return;
    return () => {
      doc.loadingTask.destroy().catch(logErr("pdf destroy"));
    };
  }, [doc]);

  // Fit-width tracks the pane's width, so resizing the editor re-fits.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el || !fit || !baseSize) return;
    const refit = () => setZoom(fitWidthZoom(el.clientWidth, baseSize.width, GUTTER));
    refit();
    const ro = new ResizeObserver(refit);
    ro.observe(el);
    return () => ro.disconnect();
  }, [fit, baseSize]);

  // Keep the same spot in the document on screen across zoom changes.
  const prevZoom = useRef(zoom);
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (el && prevZoom.current !== zoom) {
      el.scrollTop *= zoom / prevZoom.current;
      el.scrollLeft *= zoom / prevZoom.current;
    }
    prevZoom.current = zoom;
  }, [zoom]);

  const zoomBy = useCallback((dir: 1 | -1) => {
    setFit(false);
    setZoom((z) => stepZoom(z, dir));
  }, []);

  // Ctrl/Cmd + wheel (and trackpad pinch, which arrives as ctrl+wheel) zooms.
  // Registered by hand because React's onWheel is passive and can't stop the
  // page itself from zooming.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      setFit(false);
      setZoom((z) => clampZoom(z * Math.exp(-e.deltaY / 300)));
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const onScroll = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const tops = Array.from(el.querySelectorAll<HTMLElement>("[data-pdf-page]"), (p) => p.offsetTop);
    setPage(currentPage(tops, el.scrollTop, el.clientHeight));
  }, []);

  const buttonStyle: CSSProperties = {
    background: "none",
    border: `1px solid ${colors.border}`,
    borderRadius: 4,
    color: colors.text,
    cursor: "pointer",
    fontSize: fontSizes.panels,
    padding: "2px 8px",
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", overflow: "hidden", backgroundColor: colors.sidebar }}>
      <div
        data-testid="pdf-toolbar"
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "4px 8px",
          borderBottom: `1px solid ${colors.border}`,
          color: colors.textMuted,
          fontSize: fontSizes.panels,
        }}
      >
        <button type="button" style={buttonStyle} title="Zoom out" onClick={() => zoomBy(-1)}>
          −
        </button>
        <span style={{ minWidth: 44, textAlign: "center" }}>{Math.round(zoom * 100)}%</span>
        <button type="button" style={buttonStyle} title="Zoom in" onClick={() => zoomBy(1)}>
          +
        </button>
        <button type="button" style={{ ...buttonStyle, background: fit ? colors.selectedBg : "none" }} title="Fit page width" onClick={() => setFit(true)}>
          Fit width
        </button>
        <span style={{ marginLeft: "auto" }} data-testid="pdf-page-indicator">
          {doc ? `Page ${page} / ${doc.numPages}` : ""}
        </span>
      </div>
      {error && <div style={{ padding: 16, color: colors.error, fontSize: 13 }}>{error}</div>}
      {!doc && !error && <div style={{ padding: 16, color: colors.textDim, fontSize: 13 }}>Loading PDF...</div>}
      <div ref={scrollRef} onScroll={onScroll} style={{ flex: 1, overflow: "auto", padding: GUTTER }}>
        {doc && baseSize && (
          // width: max-content lets a zoomed-in page overflow sideways and
          // scroll horizontally instead of being clipped at the left edge.
          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: PAGE_GAP, minWidth: "100%", width: "max-content" }}>
            {Array.from({ length: doc.numPages }, (_, i) => (
              <PdfPage key={i} doc={doc} pageNumber={i + 1} zoom={zoom} fallback={baseSize} rootRef={scrollRef} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

interface PdfPageProps {
  doc: PDFDocumentProxy;
  pageNumber: number;
  zoom: number;
  fallback: PageSize;
  rootRef: RefObject<HTMLDivElement | null>;
}

// PdfPage renders one page (canvas + selectable text layer) only while it's
// near the viewport, and frees its canvas when scrolled far away, so long
// documents don't hold every page's bitmap in memory.
function PdfPage({ doc, pageNumber, zoom, fallback, rootRef }: PdfPageProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const textRef = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);
  // This page's own CSS size at 100%, once known (pages can differ in size).
  const [size, setSize] = useState<PageSize | null>(null);
  const [userUnit, setUserUnit] = useState(1);

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const io = new IntersectionObserver((entries) => setVisible(entries.some((e) => e.isIntersecting)), {
      root: rootRef.current,
      rootMargin: "100% 0px",
    });
    io.observe(el);
    return () => io.disconnect();
  }, [rootRef]);

  useEffect(() => {
    const canvas = canvasRef.current;
    const text = textRef.current;
    if (!canvas || !text) return;
    if (!visible) {
      // Release the bitmap; it's redrawn when the page scrolls back in.
      canvas.width = 0;
      canvas.height = 0;
      text.replaceChildren();
      return;
    }
    let cancelled = false;
    let renderTask: RenderTask | null = null;
    let textLayer: TextLayer | null = null;
    (async () => {
      const pdfPage = await doc.getPage(pageNumber);
      if (cancelled) return;
      const viewport = pdfPage.getViewport({ scale: zoom * CSS_UNITS });
      setSize({ width: viewport.width / zoom, height: viewport.height / zoom });
      setUserUnit(viewport.userUnit);
      const outputScale = canvasOutputScale(viewport.width, viewport.height, window.devicePixelRatio || 1);
      // Draw offscreen, then swap in, so re-renders on zoom/refresh don't
      // blank the page while pdf.js works.
      const offscreen = document.createElement("canvas");
      offscreen.width = Math.floor(viewport.width * outputScale);
      offscreen.height = Math.floor(viewport.height * outputScale);
      renderTask = pdfPage.render({
        canvas: offscreen,
        viewport,
        transform: outputScale !== 1 ? [outputScale, 0, 0, outputScale, 0, 0] : undefined,
      });
      await renderTask.promise;
      if (cancelled) return;
      canvas.width = offscreen.width;
      canvas.height = offscreen.height;
      canvas.getContext("2d")?.drawImage(offscreen, 0, 0);

      const layerDiv = document.createElement("div");
      layerDiv.className = "textLayer";
      textLayer = new TextLayer({ textContentSource: pdfPage.streamTextContent(), container: layerDiv, viewport });
      await textLayer.render();
      if (cancelled) return;
      const end = document.createElement("div");
      end.className = "endOfContent";
      layerDiv.append(end);
      text.replaceChildren(layerDiv);
    })().catch((err: unknown) => {
      if (cancelled || err instanceof RenderingCancelledException) return;
      logErr(`pdf render page ${pageNumber}`)(err);
    });
    return () => {
      cancelled = true;
      renderTask?.cancel();
      textLayer?.cancel();
    };
  }, [visible, doc, pageNumber, zoom]);

  // While a selection is being dragged, stretch endOfContent over the page so
  // the selection doesn't jump when the pointer crosses the gaps between spans.
  useEffect(() => {
    const text = textRef.current;
    if (!text) return;
    const layer = () => text.querySelector(".textLayer");
    const down = () => layer()?.classList.add("selecting");
    const up = () => layer()?.classList.remove("selecting");
    text.addEventListener("mousedown", down);
    window.addEventListener("mouseup", up);
    return () => {
      text.removeEventListener("mousedown", down);
      window.removeEventListener("mouseup", up);
    };
  }, []);

  const cssSize = size ?? fallback;
  const style = {
    "--scale-factor": zoom * CSS_UNITS,
    "--user-unit": userUnit,
    position: "relative",
    flex: "none",
    width: Math.floor(cssSize.width * zoom),
    height: Math.floor(cssSize.height * zoom),
    backgroundColor: "#fff",
    boxShadow: "0 1px 4px rgba(0, 0, 0, 0.3)",
  } as CSSProperties;

  return (
    <div ref={wrapRef} className="loop-pdf-page" data-pdf-page={pageNumber} style={style}>
      <canvas ref={canvasRef} style={{ position: "absolute", inset: 0, width: "100%", height: "100%" }} />
      <div ref={textRef} style={{ position: "absolute", inset: 0 }} />
    </div>
  );
}
