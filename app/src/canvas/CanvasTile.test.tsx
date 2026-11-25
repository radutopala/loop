import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { PluginInfo } from "../api/plugins";
import type { AgentInfo } from "../hooks/useAgentRegistry";
import { PluginPanelsProvider } from "../plugins/PluginPanelsContext";
import { darkColors } from "../theme";
import type { LeafNode, PanelType } from "../types/panels";
import { CanvasTile } from "./CanvasTile";
import type { CanvasTile as CanvasTileType } from "./types";

const base: CanvasTileType = { id: "notes-1", panel: "notes", x: 100, y: 50, width: 400, height: 300, zIndex: 2 };

function renderTile(props: Partial<React.ComponentProps<typeof CanvasTile>> = {}, plugins: PluginInfo[] = []) {
  const handlers = {
    renderLeaf: vi.fn((leaf: LeafNode) => <div>content of {leaf.id}</div>),
    onMove: vi.fn(),
    onResize: vi.fn(),
    onBringToFront: vi.fn(),
    onClose: vi.fn(),
    onAddTile: vi.fn(),
  };
  render(
    <PluginPanelsProvider value={{ plugins, shown: new Set() }}>
      <CanvasTile tile={base} {...handlers} {...props} />
    </PluginPanelsProvider>,
  );
  return handlers;
}

/** The tile's outer box, from its label in the header. */
function tileBox(label: string): HTMLElement {
  return header(label).parentElement as HTMLElement;
}

function header(label: string): HTMLElement {
  return screen.getByText(label).parentElement as HTMLElement;
}

/** A color as jsdom normalizes it in a style. */
function css(color: string): string {
  const el = document.createElement("div");
  el.style.backgroundColor = color;
  return el.style.backgroundColor;
}

const agent = (status: string): AgentInfo => ({ agent_id: "a", channel_id: "c", name: "Builder", status, work_summary: "" });

describe("CanvasTile", () => {
  it("places the tile and renders its leaf", () => {
    const { renderLeaf } = renderTile();
    const box = tileBox("Notes");
    expect(box.style.left).toBe("100px");
    expect(box.style.top).toBe("50px");
    expect(box.style.width).toBe("400px");
    expect(box.style.height).toBe("300px");
    expect(box.style.zIndex).toBe("2");
    expect(screen.getByText("content of notes-1")).toBeTruthy();
    expect(renderLeaf).toHaveBeenCalledWith({ type: "leaf", id: "notes-1", panel: "notes", flex: 1 });
    expect(screen.queryByTitle("Maximize tile")).toBeNull();
    expect(screen.queryByTestId("container-stats")).toBeNull();
    expect(document.getElementById("pane-header-slot-notes-1")).not.toBeNull();
  });

  it("passes a side chat's open mode and a plugin's ref to its leaf, labelled by them", () => {
    const { renderLeaf } = renderTile({ tile: { ...base, id: "cs", panel: "chat-side", openMode: "fresh" } });
    expect(screen.getByText("Chat · fresh")).toBeTruthy();
    expect(renderLeaf).toHaveBeenLastCalledWith({ type: "leaf", id: "cs", panel: "chat-side", flex: 1, openMode: "fresh" });
  });

  it("labels a plugin tile with its panel's label", () => {
    const plugin = { id: "p", panel: "board", label: "Board" };
    const { renderLeaf } = renderTile({ tile: { ...base, id: "pl", panel: "plugin", plugin } });
    expect(screen.getByText("Board")).toBeTruthy();
    expect(renderLeaf).toHaveBeenLastCalledWith({ type: "leaf", id: "pl", panel: "plugin", flex: 1, plugin });
  });

  it.each([
    { status: "running", color: darkColors.active },
    { status: "error", color: darkColors.error },
    { status: "idle", color: darkColors.textDim },
  ])("labels an agent tile with its name and a $status dot", ({ status, color }) => {
    renderTile({ tile: { ...base, id: "docker-agent-1", panel: "docker-agent" }, agentInfo: agent(status) });
    const dot = header("Builder").firstElementChild as HTMLElement;
    expect(dot.style.backgroundColor).toBe(css(color));
  });

  it("labels an agent tile it has no info for with its id, without a dot", () => {
    renderTile({ tile: { ...base, id: "docker-agent-1", panel: "docker-agent" } });
    expect(header("docker-agent-1").firstElementChild?.textContent).toBe("docker-agent-1");
  });

  it("shows the backing container's stats", () => {
    renderTile({
      tile: { ...base, id: "chat", panel: "chat" },
      containerStats: { agent: { container_id: "abcdef1234567890", type: "agent", cpu_percent: 12.4, mem_usage: 1024, mem_limit: 2048 } },
    });
    expect(screen.getByTestId("container-stats").textContent).toContain("12%");
  });

  it("brings itself to the front on any mousedown in it", () => {
    const { onBringToFront } = renderTile();
    fireEvent.mouseDown(screen.getByText("content of notes-1"));
    expect(onBringToFront).toHaveBeenCalledWith("notes-1");
  });

  it("reports drags of its header in world units, raised while dragged", () => {
    const { onMove, onBringToFront } = renderTile({ zoom: 2 });
    const box = tileBox("Notes");
    const resting = box.style.boxShadow;
    fireEvent.mouseDown(header("Notes"), { clientX: 10, clientY: 10 });
    expect(onBringToFront).toHaveBeenCalledWith("notes-1");
    expect(box.style.boxShadow).not.toBe(resting);
    expect(box.style.transition).toBe("none");
    fireEvent.mouseMove(document, { clientX: 30, clientY: 50 });
    fireEvent.mouseMove(document, { clientX: 32, clientY: 50 });
    expect(onMove.mock.calls).toEqual([
      ["notes-1", 10, 20],
      ["notes-1", 1, 0],
    ]);
    fireEvent.mouseUp(document);
    expect(box.style.boxShadow).toBe(resting);
    fireEvent.mouseMove(document, { clientX: 100, clientY: 100 });
    expect(onMove).toHaveBeenCalledTimes(2);
  });

  it("resizes from its corner, not below the minimum", () => {
    const { onResize, onBringToFront } = renderTile({ zoom: 2 });
    const box = tileBox("Notes");
    const corner = box.lastElementChild as HTMLElement;
    fireEvent.mouseDown(corner, { clientX: 0, clientY: 0 });
    expect(onBringToFront).toHaveBeenCalledWith("notes-1");
    expect(box.style.transition).toBe("none");
    fireEvent.mouseMove(document, { clientX: 40, clientY: 20 });
    expect(onResize).toHaveBeenLastCalledWith("notes-1", 420, 310);
    fireEvent.mouseMove(document, { clientX: -2000, clientY: -2000 });
    expect(onResize).toHaveBeenLastCalledWith("notes-1", 200, 120);
    fireEvent.mouseUp(document);
    expect(box.style.transition).not.toBe("none");
    fireEvent.mouseMove(document, { clientX: 0, clientY: 0 });
    expect(onResize).toHaveBeenCalledTimes(2);
  });

  it("closes itself", () => {
    const { onClose } = renderTile();
    fireEvent.click(screen.getByTitle("Close tile"));
    expect(onClose).toHaveBeenCalledWith("notes-1");
  });

  it("toggles maximize, titled by its state", () => {
    const onToggleMaximize = vi.fn();
    renderTile({ onToggleMaximize });
    const btn = screen.getByTitle("Maximize tile");
    fireEvent.mouseEnter(btn);
    expect(btn.style.color).toBe(css(darkColors.textLight));
    fireEvent.mouseLeave(btn);
    expect(btn.style.color).toBe(css(darkColors.textDim));
    fireEvent.click(btn);
    expect(onToggleMaximize).toHaveBeenCalledWith("notes-1");
  });

  it("offers restoring when maximized", () => {
    renderTile({ onToggleMaximize: vi.fn(), isMaximized: true });
    expect(screen.getByTitle("Restore tile")).toBeTruthy();
  });

  it("adds a tile right of itself from its add menu, greying out used singletons", () => {
    const plugins: PluginInfo[] = [{ id: "p", name: "Pl", version: "1", capabilities: [], status: "running", panels: [{ id: "board", label: "Board", entry: "index.html" }] }];
    const { onAddTile } = renderTile({ usedSingletons: new Set<PanelType>(["git"]), hiddenPanels: ["memory"] }, plugins);
    const add = screen.getByTitle("Add panel");
    fireEvent.mouseEnter(add);
    expect(add.style.color).toBe(css(darkColors.textLight));
    fireEvent.mouseLeave(add);
    expect(add.style.color).toBe(css(darkColors.textDim));
    fireEvent.click(add);
    expect(screen.queryByRole("button", { name: "Memory" })).toBeNull();
    const git = screen.getByRole("button", { name: "Git" }) as HTMLButtonElement;
    expect(git.disabled).toBe(true);
    fireEvent.mouseEnter(git);
    expect(git.style.backgroundColor).toBe("transparent");
    fireEvent.click(git);
    expect(onAddTile).not.toHaveBeenCalled();

    const editor = screen.getByRole("button", { name: "Editor" });
    fireEvent.mouseEnter(editor);
    expect(editor.style.backgroundColor).toBe("rgba(255, 255, 255, 0.08)");
    fireEvent.mouseLeave(editor);
    expect(editor.style.backgroundColor).toBe("transparent");

    fireEvent.click(screen.getByRole("button", { name: "Chat (Fresh session)" }));
    expect(onAddTile).toHaveBeenCalledWith("chat-side", { x: 520, y: 50 }, { openMode: "fresh" });
    expect(screen.queryByRole("button", { name: "Editor" })).toBeNull();

    fireEvent.click(add);
    fireEvent.click(screen.getByRole("button", { name: "Board" }));
    expect(onAddTile).toHaveBeenLastCalledWith("plugin", { x: 520, y: 50 }, { plugin: { id: "p", panel: "board", label: "Board" } });
  });

  it("closes its add menu on the add button, or a mousedown outside it, not inside it", () => {
    const { onBringToFront } = renderTile();
    const add = screen.getByTitle("Add panel");
    fireEvent.click(add);
    fireEvent.click(add);
    expect(screen.queryByRole("button", { name: "Editor" })).toBeNull();

    fireEvent.click(add);
    onBringToFront.mockClear();
    fireEvent.mouseDown(screen.getByRole("button", { name: "Editor" }));
    expect(screen.getByRole("button", { name: "Editor" })).toBeTruthy();
    fireEvent.mouseDown(add);
    expect(screen.getByRole("button", { name: "Editor" })).toBeTruthy();
    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("button", { name: "Editor" })).toBeNull();
  });
});
