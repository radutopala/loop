import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { WorkflowRun } from "../../api/workflows";
import { darkColors } from "../../theme";
import { WorkflowRunRow } from "./WorkflowRunRow";

function makeRun(over: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    id: "run-abcdef1234567890",
    workflow_name: "deploy",
    channel_id: "",
    dir_path: "",
    worktree_path: "",
    status: "running",
    inputs: "",
    paused_node_id: "",
    error_text: "",
    started_at: new Date(Date.now() - 5000).toISOString(),
    finished_at: null,
    ...over,
  };
}

describe("WorkflowRunRow", () => {
  it("shows name, status, short id and age, and calls onClick", () => {
    const onClick = vi.fn();
    render(<WorkflowRunRow run={makeRun()} isSelected={false} colors={darkColors} onClick={onClick} testId="row" />);
    expect(screen.getByText("deploy")).toBeTruthy();
    expect(screen.getByText("RUNNING")).toBeTruthy();
    expect(screen.getByText("run-abcdef12")).toBeTruthy();
    expect(screen.getByText(/s ago$/)).toBeTruthy();
    expect(screen.queryByTitle("Go to channel")).toBeNull();
    fireEvent.click(screen.getByTestId("row"));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("highlights on hover only when not selected", () => {
    const { rerender } = render(<WorkflowRunRow run={makeRun()} isSelected={false} colors={darkColors} onClick={() => {}} testId="row" />);
    const row = screen.getByTestId("row");
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe("rgba(255, 255, 255, 0.04)");
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe("transparent");

    rerender(<WorkflowRunRow run={makeRun()} isSelected colors={darkColors} onClick={() => {}} testId="row" />);
    const selectedBg = row.style.background;
    fireEvent.mouseEnter(row);
    expect(row.style.background).toBe(selectedBg);
    fireEvent.mouseLeave(row);
    expect(row.style.background).toBe(selectedBg);
  });

  it("links to the channel by name without triggering the row click", () => {
    const onClick = vi.fn();
    const onSelectChannel = vi.fn();
    render(
      <WorkflowRunRow
        run={makeRun({ channel_id: "chan-1234567890", channel_name: "ops", channel_worktree: true, dir_path: "/work/proj" })}
        isSelected={false}
        colors={darkColors}
        onClick={onClick}
        onSelectChannel={onSelectChannel}
      />,
    );
    const link = screen.getByTitle("Go to channel");
    expect(link.textContent).toBe("#ops");
    expect(link.style.cursor).toBe("pointer");
    expect(screen.getByTitle("Channel is a worktree").textContent).toBe("wt");
    expect(screen.getByText("/work/proj")).toBeTruthy();
    fireEvent.click(link);
    expect(onSelectChannel).toHaveBeenCalledWith("chan-1234567890");
    expect(onClick).not.toHaveBeenCalled();
  });

  it("falls back to a short channel id and tolerates a missing channel handler", () => {
    const onClick = vi.fn();
    render(<WorkflowRunRow run={makeRun({ channel_id: "chan-1234567890" })} isSelected={false} colors={darkColors} onClick={onClick} />);
    const link = screen.getByTitle("Go to channel");
    expect(link.textContent).toBe("#chan-123");
    expect(link.style.cursor).toBe("default");
    expect(screen.queryByTitle("Channel is a worktree")).toBeNull();
    fireEvent.click(link);
    expect(onClick).not.toHaveBeenCalled();
  });

  it("shows where a paused run stopped", () => {
    render(<WorkflowRunRow run={makeRun({ status: "paused", paused_node_id: "approve" })} isSelected={false} colors={darkColors} onClick={() => {}} />);
    expect(screen.getByText("PAUSED")).toBeTruthy();
    expect(screen.getByText("at approve")).toBeTruthy();
  });

  it("omits the paused node for a paused run with no node, and dims unknown statuses", () => {
    const run = makeRun({ status: "weird" as WorkflowRun["status"], paused_node_id: "x" });
    render(<WorkflowRunRow run={run} isSelected={false} colors={darkColors} onClick={() => {}} />);
    expect(screen.queryByText("at x")).toBeNull();
    const badge = screen.getByText("WEIRD");
    expect(badge).toBeTruthy();
    render(<WorkflowRunRow run={makeRun({ status: "paused", paused_node_id: "" })} isSelected={false} colors={darkColors} onClick={() => {}} />);
    expect(screen.queryByText(/^at /)).toBeNull();
  });
});
