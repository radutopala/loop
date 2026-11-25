import { act, render, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { contentCapBase } from "../api/api";
import { checkFilesExist } from "../api/files";
import { type PluginInfo, pluginXRequest, postPluginNotification } from "../api/plugins";
import { ThemeProvider, useTheme } from "../ThemeContext";
import { loadFrame } from "../test/app-shell-bridge";
import { darkColors, lightColors } from "../theme";
import { writeClipboard } from "../utils/copyToClipboard";
import { pluginTheme } from "./bridge";
import { cardInitialHeight } from "./cardHeight";
import { PluginCard, type PluginCardProps } from "./PluginCard";

vi.mock("../api/api", () => ({ contentCapBase: vi.fn() }));
vi.mock("../api/files", () => ({ checkFilesExist: vi.fn() }));
vi.mock("../api/plugins", () => ({ pluginXRequest: vi.fn(), postPluginNotification: vi.fn() }));
vi.mock("../utils/copyToClipboard", () => ({ writeClipboard: vi.fn() }));
vi.mock("../hooks/useContentCapsEpoch", () => ({ useContentCapsEpoch: () => 0 }));

const capMock = vi.mocked(contentCapBase);
const existsMock = vi.mocked(checkFilesExist);
const requestMock = vi.mocked(pluginXRequest);
const notifyMock = vi.mocked(postPluginNotification);
const copyMock = vi.mocked(writeClipboard);

const cogtest: PluginInfo = {
  id: "cogtest",
  name: "Cognitive tests",
  version: "1",
  capabilities: ["ui.notification.notify", "ui.open_file", "ui.copy"],
  status: "installed",
  settings: { level: 2 },
  cards: [{ id: "score", entry: "ui/score.html" }],
};

const props: PluginCardProps = {
  pluginId: "cogtest",
  cardId: "score",
  label: "Score",
  data: { points: 7 },
  channelId: "ch1",
  channelName: "proj",
  dirPath: "/src/proj",
  plugins: [cogtest],
};

/** Lets a test switch the theme the card renders under. */
function ThemeSwitch({ onReady }: { onReady: (set: (name: string) => void) => void }) {
  const { setThemeName } = useTheme();
  useEffect(() => onReady(setThemeName), [onReady, setThemeName]);
  return null;
}

async function renderFrame(over: Partial<PluginCardProps> = {}) {
  let setTheme!: (name: string) => void;
  const view = render(
    <ThemeProvider initialTheme="dark">
      <ThemeSwitch onReady={(s) => (setTheme = s)} />
      <PluginCard {...props} {...over} />
    </ThemeProvider>,
  );
  const frame = (await screen.findByTestId("plugin-card-frame")) as HTMLIFrameElement;
  return { ...view, frame, setTheme: (name: string) => act(() => setTheme(name)) };
}

describe("PluginCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    capMock.mockResolvedValue("https://content.test/cap/");
  });
  afterEach(() => vi.restoreAllMocks());

  it("renders nothing while the plugins are listed", () => {
    const { container } = render(<PluginCard {...props} plugins={null} />);
    expect(container.innerHTML).toBe("");
    expect(capMock).not.toHaveBeenCalled();
  });

  it.each([
    { name: "a plugin gone", plugins: [] },
    { name: "a card gone", plugins: [{ ...cogtest, cards: [] }] },
    { name: "a plugin off here", plugins: [{ ...cogtest, enabled: false }] },
  ])("says the card isn't available for $name", ({ plugins }) => {
    render(<PluginCard {...props} plugins={plugins} />);
    expect(screen.getByTestId("plugin-card-message").textContent).toBe("This cogtest card isn't available: the plugin may have been removed, not approved for cards or not enabled here.");
    expect(capMock).not.toHaveBeenCalled();
  });

  it("frames the card's page from the plugin's bundle", async () => {
    const { frame } = await renderFrame();
    expect(capMock).toHaveBeenCalledWith({ kind: "plugin", name: "cogtest" });
    expect(frame.getAttribute("src")).toBe("https://content.test/cap/ui/score.html");
    expect(frame.getAttribute("title")).toBe("Score");
    expect(frame.getAttribute("sandbox")).toBe("allow-scripts allow-forms");
    expect(frame.style.height).toBe(`${cardInitialHeight}px`);
    const box = screen.getByTestId("plugin-card");
    expect(box.dataset.plugin).toBe("cogtest");
    expect(box.dataset.card).toBe("score");
  });

  it.each([
    { name: "an error", err: new Error("no link"), want: "no link" },
    { name: "anything else", err: "refused", want: "refused" },
  ])("shows a failed link's $name", async ({ err, want }) => {
    capMock.mockRejectedValue(err);
    render(<PluginCard {...props} />);
    await waitFor(() => expect(screen.getByTestId("plugin-card-message").textContent).toBe(want));
  });

  it("drops a link that arrives after unmounting", async () => {
    let ok!: (b: string) => void;
    let fail!: (e: Error) => void;
    capMock.mockReturnValueOnce(new Promise((r) => (ok = r))).mockReturnValueOnce(new Promise((_, r) => (fail = r)));
    const a = render(<PluginCard {...props} />);
    a.unmount();
    const b = render(<PluginCard {...props} />);
    b.unmount();
    await act(async () => {
      ok("late/");
      fail(new Error("late"));
    });
    expect(screen.queryByTestId("plugin-card")).toBeNull();
  });

  it("answers the page's calls as the card", async () => {
    requestMock.mockResolvedValue({ status: 200, body: { ok: true } } as never);
    notifyMock.mockResolvedValue();
    copyMock.mockResolvedValue();
    const { frame } = await renderFrame();
    const page = loadFrame(frame);

    expect(await page.call("context.get")).toMatchObject({
      result: { pluginId: "cogtest", panel: "", channelId: "ch1", channelName: "proj", dirPath: "/src/proj", card: "score", data: { points: 7 } },
    });
    expect(await page.call("api.request", { method: "GET", path: "scores", body: 1 })).toMatchObject({ result: { status: 200, body: { ok: true } } });
    expect(requestMock).toHaveBeenCalledWith("cogtest", "ch1", "GET", "scores", 1);
    expect(await page.call("theme.get")).toMatchObject({ result: pluginTheme(darkColors) });
    expect(await page.call("settings.get")).toMatchObject({ result: { level: 2 } });
    await page.call("ui.notification.notify", { title: "Done", body: "7 points" });
    expect(notifyMock).toHaveBeenCalledWith("cogtest", "ch1", "Done", "7 points");
    await page.call("ui.copy", { text: "7" });
    expect(copyMock).toHaveBeenCalledWith("7");
    expect(await page.call("state.get")).toMatchObject({ result: null });
    await page.call("state.set", { value: { round: 2 } });
    expect(await page.call("state.get")).toMatchObject({ result: { round: 2 } });
    await page.call("ui.resize", { height: 333.2 });
    await waitFor(() => expect(frame.style.height).toBe("334px"));
    page.close();
  });

  it("answers with empty settings for a plugin without any", async () => {
    const { frame } = await renderFrame({ plugins: [{ ...cogtest, settings: undefined }] });
    const page = loadFrame(frame);
    expect(await page.call("settings.get")).toMatchObject({ result: {} });
    page.close();
  });

  it("opens a file of the channel the page names", async () => {
    const opened = vi.fn();
    window.addEventListener("loop:open-file", opened);
    existsMock.mockResolvedValue([{ path: "a.ts", exists: true, root_index: 1, rel_path: "src/a.ts" }] as never);
    const { frame } = await renderFrame();
    const page = loadFrame(frame);
    expect(await page.call("ui.open_file", { path: "a.ts", line: 3 })).toMatchObject({ result: null });
    expect(existsMock).toHaveBeenCalledWith("ch1", ["a.ts"]);
    expect((opened.mock.calls[0]![0] as CustomEvent).detail).toEqual({ channelId: "ch1", target: { rootIndex: 1, relPath: "src/a.ts" }, line: 3 });
    await page.call("ui.open_file", { path: "a.ts" });
    expect((opened.mock.calls[1]![0] as CustomEvent).detail.line).toBeNull();
    window.removeEventListener("loop:open-file", opened);
    page.close();
  });

  it.each([
    { name: "no answer", files: [] },
    { name: "a missing file", files: [{ path: "a.ts", exists: false }] },
    { name: "no root", files: [{ path: "a.ts", exists: true, rel_path: "a.ts" }] },
    { name: "no relative path", files: [{ path: "a.ts", exists: true, root_index: 0 }] },
  ])("fails to open a file for $name", async ({ files }) => {
    existsMock.mockResolvedValue(files as never);
    const { frame } = await renderFrame();
    const page = loadFrame(frame);
    expect(await page.call("ui.open_file", { path: "a.ts" })).toMatchObject({ error: { message: 'no file "a.ts" in the channel' } });
    page.close();
  });

  it("tells the page of a new theme", async () => {
    const { frame, setTheme } = await renderFrame();
    const page = loadFrame(frame);
    const theme = page.next("theme");
    setTheme("light");
    expect(await theme).toEqual(pluginTheme(lightColors));
    page.close();
  });

  it("answers with the last host after the card goes, and connects again on reload", async () => {
    const { frame, rerender } = await renderFrame();
    const first = loadFrame(frame);
    const second = loadFrame(frame);
    rerender(
      <ThemeProvider initialTheme="dark">
        <ThemeSwitch onReady={() => {}} />
        <PluginCard {...props} plugins={[]} />
      </ThemeProvider>,
    );
    expect(screen.getByTestId("plugin-card-message")).toBeTruthy();
    expect(await second.call("settings.get")).toMatchObject({ result: { level: 2 } });
    // The first bridge closed as the frame loaded again: nobody answers it.
    const unanswered = await Promise.race([first.call("settings.get"), new Promise((r) => setTimeout(() => r("none"), 20))]);
    expect(unanswered).toBe("none");
    first.close();
    second.close();
  });

  it("connects nothing to a frame without a window", async () => {
    const { frame } = await renderFrame();
    vi.spyOn(frame, "contentWindow", "get").mockReturnValue(null);
    expect(() => act(() => frame.dispatchEvent(new Event("load")))).not.toThrow();
  });
});
