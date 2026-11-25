import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ComposerSwitch } from "../../api/composerSwitches";
import { ComposerSwitchesContext, type ComposerSwitchesView } from "../../hooks/useComposerSwitches";
import { ComposerSwitchPills } from "./ComposerSwitchPills";

function view(switches: ComposerSwitch[], over: Partial<ComposerSwitchesView> = {}): ComposerSwitchesView {
  return { switches, pending: new Set(), errors: new Map(), turn: vi.fn().mockResolvedValue(undefined), ...over };
}

function renderPills(v: ComposerSwitchesView | null) {
  return render(
    <ComposerSwitchesContext.Provider value={v}>
      <ComposerSwitchPills />
    </ComposerSwitchesContext.Provider>,
  );
}

const sw = (over: Partial<ComposerSwitch> = {}): ComposerSwitch => ({ plugin_id: "p", switch_id: "s", on: false, ...over });

describe("ComposerSwitchPills", () => {
  it("renders nothing without a view or switches", () => {
    const { container, rerender } = renderPills(null);
    expect(container.innerHTML).toBe("");
    rerender(
      <ComposerSwitchesContext.Provider value={view([])}>
        <ComposerSwitchPills />
      </ComposerSwitchesContext.Provider>,
    );
    expect(container.innerHTML).toBe("");
  });

  it("draws a switch without an icon as its label's initial and turns it on click", async () => {
    const s = sw({ label: "focus" });
    const v = view([s]);
    renderPills(v);
    const btn = screen.getByRole("button", { name: "focus" });
    expect(btn.textContent).toBe("F");
    expect(btn.getAttribute("data-look")).toBe("off");
    expect(btn.getAttribute("aria-pressed")).toBe("false");
    expect(btn.style.border).toContain("solid");
    await userEvent.click(btn);
    expect(v.turn).toHaveBeenCalledWith(s);
  });

  it("falls back to the switch id for a switch without a label", () => {
    renderPills(view([sw({ switch_id: "zen", on: true })]));
    const btn = screen.getByRole("button", { name: "zen" });
    expect(btn.textContent).toBe("Z");
    expect(btn.getAttribute("data-look")).toBe("on");
  });

  it("masks the plugin's icon in the pill's color", () => {
    renderPills(view([sw({ icon: "data:image/svg+xml,x", on: true })]));
    const btn = screen.getByTestId("composer-switch-p/s");
    const icon = btn.querySelector("span")!;
    expect(icon.getAttribute("aria-hidden")).toBe("true");
    expect(icon.style.maskImage).toContain("data:image/svg+xml,x");
  });

  it("disables a switch whose click is pending, dashed", () => {
    renderPills(view([sw()], { pending: new Set(["p/s"]) }));
    const btn = screen.getByTestId("composer-switch-p/s") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("data-look")).toBe("busy");
    expect(btn.style.border).toContain("dashed");
    expect(btn.style.cursor).toBe("progress");
  });

  it("shows a busy switch that's on in the accent", () => {
    renderPills(view([sw({ on: false, status: "busy" }), sw({ switch_id: "t", on: true, status: "busy" })]));
    const off = screen.getByTestId("composer-switch-p/s");
    const on = screen.getByTestId("composer-switch-p/t");
    expect(off.getAttribute("data-look")).toBe("busy");
    expect(on.getAttribute("data-look")).toBe("busy");
    expect(off.style.color).not.toBe(on.style.color);
  });

  it("shows a failed click's error in its tooltip", async () => {
    renderPills(view([sw({ label: "focus" })], { errors: new Map([["p/s", "plugin crashed"]]) }));
    const btn = screen.getByRole("button", { name: "focus" });
    expect(btn.getAttribute("data-look")).toBe("error");
    await userEvent.hover(btn);
    expect((await screen.findByRole("tooltip")).textContent).toContain("plugin crashed");
  });
});
