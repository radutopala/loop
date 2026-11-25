import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fetchReadme } from "../../api/loopApi";
import { ThemeProvider } from "../../ThemeContext";
import { darkColors } from "../../theme";
import type { Channel } from "../../types";
import { buildMarkdownStyles, FilePanel, MarkdownFilePanel } from "./FilePanel";

vi.mock("../../api/loopApi", () => ({ fetchReadme: vi.fn() }));

// The header info fetches commits and PR state; the stub only shows which
// channel the panel handed it.
vi.mock("../layout/ChannelHeaderInfo", () => ({
  ChannelHeaderInfo: ({ channel }: { channel: Channel }) => <div data-testid="header-info">{channel.name}</div>,
}));

const WIDTH_KEY = "loop-file-panel-width";
const channel = { id: "ch1", name: "loop" } as Channel;

const renderPanel = (props: Partial<Parameters<typeof FilePanel>[0]> = {}) => {
  const onClose = vi.fn();
  render(
    <ThemeProvider initialTheme="dark">
      <FilePanel title="Files" dataTestId="panel" onClose={onClose} {...props}>
        <p>body</p>
      </FilePanel>
    </ThemeProvider>,
  );
  return { onClose, panel: screen.getByTestId("panel") };
};

describe("FilePanel", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.spyOn(window, "innerWidth", "get").mockReturnValue(1000);
  });
  afterEach(() => vi.restoreAllMocks());

  it("renders only its children when embedded", () => {
    renderPanel({ embedded: true, noPadding: true });
    const panel = screen.getByTestId("panel");
    expect(screen.getByText("body")).toBeTruthy();
    expect(screen.queryByTitle("Close panel")).toBeNull();
    expect((panel.firstElementChild as HTMLElement).style.overflow).toBe("hidden");
  });

  it("pads the content of an embedded panel that doesn't manage its own layout", () => {
    renderPanel({ embedded: true });
    expect((screen.getByTestId("panel").firstElementChild as HTMLElement).style.padding).toBe("12px 16px");
  });

  it("opens at 60% of the window without a stored width", () => {
    const { panel } = renderPanel();
    expect(panel.style.width).toBe("600px");
  });

  it("opens at the stored width", () => {
    localStorage.setItem(WIDTH_KEY, "420");
    expect(renderPanel().panel.style.width).toBe("420px");
  });

  it("ignores a stored width below the minimum", () => {
    localStorage.setItem(WIDTH_KEY, "100");
    expect(renderPanel().panel.style.width).toBe("600px");
  });

  it("resizes from its left edge, clamped, and remembers the width", () => {
    const { panel } = renderPanel();
    const handle = panel.firstElementChild as HTMLElement;
    fireEvent.mouseDown(handle, { clientX: 500 });
    expect(panel.style.userSelect).toBe("none");
    // Leaving the handle mid-drag keeps it highlighted.
    fireEvent.mouseEnter(handle);
    fireEvent.mouseLeave(handle);
    expect(handle.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseMove(document, { clientX: 450 });
    expect(panel.style.width).toBe("600px");
    fireEvent.mouseMove(document, { clientX: 520 });
    expect(panel.style.width).toBe("580px");
    fireEvent.mouseMove(document, { clientX: 1000 });
    expect(panel.style.width).toBe("280px");
    fireEvent.mouseUp(document);
    expect(localStorage.getItem(WIDTH_KEY)).toBe("280");
    expect(panel.style.userSelect).toBe("");
    fireEvent.mouseMove(document, { clientX: 500 });
    expect(panel.style.width).toBe("280px");
  });

  it("highlights the resize handle on hover", () => {
    const { panel } = renderPanel();
    const handle = panel.firstElementChild as HTMLElement;
    fireEvent.mouseEnter(handle);
    expect(handle.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(handle);
    expect(handle.style.backgroundColor).toBe("transparent");
  });

  it("closes, maximizes and highlights its header buttons", () => {
    const onToggleMaximize = vi.fn();
    const { onClose } = renderPanel({ onToggleMaximize });
    const close = screen.getByTitle("Close panel");
    fireEvent.mouseEnter(close);
    expect(close.style.backgroundColor).not.toBe("transparent");
    fireEvent.mouseLeave(close);
    expect(close.style.backgroundColor).toBe("transparent");
    fireEvent.click(close);
    fireEvent.click(screen.getByTitle("Maximize panel"));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onToggleMaximize).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Files")).toBeTruthy();
  });

  it("fills the window when maximized, with search and channel info", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    const onOpenPalette = vi.fn();
    const { panel } = renderPanel({ maximized: true, channel, onOpenPalette, onToggleMaximize: vi.fn() });
    expect(panel.style.width).toBe("100%");
    expect(screen.getByTitle("Restore panel")).toBeTruthy();
    expect(screen.getByTestId("header-info").textContent).toBe("loop");
    const search = screen.getByTitle("Search messages (Cmd+K)");
    expect(search.textContent).toBe("⌘K");
    fireEvent.click(search);
    expect(onOpenPalette).toHaveBeenCalledTimes(1);
    // No resize handle: the first child is the drag region, padded past the window controls.
    expect((panel.firstElementChild as HTMLElement).style.paddingLeft).toBe("76px");
  });

  it("shows Ctrl+K off macOS and keeps the drag region close to an open sidebar", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    const { panel } = renderPanel({ maximized: true, sidebarOpen: true, onOpenPalette: vi.fn() });
    expect(screen.getByTitle("Search messages (Cmd+K)").textContent).toBe("Ctrl+K");
    expect((panel.firstElementChild as HTMLElement).style.paddingLeft).toBe("4px");
    expect(screen.queryByTestId("header-info")).toBeNull();
  });
});

describe("MarkdownFilePanel", () => {
  const renderReadme = () =>
    render(
      <ThemeProvider initialTheme="dark">
        <MarkdownFilePanel onClose={vi.fn()} />
      </ThemeProvider>,
    );

  beforeEach(() => {
    vi.mocked(fetchReadme).mockReset();
  });

  it("shows the README rendered as markdown", async () => {
    vi.mocked(fetchReadme).mockResolvedValue("# Loop\n\nRuns **agents**");
    renderReadme();
    expect(screen.getByText("Loading...")).toBeTruthy();
    expect((await screen.findByRole("heading", { name: "Loop" })).tagName).toBe("H1");
    expect(screen.getByText("agents").tagName).toBe("STRONG");
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("keeps loading while the README is empty", async () => {
    vi.mocked(fetchReadme).mockResolvedValue("");
    renderReadme();
    await act(async () => {});
    expect(screen.getByText("Loading...")).toBeTruthy();
  });

  it("shows why the README failed to load", async () => {
    vi.mocked(fetchReadme).mockRejectedValue(new Error("not found"));
    renderReadme();
    expect(await screen.findByText("not found")).toBeTruthy();
    expect(screen.queryByText("Loading...")).toBeNull();
  });

  it("shows a generic error for a non-Error failure", async () => {
    vi.mocked(fetchReadme).mockRejectedValue("boom");
    renderReadme();
    expect(await screen.findByText("Failed to load")).toBeTruthy();
  });
});

describe("buildMarkdownStyles", () => {
  it("styles README content with the palette", () => {
    const css = buildMarkdownStyles(darkColors);
    expect(css).toContain(`.readme-content a { color: ${darkColors.active}; text-decoration: none; }`);
    expect(css).toContain(`background: ${darkColors.codeBlockBg};`);
  });
});
