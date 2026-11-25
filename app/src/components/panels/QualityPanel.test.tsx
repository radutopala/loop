import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchQualityC4,
  fetchQualityClones,
  fetchQualityComplexity,
  fetchQualityCycles,
  fetchQualityEvolution,
  fetchQualitySnapshot,
  NoPreviousSignal,
  type QualityFileTile,
  type QualityScanReport,
  type QualitySnapshot,
  simulateQualityWhatif,
  triggerQualityScan,
} from "../../api/quality";
import { useEventStream } from "../../hooks/useEventStream";
import type { WSEvent } from "../../types";
import { QualityPanel } from "./QualityPanel";

vi.mock("../../api/quality", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/quality")>()),
  fetchQualityC4: vi.fn(),
  fetchQualityClones: vi.fn(),
  fetchQualityComplexity: vi.fn(),
  fetchQualityCycles: vi.fn(),
  fetchQualityEvolution: vi.fn(),
  fetchQualitySnapshot: vi.fn(),
  simulateQualityWhatif: vi.fn(),
  triggerQualityScan: vi.fn(),
}));

// The event stream rides a WebSocket; tests drive the panel's handler directly.
vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

// mermaid lays out SVG with browser measurement APIs jsdom lacks.
vi.mock("mermaid", () => ({ default: { initialize: vi.fn(), render: vi.fn().mockResolvedValue({ svg: "<svg></svg>" }) } }));

const tile = (over: Partial<QualityFileTile> = {}): QualityFileTile => ({
  path: "pkg/main.go",
  loc: 100,
  deficit: 0.5,
  metric_deficits: { cycles: 0.5 },
  top_reason: "cycles",
  ...over,
});

const snapshot = (over: Partial<QualitySnapshot> = {}): QualitySnapshot => ({
  dir_path: "/repo",
  branch: "main",
  current_branch: "main",
  branch_mismatch: false,
  signal: 7500,
  previous_signal: 7000,
  geo_mean: 0.75,
  scanned_at: "",
  metrics: [{ name: "cycles", score: 0.9, raw: 1 }],
  tiles: [tile()],
  ...over,
});

const report = (over: Partial<QualityScanReport> = {}): QualityScanReport => ({
  dir_path: "/repo",
  branch: "feature",
  signal: 4000,
  previous_signal: NoPreviousSignal,
  geo_mean: 0.4,
  file_count: 1,
  parse_failed: 0,
  scanned_at: "2026-02-03T04:05:06Z",
  metrics: [],
  tiles: [tile({ path: "new.go" })],
  rules: { passed: [], failed: [{ name: "no-cycles", severity: "error", message: "Cycle found" }] },
  ...over,
});

type ROCallback = (entries: { contentRect: { width: number; height: number } }[]) => void;

function stubResizeObserver() {
  const callbacks: ROCallback[] = [];
  const observe = vi.fn();
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(cb: ROCallback) {
        callbacks.push(cb);
      }
      observe = observe;
      disconnect() {}
      unobserve() {}
    },
  );
  return { callbacks, observe };
}

function emit(event: WSEvent | { type: string; data: unknown }) {
  const calls = vi.mocked(useEventStream).mock.calls;
  const { onEvent } = calls[calls.length - 1]![0];
  act(() => onEvent(event as WSEvent));
}

const renderPanel = (props: Partial<React.ComponentProps<typeof QualityPanel>> = {}) => {
  const onClose = vi.fn();
  const utils = render(<QualityPanel channelId="ch1" dirPath="/repo" branch="main" onClose={onClose} {...props} />);
  return { ...utils, onClose };
};

beforeEach(() => {
  vi.resetAllMocks();
  stubResizeObserver();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("QualityPanel", () => {
  it("shows loading with a Close button, then the empty state, and subscribes to the channel", async () => {
    let resolve: (s: QualitySnapshot | null) => void = () => {};
    vi.mocked(fetchQualitySnapshot).mockReturnValue(new Promise((r) => (resolve = r)));
    const { onClose } = renderPanel();
    expect(screen.getByText("Loading…")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(fetchQualitySnapshot).toHaveBeenCalledWith("ch1");
    expect(vi.mocked(useEventStream).mock.calls[0]![0].channelId).toBe("ch1");

    await act(async () => resolve(null));
    expect(screen.getByText(/Quality scans your codebase/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("hides Close when embedded in every state", async () => {
    let resolve: (s: QualitySnapshot | null) => void = () => {};
    vi.mocked(fetchQualitySnapshot).mockReturnValue(new Promise((r) => (resolve = r)));
    const { rerender } = renderPanel({ embedded: true });
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
    await act(async () => resolve(null));
    expect(screen.getByRole("button", { name: "Scan now" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();

    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    rerender(<QualityPanel channelId="ch2" dirPath="/repo" branch="main" embedded onClose={vi.fn()} />);
    expect(await screen.findByText("7500")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("shows snapshot load errors in the empty state, including non-Error rejections", async () => {
    vi.mocked(fetchQualitySnapshot).mockRejectedValue(new Error("load failed"));
    const { unmount } = renderPanel();
    expect(await screen.findByText("load failed")).toBeTruthy();
    unmount();

    vi.mocked(fetchQualitySnapshot).mockRejectedValue("plain failure");
    renderPanel();
    expect(await screen.findByText("plain failure")).toBeTruthy();
  });

  it("starts a scan from the empty state and enters the scanning view", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(null);
    vi.mocked(triggerQualityScan).mockResolvedValue({ status: "started" });
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Scan now" }));
    expect(triggerQualityScan).toHaveBeenCalledWith("ch1");
    const scanBtn = screen.getByRole("button", { name: "Scanning…" });
    expect((scanBtn as HTMLButtonElement).disabled).toBe(true);
    // No display yet: the headline is the bare scanning label.
    expect(screen.getAllByText("Scanning…")).toHaveLength(2);
    // Diagnostics with nothing scanned shows its empty state.
    await userEvent.click(screen.getByRole("button", { name: "Diagnostics" }));
    expect(screen.getByText("No diagnostics available — run a scan first.")).toBeTruthy();
  });

  it("stays scanning when another scan is already in flight", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(null);
    vi.mocked(triggerQualityScan).mockResolvedValue({ status: "in_progress" });
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Scan now" }));
    expect(screen.getByRole("button", { name: "Scanning…" })).toBeTruthy();
  });

  it("returns to the empty state with the error when the scan request fails", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(null);
    vi.mocked(triggerQualityScan).mockRejectedValueOnce(new Error("scan refused")).mockRejectedValueOnce("scan string");
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Scan now" }));
    expect(await screen.findByText("scan refused")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Scan now" }));
    expect(await screen.findByText("scan string")).toBeTruthy();
    expect(screen.queryByText("scan refused")).toBeNull();
  });

  it("shows a scan error as a banner over an existing snapshot", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    vi.mocked(triggerQualityScan).mockRejectedValue(new Error("busy"));
    renderPanel();
    await userEvent.click(await screen.findByRole("button", { name: "Scan now" }));
    expect(await screen.findByText("busy")).toBeTruthy();
    expect(screen.getByText("7500")).toBeTruthy();
  });

  it("renders a positive delta headline with the previous signal and scan time", async () => {
    const scannedAt = "2026-01-01T10:00:00Z";
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot({ scanned_at: scannedAt }));
    renderPanel();
    const delta = await screen.findByText("Δ +500");
    expect(delta.style.color).toBe("rgb(34, 197, 94)");
    expect(screen.getByText("7500").style.color).toBe("rgb(34, 197, 94)");
    expect(screen.getByText("7000")).toBeTruthy();
    expect(screen.getByText(new RegExp(`geo-mean 0\\.750 · branch "main" · ${new Date(scannedAt).toLocaleString().replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`))).toBeTruthy();
    // Snapshots carry no rules, so Overview lists none.
    expect(screen.queryByText(/Failing rules/)).toBeNull();
    expect(screen.getByText("raw 1.000")).toBeTruthy();
  });

  it("renders negative and zero deltas without a scan time", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot({ signal: 6000, previous_signal: 6500 }));
    const { unmount } = renderPanel();
    const neg = await screen.findByText("Δ -500");
    expect(neg.style.color).toBe("rgb(239, 68, 68)");
    expect(screen.getByText("6000").style.color).toBe("rgb(245, 158, 11)");
    expect(screen.getByText(/geo-mean 0\.750 · branch "main"$/)).toBeTruthy();
    unmount();

    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot({ signal: 6000, previous_signal: 6000, tiles: undefined as unknown as QualityFileTile[] }));
    renderPanel();
    expect(await screen.findByText("Δ 0")).toBeTruthy();
    // Without tiles the treemap draws its placeholder leaf.
    expect(screen.getByText("no data")).toBeTruthy();
  });

  it("warns when the snapshot was taken on another branch", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot({ branch_mismatch: true, branch: "old", current_branch: "new" }));
    renderPanel();
    expect(await screen.findByText('Snapshot taken on "old", current branch "new" — Scan now to refresh')).toBeTruthy();
  });

  it("tracks scan lifecycle events and replaces the snapshot with the scan report", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    renderPanel();
    await screen.findByText("Δ +500");

    emit({ type: "quality.session_started", data: null });
    expect(screen.getByRole("button", { name: "Scanning…" })).toBeTruthy();

    // Malformed progress payloads are ignored.
    emit({ type: "quality.scan_progress", data: null });
    emit({ type: "quality.scan_progress", data: "nope" });
    emit({ type: "quality.scan_progress", data: { done: 1 } });
    emit({ type: "quality.scan_progress", data: { done: "1", total: 2 } });
    expect(screen.queryByText(/files$/)).toBeNull();

    emit({ type: "quality.scan_progress", data: { done: 3, total: 4 } });
    expect(screen.getByText("Scanning… 3/4 files")).toBeTruthy();
    const bar = screen.getByText("Scanning… 3/4 files").nextElementSibling?.firstElementChild as HTMLElement;
    expect(bar.style.width).toBe("75%");

    emit({ type: "quality.scan_progress", data: { done: 0, total: 0 } });
    const zeroBar = screen.getByText("Scanning… 0/0 files").nextElementSibling?.firstElementChild as HTMLElement;
    expect(zeroBar.style.width).toBe("0%");

    // Unknown events change nothing.
    emit({ type: "quality.other", data: null });
    expect(screen.getByText("Scanning… 0/0 files")).toBeTruthy();

    vi.mocked(fetchQualitySnapshot).mockClear();
    emit({ type: "quality.scanned", data: report() });
    expect(screen.getByRole("button", { name: "Scan now" })).toBeTruthy();
    expect(fetchQualitySnapshot).toHaveBeenCalledWith("ch1");
    // The report has no previous signal: first-scan headline.
    expect(screen.getByText("4000").style.color).toBe("rgb(239, 68, 68)");
    expect(screen.getByText(/first scan · geo-mean 0\.400 · branch "feature" · /)).toBeTruthy();
    expect(screen.getByText("Failing rules (1)")).toBeTruthy();
    expect(screen.getByText("Cycle found")).toBeTruthy();
  });

  it("handles a scan report without tiles or rules, and a first-scan headline without a time", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot({ previous_signal: NoPreviousSignal, scanned_at: "" }));
    renderPanel();
    expect(await screen.findByText(/first scan · geo-mean 0\.750 · branch "main"$/)).toBeTruthy();

    emit({ type: "quality.scanned", data: report({ tiles: undefined as unknown as QualityFileTile[], rules: undefined as unknown as QualityScanReport["rules"] }) });
    expect(screen.getByText("no data")).toBeTruthy();
    expect(screen.queryByText(/Failing rules/)).toBeNull();
  });

  it("leaves the scanning state on cancel and session end", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    renderPanel();
    await screen.findByText("Δ +500");
    for (const type of ["quality.scan_cancelled", "quality.session_ended"]) {
      emit({ type: "quality.session_started", data: null });
      emit({ type: "quality.scan_progress", data: { done: 1, total: 2 } });
      expect(screen.getByText("Scanning… 1/2 files")).toBeTruthy();
      emit({ type, data: null });
      expect(screen.getByRole("button", { name: "Scan now" })).toBeTruthy();
      expect(screen.getByText("Δ +500")).toBeTruthy();
    }
  });

  it("refetches tab data after a new scan", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    vi.mocked(fetchQualityCycles).mockResolvedValue({ cycles: [], largest_cycle_size: 0, total_nodes_in_cycles: 0 });
    renderPanel();
    await screen.findByText("Δ +500");
    await userEvent.click(screen.getByRole("button", { name: "Cycles" }));
    expect(await screen.findByText("No import cycles detected — graph is acyclic.")).toBeTruthy();
    expect(fetchQualityCycles).toHaveBeenCalledTimes(1);
    emit({ type: "quality.scanned", data: report() });
    await waitFor(() => expect(fetchQualityCycles).toHaveBeenCalledTimes(2));
  });

  it("switches between every tab", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    vi.mocked(fetchQualityCycles).mockResolvedValue({ cycles: [], largest_cycle_size: 0, total_nodes_in_cycles: 0 });
    vi.mocked(fetchQualityEvolution).mockResolvedValue(null);
    vi.mocked(fetchQualityC4).mockResolvedValue({ mermaid: "", component_count: 0, edge_count: 0 });
    vi.mocked(fetchQualityComplexity).mockRejectedValue(new Error("complexity down"));
    vi.mocked(fetchQualityClones).mockRejectedValue(new Error("clones down"));
    renderPanel();
    await screen.findByText("Δ +500");

    const tab = (name: string) => screen.getByRole("button", { name });
    await userEvent.click(tab("Diagnostics"));
    expect(screen.getByText("Files by deficit (1)")).toBeTruthy();
    expect(tab("Diagnostics").style.borderBottom).toContain("2px solid");
    expect(tab("Overview").style.borderBottom).toBe("2px solid transparent");
    await userEvent.click(tab("Hotspots"));
    expect(await screen.findByText("complexity down")).toBeTruthy();
    await userEvent.click(tab("Evolution"));
    expect(await screen.findByText(/No git history available/)).toBeTruthy();
    await userEvent.click(tab("What-if"));
    expect(screen.getByText(/No simulation yet/)).toBeTruthy();
    await userEvent.click(tab("C4"));
    expect(await screen.findByText("No components to diagram — run a scan first.")).toBeTruthy();
    await userEvent.click(tab("Overview"));
    expect(screen.getByText("raw 1.000")).toBeTruthy();
  });

  it("runs a what-if delete from a diagnostics tile and shows the result", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    let resolve: (v: Awaited<ReturnType<typeof simulateQualityWhatif>>) => void = () => {};
    vi.mocked(simulateQualityWhatif).mockReturnValue(new Promise((r) => (resolve = r)));
    renderPanel();
    await screen.findByText("Δ +500");
    await userEvent.click(screen.getByRole("button", { name: "Diagnostics" }));
    await userEvent.click(screen.getByRole("button", { name: /pkg\/main\.go/ }));
    await userEvent.click(screen.getByRole("button", { name: "Simulate delete" }));
    expect(simulateQualityWhatif).toHaveBeenCalledWith("ch1", [{ op: "delete", path: "pkg/main.go" }]);
    expect(screen.getByText("Loading…")).toBeTruthy();
    await act(async () =>
      resolve({
        mutations: [{ op: "delete", path: "pkg/main.go" }],
        baseline_signal: 7500,
        predicted_signal: 7600,
        delta_signal: 100,
        baseline_metrics: [],
        predicted_metrics: [],
      }),
    );
    expect(screen.getByText("delete pkg/main.go")).toBeTruthy();
    expect(screen.getByText("Δ +100")).toBeTruthy();
  });

  it("shows what-if errors from the overview treemap, including non-Error rejections", async () => {
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot());
    vi.mocked(simulateQualityWhatif).mockRejectedValueOnce(new Error("sim broke")).mockRejectedValueOnce("sim string");
    const { container } = renderPanel();
    await screen.findByText("Δ +500");
    const clickTile = () => act(() => container.querySelector("rect")?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    clickTile();
    await userEvent.click(screen.getByRole("button", { name: "Simulate delete" }));
    expect(await screen.findByText("sim broke")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "Overview" }));
    clickTile();
    await userEvent.click(screen.getByRole("button", { name: "Simulate delete" }));
    expect(await screen.findByText("sim string")).toBeTruthy();
  });

  it("resizes the treemap to its container", async () => {
    const { callbacks, observe } = stubResizeObserver();
    vi.mocked(fetchQualitySnapshot).mockResolvedValue(snapshot({ tiles: [tile({ path: "small.go", loc: 10 }), tile({ path: "large.go", loc: 1000 })] }));
    const { container } = renderPanel();
    await screen.findByText("Δ +500");
    // Tiles are laid out largest first.
    expect(Array.from(container.querySelectorAll("rect > title")).map((t) => t.textContent?.split("\n")[0])).toEqual(["large.go", "small.go"]);
    const svg = () => container.querySelector("svg") as SVGSVGElement;
    expect(svg().getAttribute("width")).toBe("600");
    expect(observe).toHaveBeenCalledWith(svg().parentElement);

    const cb = callbacks[callbacks.length - 1]!;
    act(() => cb([]));
    act(() => cb([{ contentRect: { width: 0, height: 100 } }]));
    act(() => cb([{ contentRect: { width: 100, height: 0 } }]));
    expect(svg().getAttribute("width")).toBe("600");
    act(() => cb([{ contentRect: { width: 400, height: 200 } }]));
    expect(svg().getAttribute("width")).toBe("400");
    expect(svg().getAttribute("height")).toBe("200");
  });
});
