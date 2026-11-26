import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import { writeClipboard } from "../../utils/copyToClipboard";
import { CopyButton } from "./CopyButton";

vi.mock("../../utils/copyToClipboard", () => ({ writeClipboard: vi.fn() }));

const writeMock = vi.mocked(writeClipboard);

describe("CopyButton", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    writeMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("is hidden until visible and labelled for copying", () => {
    const { rerender } = render(<CopyButton text="hi" visible={false} />);
    const button = screen.getByRole("button", { name: "Copy to clipboard" });
    expect(button.title).toBe("Copy");
    expect(button.style.opacity).toBe("0");
    expect(button.querySelector("rect")).toBeTruthy();
    rerender(<CopyButton text="hi" visible />);
    expect(button.style.opacity).toBe("1");
  });

  it("copies text and html, shows a check, then reverts", async () => {
    const parentClick = vi.fn();
    render(
      <div onClick={parentClick}>
        <CopyButton text="plain" html="<b>rich</b>" visible={false} testId="copy" />
      </div>,
    );
    const button = screen.getByTestId("copy");
    await act(async () => {
      fireEvent.click(button);
    });
    expect(writeMock).toHaveBeenCalledWith("plain", "<b>rich</b>");
    expect(parentClick).not.toHaveBeenCalled();
    expect(button.title).toBe("Copied");
    expect(button.style.opacity).toBe("1");
    expect(button.querySelector("polyline")).toBeTruthy();

    // Hover doesn't recolour the check.
    fireEvent.mouseEnter(button);
    expect(button.style.color).toBe(css(darkColors.active));
    fireEvent.mouseLeave(button);
    expect(button.style.color).toBe(css(darkColors.active));

    act(() => {
      vi.advanceTimersByTime(1200);
    });
    expect(button.title).toBe("Copy");
    expect(button.querySelector("polyline")).toBeNull();
  });

  it("restarts the revert on a second copy and drops it on unmount", async () => {
    const { unmount } = render(<CopyButton text="x" visible testId="copy" />);
    const button = screen.getByTestId("copy");
    await act(async () => {
      fireEvent.click(button);
    });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    await act(async () => {
      fireEvent.click(button);
    });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(button.title).toBe("Copied");
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(button.title).toBe("Copy");

    await act(async () => {
      fireEvent.click(button);
    });
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("stays put when the clipboard refuses the write", async () => {
    writeMock.mockRejectedValue(new Error("blocked"));
    render(<CopyButton text="x" visible title="Copy link" icon={<span>icon</span>} />);
    const button = screen.getByRole("button", { name: "Copy link" });
    expect(button.textContent).toBe("icon");
    await act(async () => {
      fireEvent.click(button);
    });
    expect(button.title).toBe("Copy link");
    expect(button.textContent).toBe("icon");
  });

  it("lightens on hover", () => {
    render(<CopyButton text="x" visible style={{ marginLeft: 4 }} />);
    const button = screen.getByRole("button");
    expect(button.style.marginLeft).toBe("4px");
    fireEvent.mouseEnter(button);
    expect(button.style.color).toBe(css(darkColors.textLight));
    fireEvent.mouseLeave(button);
    expect(button.style.color).toBe(css(darkColors.textDim));
  });
});
