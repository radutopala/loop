import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkflowDef } from "../../api/workflows";
import { darkColors } from "../../theme";
import { WorkflowDefList } from "./WorkflowDefList";

const def = (name: string, description: string, scope: WorkflowDef["scope"]): WorkflowDef => ({ name, description, scope, nodes: [] });

const grouped = {
  global: [def("deploy", "Ship to prod", "global"), def("nightly", "Cleanup job", "global")],
  project: [def("lint", "Run linters", "project")],
};

function renderList(over: Partial<Parameters<typeof WorkflowDefList>[0]> = {}) {
  const onSelect = vi.fn();
  const onRun = vi.fn();
  const view = render(<WorkflowDefList grouped={grouped} selectedName={null} onSelect={onSelect} onRun={onRun} colors={darkColors} {...over} />);
  return { onSelect, onRun, ...view };
}

describe("WorkflowDefList", () => {
  it("renders nothing when there are no workflows", () => {
    const { container } = renderList({ grouped: { global: [], project: [] } });
    expect(container.innerHTML).toBe("");
  });

  it("groups workflows under Global and Project headings", () => {
    renderList();
    expect(screen.getByText("Global")).toBeTruthy();
    expect(screen.getByText("Project")).toBeTruthy();
    expect(screen.getByTestId("workflow-def-deploy").getAttribute("title")).toBe("Ship to prod");
    expect(screen.getByTestId("workflow-def-lint")).toBeTruthy();
  });

  it("hides a heading whose group is empty", () => {
    renderList({ grouped: { global: [], project: grouped.project } });
    expect(screen.queryByText("Global")).toBeNull();
    expect(screen.getByText("Project")).toBeTruthy();
  });

  it("selects a row, deselects the selected row, and clears with All runs", async () => {
    const user = userEvent.setup();
    const { onSelect, rerender, onRun } = renderList();
    await user.click(screen.getByTestId("workflow-def-deploy"));
    expect(onSelect).toHaveBeenLastCalledWith("deploy");

    rerender(<WorkflowDefList grouped={grouped} selectedName="deploy" onSelect={onSelect} onRun={onRun} colors={darkColors} />);
    await user.click(screen.getByTestId("workflow-def-deploy"));
    expect(onSelect).toHaveBeenLastCalledWith(null);

    onSelect.mockClear();
    await user.click(screen.getByRole("button", { name: "All runs" }));
    expect(onSelect).toHaveBeenCalledWith(null);
  });

  it("runs a workflow without selecting its row", async () => {
    const user = userEvent.setup();
    const { onSelect, onRun } = renderList();
    await user.click(screen.getByTestId("workflow-run-lint"));
    expect(onRun).toHaveBeenCalledWith("lint");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("highlights the run button on hover", () => {
    renderList();
    const btn = screen.getByTestId("workflow-run-deploy");
    fireEvent.mouseEnter(btn);
    expect(btn.style.background).not.toBe("transparent");
    fireEvent.mouseLeave(btn);
    expect(btn.style.background).toBe("transparent");
  });

  it("filters by name and description, case-insensitively", async () => {
    const user = userEvent.setup();
    renderList();
    const search = screen.getByTestId("workflow-search");
    await user.type(search, "CLEANUP");
    expect(screen.getByTestId("workflow-def-nightly")).toBeTruthy();
    expect(screen.queryByTestId("workflow-def-deploy")).toBeNull();
    expect(screen.queryByText("Project")).toBeNull();

    await user.clear(search);
    await user.type(search, "lint");
    expect(screen.getByTestId("workflow-def-lint")).toBeTruthy();
    expect(screen.queryByText("Global")).toBeNull();
  });

  it("treats a missing description as empty when filtering", async () => {
    const user = userEvent.setup();
    const noDesc = { name: "bare", nodes: [] } as unknown as WorkflowDef;
    renderList({ grouped: { global: [noDesc], project: [] } });
    await user.type(screen.getByTestId("workflow-search"), "zzz");
    expect(screen.queryByTestId("workflow-def-bare")).toBeNull();
  });

  it("says when nothing matches", async () => {
    const user = userEvent.setup();
    renderList();
    await user.type(screen.getByTestId("workflow-search"), "  nope ");
    expect(screen.getByText("No workflows match “nope”")).toBeTruthy();
  });

  it("shows every workflow for a whitespace-only query", async () => {
    const user = userEvent.setup();
    renderList();
    await user.type(screen.getByTestId("workflow-search"), "   ");
    expect(screen.getByTestId("workflow-def-deploy")).toBeTruthy();
    expect(screen.queryByText(/No workflows match/)).toBeNull();
  });
});
