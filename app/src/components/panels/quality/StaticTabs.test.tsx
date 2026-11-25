import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { hierarchy } from "@visx/hierarchy";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { QualityFileTile, QualityRule, QualityWhatifResponse } from "../../../api/quality";
import { DEFAULT_FONT_SIZES } from "../../../ThemeContext";
import { darkColors } from "../../../theme";
import { DiagnosticsTab } from "./DiagnosticsTab";
import { OverviewTab, type OverviewTabProps } from "./OverviewTab";
import { buildTreemapData, type TreemapDatum } from "./shared";
import { WhatifTab } from "./WhatifTab";

const base = { colors: darkColors, fontSizes: DEFAULT_FONT_SIZES, btnStyle: {} };

const tile = (over: Partial<QualityFileTile> = {}): QualityFileTile => ({
  path: "pkg/a.go",
  loc: 100,
  deficit: 0.3,
  metric_deficits: { cycles: 0.3 },
  top_reason: "cycles",
  ...over,
});

const rootFor = (tiles: QualityFileTile[]) =>
  hierarchy<TreemapDatum>(buildTreemapData(tiles))
    .sum((d) => d.size ?? 0)
    .sort((a, b) => (b.value ?? 0) - (a.value ?? 0));

const display = (over: Partial<NonNullable<OverviewTabProps["display"]>> = {}): NonNullable<OverviewTabProps["display"]> => ({
  signal: 8000,
  geo_mean: 0.8,
  metrics: [],
  tiles: [],
  scanned_at: "",
  branch: "main",
  ...over,
});

function Harness({
  tiles,
  failedRules = [],
  onSimulateDelete = vi.fn(),
  size = { width: 600, height: 320 },
  ...rest
}: {
  tiles: QualityFileTile[];
  failedRules?: QualityRule[];
  onSimulateDelete?: (p: string) => void;
  size?: { width: number; height: number };
  display?: OverviewTabProps["display"];
  noRoot?: boolean;
}) {
  const [selected, setSelected] = useState<QualityFileTile | null>(null);
  return (
    <OverviewTab
      display={"display" in rest ? (rest.display ?? null) : display({ tiles })}
      treemapRoot={rest.noRoot ? null : rootFor(tiles)}
      size={size}
      containerRef={() => {}}
      selectedTile={selected}
      setSelectedTile={setSelected}
      failedRules={failedRules}
      onSimulateDelete={onSimulateDelete}
      {...base}
    />
  );
}

describe("DiagnosticsTab", () => {
  it("shows the empty state without tiles", () => {
    render(<DiagnosticsTab tiles={[]} {...base} onSimulateDelete={vi.fn()} />);
    expect(screen.getByText("No diagnostics available — run a scan first.")).toBeTruthy();
  });

  it("sorts tiles by deficit and expands and collapses a tile's detail", async () => {
    const onSimulateDelete = vi.fn();
    const tiles = [tile({ path: "low.go", deficit: 0.1, top_reason: "" }), tile({ path: "high.go", deficit: 0.8 })];
    render(<DiagnosticsTab tiles={tiles} {...base} onSimulateDelete={onSimulateDelete} />);
    expect(screen.getByText("Files by deficit (2)")).toBeTruthy();
    const rows = screen.getAllByRole("button") as [HTMLElement, HTMLElement];
    expect(rows.map((r) => r.textContent)).toEqual(["high.go100 LOC · 0.80 · Cycles", "low.go100 LOC · 0.10"]);

    await userEvent.click(rows[0]);
    await userEvent.click(screen.getByRole("button", { name: "Simulate delete" }));
    expect(onSimulateDelete).toHaveBeenCalledWith("high.go");

    // Clicking the row again collapses it.
    await userEvent.click(rows[0]);
    expect(screen.queryByRole("button", { name: "Simulate delete" })).toBeNull();

    // The detail's Close button collapses it too.
    await userEvent.click(rows[1]);
    expect(screen.getByText("LOC 100 · deficit 0.10")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });
});

describe("OverviewTab", () => {
  it("renders nothing without display, treemap or failing rules", () => {
    const { container } = render(<Harness tiles={[]} display={null} noRoot />);
    expect(container.innerHTML).toBe("");
  });

  it("shows metric cards", () => {
    render(
      <OverviewTab
        display={display({ metrics: [{ name: "depth", score: 0.912, raw: 3.25 }] })}
        treemapRoot={null}
        size={{ width: 600, height: 320 }}
        containerRef={() => {}}
        selectedTile={null}
        setSelectedTile={vi.fn()}
        failedRules={[]}
        onSimulateDelete={vi.fn()}
        {...base}
      />,
    );
    expect(screen.getByText("Depth")).toBeTruthy();
    expect(screen.getByText("0.912")).toBeTruthy();
    expect(screen.getByText("raw 3.250")).toBeTruthy();
  });

  it("draws a placeholder leaf without a tile that ignores clicks", () => {
    const { container } = render(<Harness tiles={[]} />);
    const rects = container.querySelectorAll("rect");
    expect(rects).toHaveLength(1);
    const leaf = rects[0]!;
    expect(screen.getByText("no data")).toBeTruthy();
    expect(leaf.querySelector("title")).toBeNull();
    expect(leaf.style.cursor).toBe("default");
    fireEvent.click(leaf);
    expect(screen.queryByRole("button", { name: "Simulate delete" })).toBeNull();
  });

  it("draws leaf tiles, skips zero-size ones, and selects a tile on click", async () => {
    const onSimulateDelete = vi.fn();
    const tiles = [tile({ path: "big/main.go", loc: 100000, deficit: 0.9 }), tile({ path: "mid.go", loc: 30000, top_reason: "" }), tile({ path: "tiny.go", loc: 1 })];
    const { container } = render(<Harness tiles={tiles} onSimulateDelete={onSimulateDelete} />);
    const titles = Array.from(container.querySelectorAll("rect > title")).map((t) => t.textContent);
    // The one-LOC tile rounds to zero width and is not drawn.
    expect(titles).toEqual(["big/main.go\nLOC 100000 · deficit 0.90 · cycles", "mid.go\nLOC 30000 · deficit 0.30"]);
    expect(screen.getByText("big/main.go")).toBeTruthy();
    // The reason label rides under the name only when the tile has a top reason.
    expect(screen.getAllByText("Cycles")).toHaveLength(1);

    const rect = container.querySelectorAll("rect")[0]!;
    expect(rect.getAttribute("stroke-width")).toBe("0.5");
    fireEvent.click(rect);
    expect(container.querySelectorAll("rect")[0]!.getAttribute("stroke-width")).toBe("2");
    expect(container.querySelectorAll("rect")[0]!.getAttribute("stroke")).toBe(darkColors.active);
    expect(container.querySelectorAll("rect")[1]!.getAttribute("stroke-width")).toBe("0.5");

    await userEvent.click(screen.getByRole("button", { name: "Simulate delete" }));
    expect(onSimulateDelete).toHaveBeenCalledWith("big/main.go");
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("fills a leaf without a deficit as healthy", () => {
    const root = hierarchy<TreemapDatum>({ name: "root", children: [{ name: "bare", size: 1 }] }).sum((d) => d.size ?? 0);
    const { container } = render(
      <OverviewTab
        display={null}
        treemapRoot={root}
        size={{ width: 200, height: 100 }}
        containerRef={() => {}}
        selectedTile={null}
        setSelectedTile={vi.fn()}
        failedRules={[]}
        onSimulateDelete={vi.fn()}
        {...base}
      />,
    );
    expect(container.querySelector("rect")?.getAttribute("fill")).toBe("rgb(34, 197, 94)");
  });

  it("leaves labels off tiles too small to hold them", () => {
    const { container } = render(<Harness tiles={[tile()]} size={{ width: 60, height: 100 }} />);
    expect(container.querySelectorAll("rect")).toHaveLength(1);
    expect(container.querySelectorAll("text")).toHaveLength(0);
  });

  it("shows the name but not the reason on a tile 24 to 38 px tall", () => {
    const { container } = render(<Harness tiles={[tile()]} size={{ width: 100, height: 30 }} />);
    expect(Array.from(container.querySelectorAll("text")).map((t) => t.textContent)).toEqual(["pkg/a.go"]);
  });

  it("shows neither label on a wide but short tile", () => {
    const { container } = render(<Harness tiles={[tile()]} size={{ width: 100, height: 24 }} />);
    expect(container.querySelectorAll("text")).toHaveLength(0);
  });

  it("lists failing rules with up to five citations", () => {
    const rules: QualityRule[] = [
      {
        name: "no-cycles",
        severity: "error",
        message: "Cycles found",
        citations: [{ path: "c1.go", note: "imports c2" }, { path: "c2.go" }, { path: "c3.go" }, { path: "c4.go" }, { path: "c5.go" }, { path: "c6.go" }, { path: "c7.go" }],
      },
      { name: "max-depth", severity: "warn", message: "Too deep", citations: [] },
      { name: "plain", severity: "warn", message: "No citations" },
    ];
    render(<Harness tiles={[]} display={null} noRoot failedRules={rules} />);
    expect(screen.getByText("Failing rules (3)")).toBeTruthy();
    expect(screen.getByText("c1.go — imports c2")).toBeTruthy();
    expect(screen.getByText("c5.go")).toBeTruthy();
    expect(screen.queryByText("c6.go")).toBeNull();
    expect(screen.getByText("… and 2 more")).toBeTruthy();
    expect(screen.getByText("Too deep")).toBeTruthy();
    expect(screen.getByText("No citations")).toBeTruthy();
  });

  it("omits the overflow line with five or fewer citations", () => {
    const rules: QualityRule[] = [{ name: "r", severity: "warn", message: "m", citations: [{ path: "only.go" }] }];
    render(<Harness tiles={[]} display={null} noRoot failedRules={rules} />);
    expect(screen.getByText("only.go")).toBeTruthy();
    expect(screen.queryByText(/more$/)).toBeNull();
  });
});

describe("WhatifTab", () => {
  const result = (over: Partial<QualityWhatifResponse> = {}): QualityWhatifResponse => ({
    mutations: [{ op: "delete", path: "a.go" }],
    baseline_signal: 6000,
    predicted_signal: 7200,
    delta_signal: 1200,
    baseline_metrics: [],
    predicted_metrics: [],
    ...over,
  });
  const wprops = { colors: darkColors, fontSizes: DEFAULT_FONT_SIZES };

  it("shows the empty state before any simulation", () => {
    render(<WhatifTab target={null} loading={false} error={null} result={null} {...wprops} />);
    expect(screen.getByText("No simulation yet. Click a tile in Overview or Diagnostics, then 'Simulate delete'.")).toBeTruthy();
  });

  it("shows loading, error, and nothing without a result", () => {
    const { rerender, container } = render(<WhatifTab target={null} loading error={null} result={null} {...wprops} />);
    expect(screen.getByText("Loading…")).toBeTruthy();
    rerender(<WhatifTab target="a.go" loading={false} error="sim failed" result={null} {...wprops} />);
    expect(screen.getByText("sim failed")).toBeTruthy();
    rerender(<WhatifTab target="a.go" loading={false} error={null} result={null} {...wprops} />);
    expect(container.innerHTML).toBe("");
  });

  it("shows a positive delta and the per-metric breakdown", () => {
    render(
      <WhatifTab
        target="a.go"
        loading={false}
        error={null}
        result={result({
          baseline_metrics: [
            { name: "cycles", score: 0.5, raw: 0 },
            { name: "depth", score: 0.8, raw: 0 },
            { name: "equality", score: 0.7, raw: 0 },
            { name: "gone", score: 0.4, raw: 0 },
          ],
          predicted_metrics: [
            { name: "cycles", score: 0.75, raw: 0 },
            { name: "depth", score: 0.6, raw: 0 },
            { name: "equality", score: 0.7, raw: 0 },
          ],
        })}
        {...wprops}
      />,
    );
    expect(screen.getByText("delete a.go")).toBeTruthy();
    expect(screen.getByText("6000")).toBeTruthy();
    expect(screen.getByText("7200")).toBeTruthy();
    const delta = screen.getByText("Δ +1200");
    expect(delta.style.color).toBe("rgb(34, 197, 94)");
    expect(screen.getByText("+0.250").style.color).toBe("rgb(34, 197, 94)");
    expect(screen.getByText("-0.200").style.color).toBe("rgb(239, 68, 68)");
    // An unchanged metric, and one missing from the prediction, both show a zero delta.
    expect(screen.getAllByText("0.000")).toHaveLength(2);
    expect(screen.getByText("gone")).toBeTruthy();
  });

  it("colours negative and zero deltas", () => {
    const { rerender } = render(<WhatifTab target="a.go" loading={false} error={null} result={result({ delta_signal: -300 })} {...wprops} />);
    expect(screen.getByText("Δ -300").style.color).toBe("rgb(239, 68, 68)");
    rerender(<WhatifTab target="a.go" loading={false} error={null} result={result({ delta_signal: 0 })} {...wprops} />);
    expect(screen.getByText("Δ 0")).toBeTruthy();
  });
});
