import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import { Chevron } from "./Chevron";
import { DeletingOverlay } from "./DeletingOverlay";
import { ElapsedTimer } from "./ElapsedTimer";
import { ErrorBar } from "./ErrorBar";
import { LoopLogo } from "./LoopLogo";
import { StatusBadge } from "./StatusBadge";
import { UnifiedDiff } from "./UnifiedDiff";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("LoopLogo", () => {
  it("renders the Loop mark", () => {
    render(<LoopLogo />);
    expect(screen.getByRole("img", { name: "Loop" }).getAttribute("width")).toBe("80");
  });
});

describe("ElapsedTimer", () => {
  it.each([
    [0, "00:00:00"],
    [59, "00:00:59"],
    [3725, "01:02:05"],
  ])("formats %i seconds as %s", (seconds, want) => {
    render(<ElapsedTimer seconds={seconds} />);
    expect(screen.getByText(want)).toBeTruthy();
  });
});

describe("Chevron", () => {
  it("leaves a right-pointing chevron unrotated at the default size", () => {
    const { container } = render(<Chevron deg={0} />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("width")).toBe("10");
    expect(svg.style.transform).toBe("none");
  });

  it("rotates by deg and merges the caller's size and style", () => {
    const { container } = render(<Chevron deg={90} size={14} style={{ opacity: 0.5 }} />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("height")).toBe("14");
    expect(svg.style.transform).toBe("rotate(90deg)");
    expect(svg.style.opacity).toBe("0.5");
  });
});

describe("ErrorBar", () => {
  it("shows the message and dismisses on the close button", () => {
    const onDismiss = vi.fn();
    render(<ErrorBar message="Save failed" onDismiss={onDismiss} />);
    expect(screen.getByRole("alert").textContent).toContain("Save failed");
    fireEvent.click(screen.getByRole("button"));
    expect(onDismiss).toHaveBeenCalledOnce();
  });
});

describe("UnifiedDiff", () => {
  it("colours each kind of line and keeps blank lines visible", () => {
    render(<UnifiedDiff diff={"--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n same\n\n"} testId="diff" style={{ maxHeight: 100 }} />);
    const pre = screen.getByTestId("diff");
    expect(pre.style.maxHeight).toBe("100px");
    const rows = [...pre.children] as HTMLElement[];
    expect(rows.map((r) => r.textContent)).toEqual(["--- a/x", "+++ b/x", "@@ -1 +1 @@", "-old", "+new", " same", " "]);
    const [meta, , hunk, del, add, ctx] = rows;
    expect(meta!.style.color).toBe(css(darkColors.textDim));
    expect(meta!.style.backgroundColor).toBe("");
    expect(hunk!.style.backgroundColor).toBe(css(darkColors.diffHunkBg));
    expect(del!.style.color).toBe(css(darkColors.diffDelText));
    expect(del!.style.backgroundColor).toBe(css(darkColors.diffDelBg));
    expect(add!.style.color).toBe(css(darkColors.diffAddText));
    expect(add!.style.backgroundColor).toBe(css(darkColors.diffAddBg));
    expect(ctx!.style.color).toBe(css(darkColors.text));
  });
});

describe("DeletingOverlay", () => {
  it("starts right of the sidebar", () => {
    const sidebar = document.createElement("div");
    sidebar.dataset.testid = "sidebar";
    document.body.appendChild(sidebar);
    vi.spyOn(sidebar, "getBoundingClientRect").mockReturnValue({ right: 240 } as DOMRect);
    render(<DeletingOverlay message="Deleting channel…" />);
    const overlay = screen.getByTestId("deleting-overlay");
    expect(overlay.style.left).toBe("240px");
    expect(overlay.textContent).toContain("Deleting channel…");
    sidebar.remove();
  });

  it("covers the whole window without a sidebar", () => {
    render(<DeletingOverlay message="Deleting" />);
    expect(screen.getByTestId("deleting-overlay").style.left).toBe("0px");
  });
});

describe("StatusBadge", () => {
  it.each([
    ["connecting", darkColors.warning],
    ["running", darkColors.active],
    ["completed", darkColors.textDim],
    ["failed", darkColors.error],
  ] as const)("colours %s", (status, color) => {
    render(<StatusBadge status={status} />);
    expect(screen.getByText(status).style.backgroundColor).toBe(css(color));
  });

  it("shows the elapsed time only while hovered", () => {
    render(<StatusBadge status="running" elapsed={65} />);
    const badge = screen.getByText("running");
    expect(screen.queryByText("00:01:05")).toBeNull();
    fireEvent.mouseEnter(badge);
    expect(screen.getByText("00:01:05")).toBeTruthy();
    fireEvent.mouseLeave(badge);
    expect(screen.queryByText("00:01:05")).toBeNull();
  });

  it.each([undefined, 0])("shows no elapsed time on hover when elapsed is %s", (elapsed) => {
    render(<StatusBadge status="completed" elapsed={elapsed} />);
    const badge = screen.getByText("completed");
    act(() => {
      fireEvent.mouseEnter(badge);
    });
    expect(badge.textContent).toBe("completed");
  });
});
