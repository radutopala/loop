import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import mermaid from "mermaid";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchQualityC4,
  fetchQualityClones,
  fetchQualityComplexity,
  fetchQualityCycles,
  fetchQualityEvolution,
  type QualityClonesResponse,
  type QualityComplexityResponse,
  type QualityEvolutionResponse,
} from "../../../api/quality";
import { DEFAULT_FONT_SIZES, ThemeProvider } from "../../../ThemeContext";
import { darkColors } from "../../../theme";
import { C4Tab } from "./C4Tab";
import { CyclesTab } from "./CyclesTab";
import { EvolutionTab } from "./EvolutionTab";
import { HotspotsTab } from "./HotspotsTab";

vi.mock("../../../api/quality", () => ({
  fetchQualityC4: vi.fn(),
  fetchQualityClones: vi.fn(),
  fetchQualityComplexity: vi.fn(),
  fetchQualityCycles: vi.fn(),
  fetchQualityEvolution: vi.fn(),
}));

// mermaid lays out SVG with browser measurement APIs jsdom lacks.
vi.mock("mermaid", () => ({ default: { initialize: vi.fn(), render: vi.fn() } }));

// A clone member's path, line and name sit across a span and sibling text nodes.
const memberText = (text: string) => (_: string, el: Element | null) => el?.tagName === "SPAN" && el.textContent === text;

const props = { channelId: "ch1", scanGeneration: 0, colors: darkColors, fontSizes: DEFAULT_FONT_SIZES };

beforeEach(() => {
  vi.resetAllMocks();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("CyclesTab", () => {
  it("shows loading, then an error", async () => {
    vi.mocked(fetchQualityCycles).mockRejectedValue(new Error("cycles failed"));
    render(<CyclesTab {...props} />);
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(await screen.findByText("cycles failed")).toBeTruthy();
    expect(fetchQualityCycles).toHaveBeenCalledWith("ch1");
  });

  it("shows the empty state for an acyclic graph", async () => {
    vi.mocked(fetchQualityCycles).mockResolvedValue({ cycles: [], largest_cycle_size: 0, total_nodes_in_cycles: 0 });
    render(<CyclesTab {...props} />);
    expect(await screen.findByText("No import cycles detected — graph is acyclic.")).toBeTruthy();
  });

  it("lists a single cycle with singular wording", async () => {
    vi.mocked(fetchQualityCycles).mockResolvedValue({ cycles: [["a.go", "b.go"]], largest_cycle_size: 2, total_nodes_in_cycles: 2 });
    render(<CyclesTab {...props} />);
    expect(await screen.findByText("1 cycle · largest 2 files · 2 files in cycles")).toBeTruthy();
    expect(screen.getByText("Cycle #1 (2 files)")).toBeTruthy();
    expect(screen.getByText("↻")).toBeTruthy();
    expect(screen.getByText("↓")).toBeTruthy();
    expect(screen.getByText("b.go")).toBeTruthy();
  });

  it("pluralises several cycles", async () => {
    vi.mocked(fetchQualityCycles).mockResolvedValue({ cycles: [["a.go"], ["c.go"]], largest_cycle_size: 1, total_nodes_in_cycles: 2 });
    render(<CyclesTab {...props} />);
    expect(await screen.findByText("2 cycles · largest 1 files · 2 files in cycles")).toBeTruthy();
    expect(screen.getByText("Cycle #2 (1 files)")).toBeTruthy();
  });
});

describe("EvolutionTab", () => {
  const evo = (over: Partial<QualityEvolutionResponse> = {}): QualityEvolutionResponse => ({
    commits_scanned: 1,
    shallow_warning: false,
    coupling_pairs: [],
    churn_hotspots: [],
    bus_factor: [],
    ...over,
  });

  it("shows loading then the error", async () => {
    vi.mocked(fetchQualityEvolution).mockRejectedValue(new Error("evo failed"));
    render(<EvolutionTab {...props} />);
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(await screen.findByText("evo failed")).toBeTruthy();
  });

  it("shows the no-git empty state for a null response", async () => {
    vi.mocked(fetchQualityEvolution).mockResolvedValue(null);
    render(<EvolutionTab {...props} />);
    expect(await screen.findByText("No git history available — evolution analysis requires a git repository.")).toBeTruthy();
  });

  it("shows empty sections and singular commit wording", async () => {
    vi.mocked(fetchQualityEvolution).mockResolvedValue(evo());
    render(<EvolutionTab {...props} />);
    expect(await screen.findByText("1 commit scanned")).toBeTruthy();
    expect(screen.getByText("No strong coupling detected.")).toBeTruthy();
    expect(screen.getByText("No churn hotspots.")).toBeTruthy();
    expect(screen.getByText("No bus-factor risk detected.")).toBeTruthy();
  });

  it("lists coupling, churn and bus-factor rows", async () => {
    const lastChanged = "2026-03-04T05:06:07Z";
    vi.mocked(fetchQualityEvolution).mockResolvedValue(
      evo({
        commits_scanned: 7,
        shallow_warning: true,
        coupling_pairs: [
          { file_a: "a.go", file_b: "b.go", co_change_count: 4, jaccard: 0.75, cross_module: true },
          { file_a: "c.go", file_b: "d.go", co_change_count: 2, jaccard: 0.5, cross_module: false },
        ],
        churn_hotspots: [{ file: "hot.go", change_count: 9, last_changed_at: lastChanged }],
        bus_factor: [
          { file: "solo.go", sole_author: "dev1", sole_author_ratio: 0.9, total_commits: 10, days_since_last_other_author: 12 },
          { file: "only.go", sole_author: "dev2", sole_author_ratio: 1, total_commits: 3, days_since_last_other_author: 0 },
        ],
      }),
    );
    render(<EvolutionTab {...props} />);
    expect(await screen.findByText(/7 commits scanned/)).toBeTruthy();
    expect(screen.getByText(/shallow clone — try git fetch --unshallow/)).toBeTruthy();
    expect(screen.getByText("Coupling pairs (2)")).toBeTruthy();
    expect(screen.getByText("jaccard 0.750 · 4 co-changes · cross-module")).toBeTruthy();
    expect(screen.getByText("jaccard 0.500 · 2 co-changes")).toBeTruthy();
    expect(screen.getByText(`9 changes · last ${new Date(lastChanged).toLocaleString()}`)).toBeTruthy();
    expect(screen.getByText("dev1 · 90% of 10 commits · 12d since other author")).toBeTruthy();
    expect(screen.getByText("dev2 · 100% of 3 commits")).toBeTruthy();
  });
});

describe("HotspotsTab", () => {
  const complexity = (over: Partial<QualityComplexityResponse> = {}): QualityComplexityResponse => ({
    score: 0.5,
    raw: 1,
    total_functions: 10,
    over_threshold: 2,
    histogram: {},
    functions: [],
    offset: 0,
    limit: 50,
    returned: 0,
    ...over,
  });
  const clones = (over: Partial<QualityClonesResponse> = {}): QualityClonesResponse => ({
    score: 0.9,
    raw: 1,
    duplicated_loc: 1200,
    total_loc: 34000,
    cluster_count: 0,
    clusters: [],
    offset: 0,
    limit: 25,
    returned: 0,
    ...over,
  });

  it("fetches with the hotspot limits and shows an error from either request", async () => {
    vi.mocked(fetchQualityComplexity).mockResolvedValue(complexity());
    vi.mocked(fetchQualityClones).mockRejectedValue(new Error("clones failed"));
    render(<HotspotsTab {...props} />);
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(await screen.findByText("clones failed")).toBeTruthy();
    expect(fetchQualityComplexity).toHaveBeenCalledWith("ch1", { limit: 50 });
    expect(fetchQualityClones).toHaveBeenCalledWith("ch1", { limit: 25 });
  });

  it("prefers the complexity error", async () => {
    vi.mocked(fetchQualityComplexity).mockRejectedValue(new Error("complexity failed"));
    vi.mocked(fetchQualityClones).mockRejectedValue(new Error("clones failed"));
    render(<HotspotsTab {...props} />);
    expect(await screen.findByText("complexity failed")).toBeTruthy();
    expect(screen.queryByText("clones failed")).toBeNull();
  });

  it("renders nothing for sections whose response is null", async () => {
    vi.mocked(fetchQualityComplexity).mockResolvedValue(null as unknown as QualityComplexityResponse);
    vi.mocked(fetchQualityClones).mockResolvedValue(null as unknown as QualityClonesResponse);
    render(<HotspotsTab {...props} />);
    await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
    expect(screen.queryByText(/Complex functions/)).toBeNull();
    expect(screen.queryByText(/Clone clusters/)).toBeNull();
  });

  it("shows empty sections", async () => {
    vi.mocked(fetchQualityComplexity).mockResolvedValue(complexity());
    vi.mocked(fetchQualityClones).mockResolvedValue(clones());
    render(<HotspotsTab {...props} />);
    expect(await screen.findByText("No functions over the complexity threshold.")).toBeTruthy();
    expect(screen.getByText("No clone clusters detected.")).toBeTruthy();
    expect(screen.getByText("Complex functions (2 of 10 over threshold · score 0.500)")).toBeTruthy();
    expect(screen.getByText(`Clone clusters (0 total · ${(1200).toLocaleString()} of ${(34000).toLocaleString()} LOC duplicated)`)).toBeTruthy();
  });

  it("lists functions and clone clusters, the first cluster open and others toggleable", async () => {
    vi.mocked(fetchQualityComplexity).mockResolvedValue(
      complexity({
        functions: [{ path: "big.go", name: "Handle", start_line: 42, cyclomatic: 30, cognitive: 40, max_nesting: 6, param_count: 5, loc: 200, score: 0.25 }],
      }),
    );
    vi.mocked(fetchQualityClones).mockResolvedValue(
      clones({
        cluster_count: 2,
        clusters: [
          {
            loc: 1500,
            max_distance: 0,
            members: [
              { path: "x.go", name: "Dup", start_line: 1, end_line: 20, loc: 20 },
              { path: "y.go", name: "Dup2", start_line: 5, end_line: 25, loc: 21 },
            ],
          },
          { loc: 30, max_distance: 3, members: [{ path: "z.go", name: "Other", start_line: 9, end_line: 19, loc: 11 }] },
        ],
      }),
    );
    render(<HotspotsTab {...props} />);
    expect(await screen.findByTitle("Per-function score (1.0 healthy, 0 worst)")).toHaveProperty("textContent", "0.25");
    expect(screen.getByText("big.go")).toBeTruthy();
    expect(screen.getByText(":42")).toBeTruthy();
    expect(screen.getByText("cyc 30 · cog 40 · nest 6 · params 5 · LOC 200")).toBeTruthy();
    expect(screen.getByText(/Showing the 2/)).toBeTruthy();

    const first = screen.getByRole("button", { name: /Cluster #1 — 2 members/ });
    expect(first.textContent).toContain("▾");
    expect(screen.getByText(memberText("x.go:1 Dup"))).toBeTruthy();
    expect(screen.getByText("LOC 21")).toBeTruthy();

    const second = screen.getByRole("button", { name: /Cluster #2 — 1 members/ });
    expect(second.textContent).toContain("▸");
    expect(second.textContent).toContain("30 LOC · max-distance 3");
    expect(screen.queryByText(memberText("z.go:9 Other"))).toBeNull();
    await userEvent.click(second);
    expect(screen.getByText(memberText("z.go:9 Other"))).toBeTruthy();
    await userEvent.click(first);
    expect(screen.queryByText(memberText("x.go:1 Dup"))).toBeNull();
  });
});

describe("C4Tab", () => {
  const c4 = { mermaid: "graph TD; A-->B", component_count: 2, edge_count: 1 };

  it("shows loading then the fetch error", async () => {
    vi.mocked(fetchQualityC4).mockRejectedValue(new Error("c4 failed"));
    render(<C4Tab {...props} />);
    expect(screen.getByText("Loading…")).toBeTruthy();
    expect(await screen.findByText("c4 failed")).toBeTruthy();
    expect(mermaid.render).not.toHaveBeenCalled();
  });

  it("shows the empty state when there are no components", async () => {
    vi.mocked(fetchQualityC4).mockResolvedValue({ mermaid: "", component_count: 0, edge_count: 0 });
    render(<C4Tab {...props} />);
    expect(await screen.findByText("No components to diagram — run a scan first.")).toBeTruthy();
    expect(mermaid.render).not.toHaveBeenCalled();
  });

  it("renders the diagram SVG with the dark mermaid theme", async () => {
    vi.mocked(fetchQualityC4).mockResolvedValue(c4);
    vi.mocked(mermaid.render).mockResolvedValue({ svg: "<svg data-testid='c4-svg'></svg>" } as Awaited<ReturnType<typeof mermaid.render>>);
    render(<C4Tab {...props} />);
    expect(await screen.findByText("2 components · 1 edges")).toBeTruthy();
    expect(await screen.findByTestId("c4-svg")).toBeTruthy();
    expect(mermaid.initialize).toHaveBeenCalledWith({ startOnLoad: false, theme: "dark", securityLevel: "strict" });
    expect(vi.mocked(mermaid.render).mock.calls[0]![0]).toMatch(/^quality-c4-\d+$/);
    expect(vi.mocked(mermaid.render).mock.calls[0]![1]).toBe("graph TD; A-->B");
  });

  it("uses the default mermaid theme in a light theme", async () => {
    vi.mocked(fetchQualityC4).mockResolvedValue(c4);
    vi.mocked(mermaid.render).mockResolvedValue({ svg: "<svg></svg>" } as Awaited<ReturnType<typeof mermaid.render>>);
    render(
      <ThemeProvider initialTheme="light">
        <C4Tab {...props} />
      </ThemeProvider>,
    );
    await waitFor(() => expect(mermaid.initialize).toHaveBeenCalledWith({ startOnLoad: false, theme: "default", securityLevel: "strict" }));
  });

  it("shows render errors, including non-Error rejections", async () => {
    vi.mocked(fetchQualityC4).mockResolvedValue(c4);
    vi.mocked(mermaid.render).mockRejectedValueOnce(new Error("parse error"));
    const { rerender } = render(<C4Tab {...props} />);
    expect(await screen.findByText("parse error")).toBeTruthy();

    vi.mocked(fetchQualityC4).mockResolvedValue({ ...c4 });
    vi.mocked(mermaid.render).mockRejectedValueOnce("bad syntax");
    rerender(<C4Tab {...props} scanGeneration={1} />);
    expect(await screen.findByText("bad syntax")).toBeTruthy();
  });

  it("drops a render that finishes after unmount", async () => {
    vi.mocked(fetchQualityC4).mockResolvedValue(c4);
    let resolveRender: (v: Awaited<ReturnType<typeof mermaid.render>>) => void = () => {};
    vi.mocked(mermaid.render).mockReturnValue(new Promise((res) => (resolveRender = res)) as ReturnType<typeof mermaid.render>);
    const { unmount } = render(<C4Tab {...props} />);
    await waitFor(() => expect(mermaid.render).toHaveBeenCalled());
    unmount();
    await act(async () => resolveRender({ svg: "<svg data-testid='late'></svg>" } as Awaited<ReturnType<typeof mermaid.render>>));
    expect(screen.queryByTestId("late")).toBeNull();
  });

  it("drops a render error that lands after unmount", async () => {
    vi.mocked(fetchQualityC4).mockResolvedValue(c4);
    let rejectRender: (e: unknown) => void = () => {};
    vi.mocked(mermaid.render).mockReturnValue(new Promise((_, rej) => (rejectRender = rej)) as ReturnType<typeof mermaid.render>);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    const { unmount } = render(<C4Tab {...props} />);
    await waitFor(() => expect(mermaid.render).toHaveBeenCalled());
    unmount();
    await act(async () => rejectRender(new Error("late failure")));
    expect(screen.queryByText("late failure")).toBeNull();
    expect(consoleError).not.toHaveBeenCalled();
  });
});
