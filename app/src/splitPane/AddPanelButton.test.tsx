import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PluginInfo } from "../api/plugins";
import { PluginPanelsProvider } from "../plugins/PluginPanelsContext";
import { darkColors } from "../theme";
import { PANEL_OPTIONS } from "../types/panels";
import { EmptyLayoutPicker } from "./AddPanelButton";

const plugins: PluginInfo[] = [
  { id: "cogtest", name: "Cognitive tests", version: "1", capabilities: [], status: "installed", panels: [{ id: "quiz", label: "Quiz", entry: "q.html", singleton: true }] },
];

describe("EmptyLayoutPicker", () => {
  it("offers every panel with its icon and description", () => {
    render(<EmptyLayoutPicker onAdd={vi.fn()} />);
    for (const { panel } of PANEL_OPTIONS.filter((o) => o.panel !== "docker-agent" && o.panel !== "chat-side")) {
      const btn = screen.getByTestId(`empty-layout-add-${panel}`);
      expect(btn.querySelector("svg")).not.toBeNull();
    }
    expect(screen.getByTestId("empty-layout-add-git").textContent).toBe("GitGit diff viewer");
    // An open-mode entry shows its mode's own description.
    expect(screen.getByTestId("empty-layout-add-docker-agent-fresh").textContent).toContain("Fresh");
  });

  it("adds the clicked panel with its open mode", async () => {
    const onAdd = vi.fn();
    const user = userEvent.setup();
    render(<EmptyLayoutPicker onAdd={onAdd} />);
    await user.click(screen.getByTestId("empty-layout-add-notes"));
    expect(onAdd).toHaveBeenLastCalledWith("notes", undefined);
    await user.click(screen.getByTestId("empty-layout-add-docker-agent-fork"));
    expect(onAdd).toHaveBeenLastCalledWith("docker-agent", { openMode: "fork" });
  });

  it("leaves out hidden panels and lists plugin panels", async () => {
    const onAdd = vi.fn();
    const user = userEvent.setup();
    render(
      <PluginPanelsProvider value={{ plugins, shown: new Set() }}>
        <EmptyLayoutPicker onAdd={onAdd} hiddenPanels={["git"]} />
      </PluginPanelsProvider>,
    );
    expect(screen.queryByTestId("empty-layout-add-git")).toBeNull();
    const quiz = screen.getByTestId("empty-layout-add-plugin-cogtest/quiz");
    expect(quiz.textContent).toBe("QuizCognitive tests plugin");
    await user.click(quiz);
    expect(onAdd).toHaveBeenCalledWith("plugin", { plugin: { id: "cogtest", panel: "quiz", label: "Quiz" } });
  });

  it("highlights a button under the mouse", () => {
    render(<EmptyLayoutPicker onAdd={vi.fn()} />);
    const btn = screen.getByTestId("empty-layout-add-notes");
    fireEvent.mouseEnter(btn);
    expect(btn.style.color).toBe(hex(darkColors.textLight));
    fireEvent.mouseLeave(btn);
    expect(btn.style.color).toBe(hex(darkColors.textDim));
    expect(btn.style.backgroundColor).toBe("transparent");
  });
});

/** The color jsdom reports back for a CSS color. */
function hex(color: string) {
  const el = document.createElement("span");
  el.style.color = color;
  return el.style.color;
}
