import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { QualityFileTile } from "../../../api/quality";
import { DEFAULT_FONT_SIZES } from "../../../ThemeContext";
import { darkColors } from "../../../theme";
import { bandColor, buildTreemapData, deficitColor, EmptyState, ErrorState, formatScannedAt, LoadingState, metricLabel, SectionHelp, shortPath, TileDetail, useAsyncFetch } from "./shared";

const tile = (over: Partial<QualityFileTile> = {}): QualityFileTile => ({
  path: "internal/api/server.go",
  loc: 120,
  deficit: 0.4,
  metric_deficits: { cycles: 0.2, complexity: 0.9 },
  top_reason: "complexity",
  ...over,
});

describe("shared helpers", () => {
  it("shortPath keeps the last two segments of long paths", () => {
    expect(shortPath("a/b/c/d.go")).toBe("c/d.go");
    expect(shortPath("b/c.go")).toBe("b/c.go");
    expect(shortPath("c.go")).toBe("c.go");
  });

  it("bandColor maps the signal to red, amber and green", () => {
    expect(bandColor(4999)).toBe("#ef4444");
    expect(bandColor(5000)).toBe("#f59e0b");
    expect(bandColor(6999)).toBe("#f59e0b");
    expect(bandColor(7000)).toBe("#22c55e");
  });

  it("deficitColor interpolates green to red and clamps out-of-range input", () => {
    expect(deficitColor(0)).toBe("rgb(34, 197, 94)");
    expect(deficitColor(1)).toBe("rgb(239, 68, 68)");
    expect(deficitColor(-3)).toBe("rgb(34, 197, 94)");
    expect(deficitColor(5)).toBe("rgb(239, 68, 68)");
  });

  it("metricLabel names known metrics and passes unknown ones through", () => {
    expect(metricLabel("modularity")).toBe("Modularity");
    expect(metricLabel("redundancy")).toBe("Redundancy");
    expect(metricLabel("mystery")).toBe("mystery");
  });

  it("formatScannedAt returns empty for missing or invalid timestamps", () => {
    expect(formatScannedAt("")).toBe("");
    expect(formatScannedAt("not a date")).toBe("");
    const ts = "2026-01-02T03:04:05Z";
    expect(formatScannedAt(ts)).toBe(new Date(ts).toLocaleString());
  });

  it("buildTreemapData returns a placeholder leaf when there are no tiles", () => {
    expect(buildTreemapData([])).toEqual({ name: "root", children: [{ name: "no data", size: 1, deficit: 0 }] });
  });

  it("buildTreemapData sizes tiles by LOC with a floor of one", () => {
    const a = tile({ path: "x/y/z.go", loc: 0, deficit: 0.1 });
    const b = tile({ path: "w.go", loc: 50 });
    expect(buildTreemapData([a, b])).toEqual({
      name: "root",
      children: [
        { name: "y/z.go", size: 1, deficit: 0.1, tile: a },
        { name: "w.go", size: 50, deficit: 0.4, tile: b },
      ],
    });
  });
});

describe("useAsyncFetch", () => {
  it("resolves to data", async () => {
    const { result } = renderHook(() => useAsyncFetch(() => Promise.resolve(42), []));
    expect(result.current).toEqual({ data: null, error: null, loading: true });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current).toEqual({ data: 42, error: null, loading: false });
  });

  it("reports Error messages and non-Error rejections", async () => {
    const err = renderHook(() => useAsyncFetch(() => Promise.reject(new Error("boom")), []));
    await waitFor(() => expect(err.result.current.loading).toBe(false));
    expect(err.result.current.error).toBe("boom");

    const str = renderHook(() => useAsyncFetch(() => Promise.reject("plain"), []));
    await waitFor(() => expect(str.result.current.loading).toBe(false));
    expect(str.result.current.error).toBe("plain");
  });

  it("refetches when deps change and ignores results of a cancelled fetch", async () => {
    const resolvers: ((v: string) => void)[] = [];
    const rejecters: ((e: unknown) => void)[] = [];
    const fn = vi.fn(
      () =>
        new Promise<string>((res, rej) => {
          resolvers.push(res);
          rejecters.push(rej);
        }),
    );
    const { result, rerender } = renderHook(({ dep }) => useAsyncFetch(fn, [dep]), { initialProps: { dep: 1 } });
    rerender({ dep: 2 });
    expect(fn).toHaveBeenCalledTimes(2);
    await act(async () => {
      resolvers[0]!("stale");
    });
    expect(result.current).toEqual({ data: null, error: null, loading: true });
    await act(async () => resolvers[1]!("fresh"));
    expect(result.current).toEqual({ data: "fresh", error: null, loading: false });

    // A rejection that lands after the next dep change is dropped too.
    rerender({ dep: 3 });
    rerender({ dep: 4 });
    await act(async () => rejecters[2]!(new Error("late")));
    expect(result.current.error).toBeNull();
    expect(result.current.loading).toBe(true);
  });
});

describe("state components", () => {
  it("render their text", () => {
    render(
      <>
        <LoadingState colors={darkColors} fontSizes={DEFAULT_FONT_SIZES} />
        <ErrorState message="bad thing" colors={darkColors} fontSizes={DEFAULT_FONT_SIZES} />
        <EmptyState text="nothing here" colors={darkColors} fontSizes={DEFAULT_FONT_SIZES} />
        <SectionHelp colors={darkColors}>
          <b>help body</b>
        </SectionHelp>
      </>,
    );
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(screen.getByText("bad thing")).toBeTruthy();
    expect(screen.getByText("nothing here")).toBeTruthy();
    expect(screen.getByText("help body")).toBeTruthy();
  });
});

describe("TileDetail", () => {
  it("shows the tile, metric deficits worst-first, and wires its buttons", async () => {
    const onClose = vi.fn();
    const onSimulateDelete = vi.fn();
    const { container } = render(<TileDetail tile={tile()} onClose={onClose} onSimulateDelete={onSimulateDelete} colors={darkColors} fontSizes={DEFAULT_FONT_SIZES} btnStyle={{}} />);
    expect(screen.getByText("internal/api/server.go")).toBeTruthy();
    expect(screen.getByText(/LOC 120 · deficit 0\.40 · top reason Complexity/)).toBeTruthy();
    const labels = Array.from(container.querySelectorAll("div[style*='width: 90px']")).map((d) => d.textContent);
    expect(labels).toEqual(["Complexity", "Cycles"]);
    expect(screen.getByText("0.90")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Simulate delete" }));
    expect(onSimulateDelete).toHaveBeenCalledWith("internal/api/server.go");
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("omits the top reason and handles a tile without metric deficits", () => {
    const t = tile({ top_reason: "", metric_deficits: undefined as unknown as Record<string, number> });
    render(<TileDetail tile={t} onClose={vi.fn()} onSimulateDelete={vi.fn()} colors={darkColors} fontSizes={DEFAULT_FONT_SIZES} btnStyle={{}} />);
    expect(screen.getByText("LOC 120 · deficit 0.40")).toBeTruthy();
    expect(screen.queryByText(/top reason/)).toBeNull();
  });
});
