import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { WorktreeInfo } from "../../api/git";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import { WorktreeRows } from "./WorktreeRows";

const withThread: WorktreeInfo = { path: "/repo/.worktrees/login-fix", branch: "feat/login", thread_id: "t1" };
const withoutThread: WorktreeInfo = { path: "/repo/.worktrees/scratch/", branch: "" };

describe("WorktreeRows", () => {
  it("goes to a worktree's thread and imports one without, closing the picker first", () => {
    const calls: string[] = [];
    const onDone = vi.fn(() => calls.push("done"));
    const onSelectThread = vi.fn((id: string) => calls.push(`thread ${id}`));
    const onImport = vi.fn((path: string) => calls.push(`import ${path}`));
    const parentClick = vi.fn();
    render(
      <div onClick={parentClick}>
        <WorktreeRows testId="wts" worktrees={[withThread, withoutThread]} search="" onSelectThread={onSelectThread} onImport={onImport} onDone={onDone} />
      </div>,
    );
    expect(screen.getByText("Worktrees")).toBeTruthy();
    const rows = screen.getAllByTestId("worktree-row");
    expect(rows.map((r) => r.textContent)).toEqual(["feat/loginlogin-fixgo", "(detached)/repo/.worktrees/scratch/imp"]);

    fireEvent.click(screen.getByTestId("worktree-go"));
    fireEvent.click(screen.getByTestId("worktree-import"));
    expect(calls).toEqual(["done", "thread t1", "done", `import ${withoutThread.path}`]);
    expect(parentClick).not.toHaveBeenCalled();
  });

  it("highlights rows and buttons on hover", () => {
    render(<WorktreeRows worktrees={[withThread, withoutThread]} search="" onSelectThread={vi.fn()} onImport={vi.fn()} onDone={vi.fn()} />);
    const row = screen.getAllByTestId("worktree-row")[0]!;
    fireEvent.mouseEnter(row);
    expect(row.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
    fireEvent.mouseLeave(row);
    expect(row.style.backgroundColor).toBe("transparent");

    const go = screen.getByTestId("worktree-go");
    fireEvent.mouseEnter(go);
    expect(go.style.opacity).toBe("0.7");
    fireEvent.mouseLeave(go);
    expect(go.style.opacity).toBe("1");

    const imp = screen.getByTestId("worktree-import");
    fireEvent.mouseEnter(imp);
    expect(imp.style.color).toBe(css(darkColors.active));
    fireEvent.mouseLeave(imp);
    expect(imp.style.color).toBe(css(darkColors.textDim));
  });

  it("offers import for a thread worktree when going to threads isn't wired, and nothing without either", () => {
    const { rerender } = render(<WorktreeRows worktrees={[withThread]} search="" onImport={vi.fn()} onDone={vi.fn()} />);
    expect(screen.getByTestId("worktree-import")).toBeTruthy();
    rerender(<WorktreeRows worktrees={[withThread]} search="" onDone={vi.fn()} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("filters by search and says when nothing matches", () => {
    render(<WorktreeRows worktrees={[withThread, withoutThread]} search="nope" onDone={vi.fn()} />);
    expect(screen.queryByTestId("worktree-row")).toBeNull();
    expect(screen.getByText("No worktrees found")).toBeTruthy();
  });
});
