import { describe, expect, it } from "vitest";
import { isMediaPath, isPdfPath } from "../../api/files";
import { canvasOutputScale, clampZoom, currentPage, fitWidthZoom, MAX_CANVAS_PIXELS, MAX_ZOOM, MIN_ZOOM, stepZoom } from "./pdfZoom";

describe("isPdfPath", () => {
  it.each([
    ["doc.pdf", true],
    ["dir/Report.PDF", true],
    ["doc.pdf.txt", false],
    ["pdf", false],
    ["notes.md", false],
  ])("%s -> %s", (path, want) => {
    expect(isPdfPath(path)).toBe(want);
  });
});

describe("isMediaPath", () => {
  it.each([
    ["a.png", true],
    ["a.mp4", true],
    ["a.pdf", true],
    ["a.go", false],
  ])("%s -> %s", (path, want) => {
    expect(isMediaPath(path)).toBe(want);
  });
});

describe("clampZoom", () => {
  it.each([
    [0.01, MIN_ZOOM],
    [1.3, 1.3],
    [99, MAX_ZOOM],
  ])("%s -> %s", (zoom, want) => {
    expect(clampZoom(zoom)).toBe(want);
  });
});

describe("stepZoom", () => {
  it.each([
    [1, 1, 1.25],
    [1, -1, 0.75],
    [1.37, 1, 1.5],
    [1.37, -1, 1.25],
    [MAX_ZOOM, 1, MAX_ZOOM],
    [MIN_ZOOM, -1, MIN_ZOOM],
  ] as const)("%s step %s -> %s", (zoom, dir, want) => {
    expect(stepZoom(zoom, dir)).toBe(want);
  });
});

describe("fitWidthZoom", () => {
  it("fills the container minus the gutters", () => {
    expect(fitWidthZoom(832, 800, 16)).toBe(1);
  });
  it("clamps to the zoom range", () => {
    expect(fitWidthZoom(100_000, 800, 16)).toBe(MAX_ZOOM);
    expect(fitWidthZoom(10, 800, 16)).toBe(MIN_ZOOM);
  });
  it("falls back to 100% for an unknown page width", () => {
    expect(fitWidthZoom(800, 0, 16)).toBe(1);
  });
});

describe("currentPage", () => {
  const tops = [0, 1000, 2000, 3000];
  it.each([
    [0, 1],
    [500, 1],
    [800, 2],
    [2900, 4],
    [99_999, 4],
  ])("scrollTop %s -> page %s", (scrollTop, want) => {
    expect(currentPage(tops, scrollTop, 900)).toBe(want);
  });
  it("defaults to page 1 with no pages", () => {
    expect(currentPage([], 0, 900)).toBe(1);
  });
});

describe("canvasOutputScale", () => {
  it("uses the device pixel ratio for normal pages", () => {
    expect(canvasOutputScale(800, 1100, 2)).toBe(2);
  });
  it("lowers the scale so huge canvases stay under the pixel cap", () => {
    const scale = canvasOutputScale(3200, 4400, 2);
    expect(scale).toBeLessThan(2);
    expect(3200 * scale * 4400 * scale).toBeCloseTo(MAX_CANVAS_PIXELS);
  });
  it("keeps the device pixel ratio for an empty page", () => {
    expect(canvasOutputScale(0, 0, 2)).toBe(2);
  });
});
