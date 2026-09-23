// Pure layout helpers for the editor's PDF viewer (PdfViewer.tsx), kept out of
// the component so they're testable without pdf.js or a DOM.

// Zoom presets the +/- buttons step through; 1 = 100% (a PDF point rendered at
// its CSS size, 96/72 px).
export const MIN_ZOOM = 0.25;
export const MAX_ZOOM = 4;
export const ZOOM_STEPS = [MIN_ZOOM, 0.5, 0.75, 1, 1.25, 1.5, 2, 3, MAX_ZOOM];

export function clampZoom(zoom: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));
}

// stepZoom returns the next preset above (dir 1) or below (dir -1) the current
// zoom, so a fit-width zoom like 1.37 steps to 1.5 / 1.25 instead of drifting.
export function stepZoom(zoom: number, dir: 1 | -1): number {
  const eps = 1e-6;
  if (dir > 0) return ZOOM_STEPS.find((z) => z > zoom + eps) ?? MAX_ZOOM;
  return [...ZOOM_STEPS].reverse().find((z) => z < zoom - eps) ?? MIN_ZOOM;
}

// fitWidthZoom is the zoom at which a page `pageWidthCss` wide (its CSS width
// at 100%) fills `containerWidth` minus `gutter` on each side.
export function fitWidthZoom(containerWidth: number, pageWidthCss: number, gutter: number): number {
  if (pageWidthCss <= 0) return 1;
  return clampZoom((containerWidth - 2 * gutter) / pageWidthCss);
}

// currentPage returns the 1-based page shown at the top third of the scroll
// viewport, given each page's top offset in document order.
export function currentPage(pageTops: number[], scrollTop: number, viewportHeight: number): number {
  const probe = scrollTop + viewportHeight / 3;
  let page = 1;
  for (const [i, top] of pageTops.entries()) {
    if (top > probe) break;
    page = i + 1;
  }
  return page;
}

// Cap on a page canvas's backing-store pixels (pdf.js's own default,
// maxCanvasPixels). Past it a zoomed-in page on a HiDPI screen would allocate
// hundreds of MB, so the canvas resolution drops below devicePixelRatio.
export const MAX_CANVAS_PIXELS = 2 ** 25;

// canvasOutputScale returns the backing-store scale for a page canvas of
// `width`×`height` CSS px: devicePixelRatio for crisp text, lowered so the
// canvas stays within MAX_CANVAS_PIXELS.
export function canvasOutputScale(width: number, height: number, dpr: number): number {
  const area = width * height;
  if (area <= 0) return dpr;
  return Math.min(dpr, Math.sqrt(MAX_CANVAS_PIXELS / area));
}
