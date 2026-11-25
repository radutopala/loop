import { render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LoopInfinityIcon } from "./LoopInfinityIcon";

// jsdom has no SVG geometry: give paths a length and a point along it.
const geometry = {
  getTotalLength: () => 100,
  getPointAtLength: (len: number) => ({ x: len, y: -len }),
};

describe("LoopInfinityIcon", () => {
  let frames: FrameRequestCallback[];
  const cancel = vi.fn();
  const reducedMotion = (matches: boolean) => vi.stubGlobal("matchMedia", (q: string) => ({ matches: matches && q === "(prefers-reduced-motion: reduce)" }));

  beforeEach(() => {
    frames = [];
    cancel.mockClear();
    for (const [name, value] of Object.entries(geometry)) Object.defineProperty(SVGElement.prototype, name, { value, configurable: true });
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => frames.push(cb));
    vi.stubGlobal("cancelAnimationFrame", cancel);
    reducedMotion(false);
  });
  afterEach(() => {
    for (const name of Object.keys(geometry)) delete (SVGElement.prototype as unknown as Record<string, unknown>)[name];
    vi.unstubAllGlobals();
  });

  const paths = (c: HTMLElement) => c.querySelectorAll("path");

  it("draws a still outline in the given color", () => {
    const { container } = render(<LoopInfinityIcon color="red" />);
    expect(paths(container)).toHaveLength(2);
    expect(paths(container)[1]?.getAttribute("stroke")).toBe("red");
    expect(container.querySelector("circle")).toBeNull();
    expect(frames).toHaveLength(0);
  });

  it("draws the outline in the current color without one", () => {
    const { container } = render(<LoopInfinityIcon />);
    expect(paths(container)[1]?.getAttribute("stroke")).toBe("currentColor");
  });

  it("animates a trail and a head along the path until unmounted", () => {
    const { container, unmount } = render(<LoopInfinityIcon animated />);
    const head = container.querySelector("circle")!;
    const trail = paths(container)[2]!;
    expect(trail.style.strokeDasharray).toBe("35 65");
    expect(head.getAttribute("fill")).toBe("#22c55e");
    expect(container.querySelectorAll("stop")[1]?.getAttribute("stop-color")).toBe("#86efac");

    // The first frame starts the clock; the next moves the head on.
    frames.shift()!(1000);
    expect(head.getAttribute("cx")).toBe("0");
    frames.shift()!(2000);
    expect(head.getAttribute("cx")).toBe("25");
    expect(head.getAttribute("cy")).toBe("-25");
    expect(trail.style.strokeDashoffset).toBe("75");
    expect(head.getAttribute("fill")).toMatch(/^rgb\(\d+,\d+,\d+\)$/);
    expect(container.querySelectorAll("stop")[0]?.getAttribute("stop-color")).toMatch(/^rgb\(/);
    expect(Number(trail.getAttribute("opacity"))).toBeGreaterThanOrEqual(0.4);
    expect(frames).toHaveLength(1);

    unmount();
    expect(cancel).toHaveBeenCalled();
  });

  it("uses grays in a light theme", () => {
    const { container } = render(<LoopInfinityIcon animated isDark={false} />);
    expect(container.querySelector("circle")?.getAttribute("fill")).toBe("#3c3c3c");
    frames.shift()!(0);
    // Every gray stop has equal channels.
    for (const s of container.querySelectorAll("stop")) {
      const [r, g, b] = (s.getAttribute("stop-color")?.match(/\d+/g) ?? []).map(Number);
      expect(r).toBe(g);
      expect(g).toBe(b);
    }
  });

  it("draws once and holds still when less motion is asked for", () => {
    reducedMotion(true);
    const { container } = render(<LoopInfinityIcon animated />);
    expect(frames).toHaveLength(0);
    expect(container.querySelector("circle")?.getAttribute("cx")).toBe("0");
  });
});
