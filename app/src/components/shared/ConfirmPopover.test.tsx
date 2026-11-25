import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import { ConfirmPopover } from "./ConfirmPopover";

describe("ConfirmPopover", () => {
  it("centers below its parent by default, asking to delete", () => {
    render(<ConfirmPopover onYes={vi.fn()} onNo={vi.fn()} testId="pop" />);
    const pop = screen.getByTestId("pop");
    expect(pop.dataset.side).toBe("below");
    expect(screen.getByText("Delete?")).toBeTruthy();
    expect(pop.style.top).toBe("100%");
    expect(pop.style.marginTop).toBe("10px");
    expect(pop.style.left).toBe("50%");
    expect(pop.style.transform).toBe("translateX(-50%)");
    const arrow = pop.querySelector("svg")!;
    expect(arrow.style.top).toBe("-8px");
    expect(arrow.style.transform).toBe("translateX(-50%)");
  });

  it("lines up with the parent's left edge above it", () => {
    render(<ConfirmPopover label="Remove?" onYes={vi.fn()} onNo={vi.fn()} testId="pop" align="start" above />);
    const pop = screen.getByTestId("pop");
    expect(pop.dataset.side).toBe("above");
    expect(screen.getByText("Remove?")).toBeTruthy();
    expect(pop.style.bottom).toBe("100%");
    expect(pop.style.marginBottom).toBe("6px");
    expect(pop.style.left).toBe("4px");
    expect(pop.style.transform).toBe("");
    const arrow = pop.querySelector("svg")!;
    expect(arrow.style.bottom).toBe("-8px");
    expect(arrow.style.left).toBe("12px");
    expect(arrow.style.transform).toBe("rotate(180deg)");
  });

  it("centers above with a flipped arrow", () => {
    render(<ConfirmPopover onYes={vi.fn()} onNo={vi.fn()} testId="pop" above />);
    expect(screen.getByTestId("pop").querySelector("svg")!.style.transform).toBe("translateX(-50%) rotate(180deg)");
  });

  it("sits below at the left edge without transforms", () => {
    render(<ConfirmPopover onYes={vi.fn()} onNo={vi.fn()} testId="pop" align="start" />);
    expect(screen.getByTestId("pop").querySelector("svg")!.style.transform).toBe("");
  });

  it("answers yes or no without the click or mousedown reaching the parent", () => {
    const onYes = vi.fn();
    const onNo = vi.fn();
    const parentClick = vi.fn();
    const parentDown = vi.fn();
    render(
      <div onClick={parentClick} onMouseDown={parentDown}>
        <ConfirmPopover onYes={onYes} onNo={onNo} testId="pop" />
      </div>,
    );
    fireEvent.click(screen.getByTestId("confirm-yes"));
    expect(onYes).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByTestId("confirm-no"));
    expect(onNo).toHaveBeenCalledOnce();
    fireEvent.mouseDown(screen.getByTestId("pop"));
    fireEvent.click(screen.getByTestId("pop"));
    expect(parentClick).not.toHaveBeenCalled();
    expect(parentDown).not.toHaveBeenCalled();
  });

  it("highlights the buttons on hover", () => {
    render(<ConfirmPopover onYes={vi.fn()} onNo={vi.fn()} />);
    const yes = screen.getByTestId("confirm-yes");
    fireEvent.mouseEnter(yes);
    expect(yes.style.color).toBe(css(darkColors.white));
    expect(yes.style.background).toBe(css(darkColors.dangerHoverBg));
    fireEvent.mouseLeave(yes);
    expect(yes.style.color).toBe(css(darkColors.dangerText));
    expect(yes.style.background).toBe(css(darkColors.dangerBg));

    const no = screen.getByTestId("confirm-no");
    fireEvent.mouseEnter(no);
    expect(no.style.color).toBe(css(darkColors.textLight));
    expect(no.style.borderColor).toBe(css(darkColors.textDim));
    fireEvent.mouseLeave(no);
    expect(no.style.color).toBe(css(darkColors.textDim));
    expect(no.style.borderColor).toBe(css(darkColors.border));
  });
});
