import { EditorView } from "@codemirror/view";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createBackupKey, fetchBackupStatus } from "../../api/backup";
import { restoreBuiltins } from "../../api/builtins";
import {
  type ConfigResponse,
  type ConfigSchema,
  fetchConfigHistory,
  fetchConfigHistoryFiles,
  fetchConfigRevision,
  fetchConfigSchema,
  fetchGlobalConfig,
  fetchProjectConfig,
  fetchProjectTrust,
  restoreConfigRevision,
  saveGlobalConfig,
  saveProjectConfig,
  trustProjectConfig,
} from "../../api/configApi";
import { fetchPR } from "../../api/git";
import { getImageStatus, getReclaimable, reclaimDockerSpace } from "../../api/loopApi";
import { fetchPluginList, PLUGINS_CHANGED, setPluginEnabled } from "../../api/plugins";
import { DEFAULT_FONT_SIZES } from "../../ThemeContext";
import type { Channel, DaemonInfo, DockerReclaimable, ImageStatusResponse } from "../../types";
import { Settings } from "./Settings";

vi.mock("../../api/configApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/configApi")>()),
  fetchConfigSchema: vi.fn(),
  fetchGlobalConfig: vi.fn(),
  fetchProjectConfig: vi.fn(),
  saveGlobalConfig: vi.fn(),
  saveProjectConfig: vi.fn(),
  fetchConfigHistory: vi.fn(),
  fetchConfigHistoryFiles: vi.fn(),
  fetchConfigRevision: vi.fn(),
  restoreConfigRevision: vi.fn(),
  fetchProjectTrust: vi.fn(),
  trustProjectConfig: vi.fn(),
}));
vi.mock("../../api/loopApi", () => ({ getImageStatus: vi.fn(), getReclaimable: vi.fn(), reclaimDockerSpace: vi.fn() }));
vi.mock("../../api/builtins", () => ({ restoreBuiltins: vi.fn() }));
vi.mock("../../api/backup", () => ({ fetchBackupStatus: vi.fn(), createBackupKey: vi.fn(), startBackup: vi.fn() }));
vi.mock("../../api/git", () => ({ fetchCommits: vi.fn(), fetchPR: vi.fn() }));
vi.mock("../../api/pr", () => ({ getPRStatus: vi.fn() }));
vi.mock("../../api/plugins", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/plugins")>()),
  fetchPluginList: vi.fn(),
  setPluginEnabled: vi.fn(),
}));
// The plugin sections listen to the daemon's event stream, a WebSocket.
vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

// Settings applies a saved config's desktop settings through the theme
// context's setters.
const theme = vi.hoisted(() => ({ setThemeName: vi.fn(), setFontSizes: vi.fn(), setIslands: vi.fn(), setHiddenPills: vi.fn() }));
vi.mock("../../ThemeContext", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../ThemeContext")>();
  const { darkColors: colors } = await import("../../theme");
  const value = { themeName: "dark", colors, availableThemes: ["dark"], fontSizes: mod.DEFAULT_FONT_SIZES, islands: false, hiddenPills: [], ...theme };
  return { ...mod, useTheme: () => value };
});

const schemaMock = vi.mocked(fetchConfigSchema);
const globalMock = vi.mocked(fetchGlobalConfig);
const projectMock = vi.mocked(fetchProjectConfig);
const saveGlobalMock = vi.mocked(saveGlobalConfig);
const saveProjectMock = vi.mocked(saveProjectConfig);
const historyMock = vi.mocked(fetchConfigHistory);
const historyFilesMock = vi.mocked(fetchConfigHistoryFiles);
const revisionMock = vi.mocked(fetchConfigRevision);
const restoreRevisionMock = vi.mocked(restoreConfigRevision);
const trustMock = vi.mocked(fetchProjectTrust);
const trustConfigMock = vi.mocked(trustProjectConfig);
const imageMock = vi.mocked(getImageStatus);
const reclaimableMock = vi.mocked(getReclaimable);
const reclaimMock = vi.mocked(reclaimDockerSpace);
const builtinsMock = vi.mocked(restoreBuiltins);
const backupMock = vi.mocked(fetchBackupStatus);
const backupKeyMock = vi.mocked(createBackupKey);
const prMock = vi.mocked(fetchPR);
const pluginListMock = vi.mocked(fetchPluginList);
const pluginEnabledMock = vi.mocked(setPluginEnabled);

const schema: ConfigSchema = {
  type: "object",
  properties: {
    desktop: { type: "object", "x-section": "Desktop", "x-global-only": true, properties: { note: { type: "string", title: "Note" } } },
    model: { type: "string", title: "Model", "x-section": "Agent", "x-global-only": true },
    workflows: { type: "string", "x-section": "Workflows", "x-global-only": true },
    shortcuts: { type: "string", "x-section": "Prompt Shortcuts", "x-global-only": true },
    backup: { type: "string", "x-section": "Backup", "x-global-only": true },
    plugins: { type: "object", "x-section": "Plugins", "x-global-only": true, properties: { enabled: { type: "boolean" } } },
    workspace: { type: "string", title: "Workspace dir", "x-section": "Workspace", "x-project-only": true },
  },
};

const cfg = (content: Record<string, unknown> | null, path = "/cfg/config.json"): ConfigResponse => ({ path, content, raw: JSON.stringify(content) });
const globalCfg = cfg({ model: "opus", workflows: "w1" });
const projectCfg = cfg({ workspace: "/work" }, "/work/.loop/config.json");

const estimate: DockerReclaimable = {
  volumes_sized: true,
  build_cache: 1024,
  dangling_images: 0,
  unused_images: 2048,
  unused_image_tags: ["old:1", "old:2"],
  anonymous_volumes: 0,
  orphan_volumes: 0,
  orphan_volume_names: [],
};

const imageStatus: ImageStatusResponse = {
  status: { state: "idle" },
  versions: { loop_version: "1.0", claude_version: "", built_at: "2026-01-01" },
};

const channel = { id: "ch-1", name: "general", dir_path: "/work", display_path: "~/work", branch: "", parent_id: "" } as unknown as Channel;

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

type Props = Partial<Parameters<typeof Settings>[0]>;

function renderSettings(props: Props = {}) {
  const onClose = vi.fn();
  const user = userEvent.setup();
  const view = render(<Settings open onClose={onClose} {...props} />);
  return { onClose, user, ...view };
}

const loaded = () => screen.findByTestId("settings-search");
const nav = (name: string, i = 0) => screen.getAllByTestId(`settings-nav-${name}`)[i]!;
// The JSON editor mounts its CodeMirror view after the config loads.
async function jsonEditor() {
  const host = await screen.findByTestId("config-json-editor");
  const el = await waitFor(() => {
    const found = host.querySelector<HTMLElement>(".cm-editor");
    if (!found) throw new Error("no editor yet");
    return found;
  });
  return EditorView.findFromDOM(el)!;
}
const field = (key: string) => document.querySelector<HTMLInputElement>(`[data-config-key="${key}"] input`)!;

function stubLoopAPI(api: Partial<Window["loopAPI"]> | undefined) {
  vi.stubGlobal("loopAPI", api);
}

describe("Settings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubLoopAPI(undefined);
    schemaMock.mockResolvedValue(schema);
    globalMock.mockResolvedValue(globalCfg);
    projectMock.mockResolvedValue(projectCfg);
    saveGlobalMock.mockResolvedValue(undefined);
    saveProjectMock.mockResolvedValue(undefined);
    imageMock.mockResolvedValue(imageStatus);
    reclaimableMock.mockResolvedValue(estimate);
    historyMock.mockResolvedValue({
      path: "/cfg/config.json",
      revisions: [
        { id: 2, source: "settings", created_at: "2026-01-02T00:00:00Z", added: 1, removed: 0 },
        { id: 1, source: "initial", created_at: "2026-01-01T00:00:00Z", added: 3, removed: 0 },
      ],
    } as never);
    historyFilesMock.mockResolvedValue({ files: [] } as never);
    revisionMock.mockResolvedValue({ id: 1, path: "/cfg/config.json", content: "{}", hash: "h1", source: "initial", created_at: "2026-01-01T00:00:00Z", diff: "" } as never);
    restoreRevisionMock.mockResolvedValue(undefined);
    trustMock.mockResolvedValue({ trusted: true, current: "", approved: "", diff: "" } as never);
    trustConfigMock.mockResolvedValue(undefined as never);
    prMock.mockResolvedValue({ present: false } as never);
    pluginListMock.mockResolvedValue({ off: false, plugins: [] });
    pluginEnabledMock.mockResolvedValue(undefined);
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  describe("opening", () => {
    it("renders nothing while closed and loads nothing", () => {
      const { container } = render(<Settings open={false} onClose={vi.fn()} channelId="ch-1" />);
      expect(container.innerHTML).toBe("");
      expect(schemaMock).not.toHaveBeenCalled();
      expect(projectMock).not.toHaveBeenCalled();
    });

    it("shows Loading until the config arrives, then the Desktop section", async () => {
      const d = deferred<ConfigSchema>();
      schemaMock.mockReturnValue(d.promise);
      renderSettings();
      expect(screen.getByText("Loading...")).toBeTruthy();
      await act(async () => d.resolve(schema));
      expect(field("desktop.note")).toBeTruthy();
      expect(nav("desktop").style.fontWeight).toBe("600");
      expect(nav("agent").style.fontWeight).toBe("400");
    });

    it("lists the global sections and, for a project, its own", async () => {
      renderSettings({ projectDirPath: "/work", channelId: "ch-1" });
      await loaded();
      for (const n of ["daemon", "docker-image", "containers", "desktop", "agent", "workflows", "prompt-shortcuts", "backup", "plugins", "json", "history"]) expect(nav(n)).toBeTruthy();
      expect(nav("workspace")).toBeTruthy();
      expect(screen.getAllByTestId("settings-nav-plugins")).toHaveLength(2);
      expect(screen.getAllByTestId("settings-nav-json")).toHaveLength(2);
      expect(screen.getByText("Project")).toBeTruthy();
    });

    it("leaves out the project's Plugins without a channel, and JSON and History without a global config", async () => {
      globalMock.mockRejectedValue(new Error("down"));
      schemaMock.mockRejectedValue(new Error("down"));
      imageMock.mockRejectedValue(new Error("down"));
      renderSettings({ projectDirPath: "/work", initialSection: "__global_json__" });
      await loaded();
      expect(screen.queryByTestId("settings-nav-json")).toBeNull();
      expect(screen.queryByText("Project")).toBeNull();
      expect(screen.queryByTestId("config-json-editor")).toBeNull();
    });

    it("leaves out the project group without project sections", async () => {
      renderSettings({ projectDirPath: "/work", channelId: "ch-1" });
      await loaded();
      expect(screen.getAllByTestId("settings-nav-plugins")).toHaveLength(2);
      schemaMock.mockResolvedValue({ type: "object", properties: { model: schema.properties.model! } });
    });

    it("still opens when the daemon info can't be read", async () => {
      stubLoopAPI({ getDaemonInfo: () => Promise.reject(new Error("no bridge")) });
      renderSettings({ initialSection: "Daemon" });
      expect(await screen.findByRole("button", { name: /Restart Daemon/ })).toBeTruthy();
    });

    it("reports whether the config is dirty", async () => {
      const onConfigDirtyChange = vi.fn();
      const { user } = renderSettings({ onConfigDirtyChange });
      await loaded();
      expect(onConfigDirtyChange).toHaveBeenLastCalledWith(false);
      await user.type(field("desktop.note"), "x");
      expect(onConfigDirtyChange).toHaveBeenLastCalledWith(true);
    });
  });

  describe("header", () => {
    it("opens the palette with the platform's shortcut and shows the channel", async () => {
      vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
      const onOpenPalette = vi.fn();
      const { user } = renderSettings({ onOpenPalette, channel, sidebarOpen: false });
      await loaded();
      expect(screen.getByText("⌘K")).toBeTruthy();
      expect(screen.getByTestId("header-info").textContent).toContain("~/work");
      await user.click(screen.getByTitle("Search messages (Cmd+K)"));
      expect(onOpenPalette).toHaveBeenCalledOnce();
    });

    it("shows Ctrl+K off a Mac, and no palette button or channel without them", async () => {
      vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
      const { unmount } = renderSettings({ onOpenPalette: vi.fn() });
      await loaded();
      expect(screen.getByText("Ctrl+K")).toBeTruthy();
      unmount();
      renderSettings();
      await loaded();
      expect(screen.queryByTitle("Search messages (Cmd+K)")).toBeNull();
      expect(screen.queryByTestId("header-info")).toBeNull();
    });

    it("highlights the close button on hover and closes a clean panel", async () => {
      const { user, onClose } = renderSettings();
      await loaded();
      const close = screen.getByTitle("Close panel");
      fireEvent.mouseEnter(close);
      expect(close.style.backgroundColor).not.toBe("transparent");
      fireEvent.mouseLeave(close);
      expect(close.style.backgroundColor).toBe("transparent");
      await user.click(close);
      expect(onClose).toHaveBeenCalledOnce();
    });

    it("closes on Escape, ignoring other keys", async () => {
      const { onClose } = renderSettings();
      await loaded();
      fireEvent.keyDown(window, { key: "a" });
      expect(onClose).not.toHaveBeenCalled();
      fireEvent.keyDown(window, { key: "Escape" });
      expect(onClose).toHaveBeenCalledOnce();
    });
  });

  describe("unsaved changes", () => {
    it("asks before closing a dirty panel, and keeps it open on Cancel or a click outside", async () => {
      const { user, onClose } = renderSettings();
      await loaded();
      await user.type(field("desktop.note"), "x");
      fireEvent.keyDown(window, { key: "Escape" });
      expect(screen.getByText("Unsaved Changes")).toBeTruthy();
      await user.click(screen.getByText("You have unsaved config changes. Discard them and close?"));
      expect(screen.getByText("Unsaved Changes")).toBeTruthy();
      const modal = screen.getByText("Unsaved Changes").parentElement!;
      await user.click(within(modal).getByRole("button", { name: "Cancel" }));
      expect(screen.queryByText("Unsaved Changes")).toBeNull();
      await user.click(screen.getByTitle("Close panel"));
      await user.click(modal.parentElement ? screen.getByText("Unsaved Changes").parentElement!.parentElement! : modal);
      expect(screen.queryByText("Unsaved Changes")).toBeNull();
      expect(onClose).not.toHaveBeenCalled();
    });

    it("discards the edits and closes", async () => {
      const { user, onClose } = renderSettings();
      await loaded();
      await user.type(field("desktop.note"), "x");
      await user.click(screen.getByTitle("Close panel"));
      await user.click(screen.getByRole("button", { name: "Discard & Close" }));
      expect(onClose).toHaveBeenCalledOnce();
      expect(field("desktop.note").value).toBe("");
    });

    it("cancels the edits from the floating bar", async () => {
      const { user } = renderSettings({ initialSection: "Agent" });
      await loaded();
      await user.type(field("model"), "-x");
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      expect(field("model").value).toBe("opus");
      expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    });

    it("saves the global edits, applying the desktop settings and fetching the config again", async () => {
      const d = deferred<void>();
      saveGlobalMock.mockReturnValue(d.promise);
      const { user } = renderSettings();
      await loaded();
      await user.type(field("desktop.note"), "hi");
      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(screen.getByRole("button", { name: "Saving..." })).toHaveProperty("disabled", true);
      await act(async () => d.resolve());
      expect(saveGlobalMock).toHaveBeenCalledWith(`${JSON.stringify({ model: "opus", workflows: "w1", desktop: { note: "hi" } }, null, 2)}\n`);
      expect(theme.setThemeName).toHaveBeenCalledWith("dark");
      expect(theme.setFontSizes).toHaveBeenCalledWith(DEFAULT_FONT_SIZES);
      expect(theme.setIslands).toHaveBeenCalledWith(false);
      expect(theme.setHiddenPills).toHaveBeenCalledWith([]);
      expect(globalMock).toHaveBeenCalledTimes(2);
      expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    });

    it("applies nothing for a config without desktop settings, and logs a failed re-fetch", async () => {
      const { user } = renderSettings({ initialSection: "Agent" });
      await loaded();
      globalMock.mockRejectedValue(new Error("gone"));
      await user.type(field("model"), "-x");
      await user.click(screen.getByRole("button", { name: "Save" }));
      await waitFor(() => expect(console.warn).toHaveBeenCalledWith("[loop] re-fetching global config:", expect.any(Error)));
      expect(theme.setThemeName).not.toHaveBeenCalled();
    });

    it.each([
      [new Error("disk full"), "disk full"],
      [{}, "Failed to save"],
    ])("shows a failed global save's error %#", async (err, want) => {
      saveGlobalMock.mockRejectedValue(err);
      const { user } = renderSettings();
      await loaded();
      await user.type(field("desktop.note"), "x");
      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(await screen.findByText(want)).toBeTruthy();
    });

    it("saves JSON that doesn't parse without applying desktop settings", async () => {
      const { user } = renderSettings({ initialSection: "__global_json__" });
      const view = await jsonEditor();
      act(() => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: "{bad" } }));
      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(saveGlobalMock).toHaveBeenCalledWith("{bad");
      await waitFor(() => expect(globalMock).toHaveBeenCalledTimes(2));
      expect(theme.setThemeName).not.toHaveBeenCalled();
    });

    it("saves a dirty form left unmounted without calling it", async () => {
      const { user } = renderSettings();
      await loaded();
      await user.type(field("desktop.note"), "x");
      await user.click(nav("daemon"));
      await user.click(screen.getByRole("button", { name: "Save" }));
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      expect(saveGlobalMock).not.toHaveBeenCalled();
      expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    });

    it("saves the project's edits and fetches it again", async () => {
      const { user } = renderSettings({ projectDirPath: "/work", channelId: "ch-1", initialSection: "__proj_Workspace" });
      await loaded();
      await waitFor(() => expect(field("workspace").value).toBe("/work"));
      await user.type(field("workspace"), "/x");
      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(saveProjectMock).toHaveBeenCalledWith("ch-1", `${JSON.stringify({ workspace: "/work/x" }, null, 2)}\n`);
      await waitFor(() => expect(projectMock).toHaveBeenCalledTimes(2));
    });

    it.each([
      [new Error("untrusted"), "untrusted"],
      [{}, "Failed to save"],
    ])("shows a failed project save's error %#", async (err, want) => {
      saveProjectMock.mockRejectedValue(err);
      projectMock.mockResolvedValueOnce(projectCfg).mockRejectedValue(new Error("gone"));
      const { user } = renderSettings({ projectDirPath: "/work", channelId: "ch-1", initialSection: "__project_json__" });
      const view = await jsonEditor();
      act(() => view.dispatch({ changes: { from: 0, insert: " " } }));
      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(await screen.findByText(want)).toBeTruthy();
    });

    it("logs a failed project re-fetch after a save", async () => {
      projectMock.mockResolvedValueOnce(projectCfg).mockRejectedValue(new Error("gone"));
      const { user } = renderSettings({ projectDirPath: "/work", channelId: "ch-1", initialSection: "__proj_Workspace" });
      await loaded();
      await waitFor(() => expect(field("workspace").value).toBe("/work"));
      await user.type(field("workspace"), "/x");
      await user.click(screen.getByRole("button", { name: "Save" }));
      await waitFor(() => expect(console.warn).toHaveBeenCalledWith("[loop] re-fetching project config:", expect.any(Error)));
    });
  });

  describe("search", () => {
    it("filters the sections, lists matching keys and jumps to one", async () => {
      const animate = vi.fn();
      Object.defineProperty(HTMLElement.prototype, "animate", { value: animate, configurable: true, writable: true });
      try {
        const { user } = renderSettings({ projectDirPath: "/work", channelId: "ch-1" });
        await loaded();
        await user.type(screen.getByTestId("settings-search"), "model");
        expect(screen.queryByTestId("settings-nav-desktop")).toBeNull();
        expect(screen.queryByText("Project")).toBeNull();
        const hit = screen.getByTestId("settings-search-hit-model");
        fireEvent.mouseEnter(hit);
        expect(hit.style.color).not.toBe("");
        fireEvent.mouseLeave(hit);
        const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
        await user.click(hit);
        expect(field("model")).toBeTruthy();
        expect(scroll).toHaveBeenCalledWith({ block: "center" });
        expect(animate).toHaveBeenCalledWith(expect.any(Array), expect.objectContaining({ duration: 1600 }));
      } finally {
        delete (HTMLElement.prototype as { animate?: unknown }).animate;
      }
    });

    it("jumps to a field in jsdom without animations", async () => {
      const { user } = renderSettings({ projectDirPath: "/work", channelId: "ch-1" });
      await loaded();
      await user.type(screen.getByTestId("settings-search"), "workspace dir");
      await user.click(screen.getByTestId("settings-search-hit-workspace"));
      await waitFor(() => expect(field("workspace")).toBeTruthy());
    });

    it("skips the jump when the field isn't shown", async () => {
      projectMock.mockRejectedValue(new Error("no project"));
      const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
      const { user } = renderSettings({ projectDirPath: "/work", channelId: "ch-1" });
      await loaded();
      await user.type(screen.getByTestId("settings-search"), "workspace dir");
      await user.click(screen.getByTestId("settings-search-hit-workspace"));
      expect(await screen.findByTestId("project-config-error")).toBeTruthy();
      expect(scroll).not.toHaveBeenCalled();
    });

    it("says when nothing matches, and Escape clears the query without closing", async () => {
      const { user, onClose } = renderSettings();
      await loaded();
      const search = screen.getByTestId("settings-search");
      await user.type(search, "zzz");
      expect(screen.getByTestId("settings-search-empty")).toBeTruthy();
      await user.keyboard("{Escape}");
      expect((search as HTMLInputElement).value).toBe("");
      expect(onClose).not.toHaveBeenCalled();
      await user.keyboard("{Escape}");
      expect(onClose).toHaveBeenCalledOnce();
    });

    it("opens a section by name", async () => {
      const { user } = renderSettings();
      await loaded();
      await user.type(screen.getByTestId("settings-search"), "daemon");
      await user.click(nav("daemon"));
      expect(screen.getByRole("button", { name: /Restart Daemon/ })).toBeTruthy();
    });
  });

  describe("daemon", () => {
    it("shows the daemon's status and binary, and restarts it", async () => {
      const restarted: DaemonInfo = { running: false, binaryPath: null };
      const d = deferred<DaemonInfo>();
      stubLoopAPI({ getDaemonInfo: () => Promise.resolve({ running: true, binaryPath: "/usr/bin/loop" }), restartDaemon: () => d.promise });
      const onDaemonRestarted = vi.fn();
      const { user } = renderSettings({ initialSection: "Daemon", onDaemonRestarted });
      expect(await screen.findByText("Running")).toBeTruthy();
      expect(screen.getByText("/usr/bin/loop")).toBeTruthy();
      const btn = screen.getByRole("button", { name: /Restart Daemon/ });
      fireEvent.mouseEnter(btn);
      expect(btn.style.borderColor).not.toBe("");
      fireEvent.mouseLeave(btn);
      await user.click(btn);
      const busy = screen.getByRole("button", { name: /Restarting\.\.\./ });
      expect(busy).toHaveProperty("disabled", true);
      const before = busy.style.borderColor;
      fireEvent.mouseEnter(busy);
      expect(busy.style.borderColor).toBe(before);
      await act(async () => d.resolve(restarted));
      expect(screen.getByText("Stopped")).toBeTruthy();
      expect(screen.queryByText("Binary")).toBeNull();
      expect(onDaemonRestarted).toHaveBeenCalledOnce();
    });

    it("keeps the info when a restart returns none, without a restart callback", async () => {
      stubLoopAPI({ getDaemonInfo: () => Promise.resolve({ running: true, binaryPath: null }), restartDaemon: () => Promise.resolve(undefined as never) });
      const { user } = renderSettings({ initialSection: "Daemon" });
      await user.click(await screen.findByRole("button", { name: /Restart Daemon/ }));
      expect(await screen.findByRole("button", { name: /Restart Daemon/ })).toBeTruthy();
      expect(screen.getByText("Running")).toBeTruthy();
    });

    it("ignores a failed restart, and a bridge without the calls", async () => {
      stubLoopAPI({ restartDaemon: () => Promise.reject(new Error("nope")) });
      const onDaemonRestarted = vi.fn();
      const { user, unmount } = renderSettings({ initialSection: "Daemon", onDaemonRestarted });
      await user.click(await screen.findByRole("button", { name: /Restart Daemon/ }));
      expect(await screen.findByRole("button", { name: /Restart Daemon/ })).toBeTruthy();
      expect(onDaemonRestarted).not.toHaveBeenCalled();
      expect(screen.queryByText("Status")).toBeNull();
      unmount();

      stubLoopAPI({});
      renderSettings({ initialSection: "Daemon", onDaemonRestarted });
      await user.click(await screen.findByRole("button", { name: /Restart Daemon/ }));
      await waitFor(() => expect(onDaemonRestarted).toHaveBeenCalledOnce());
    });

    it("restarts without a bridge at all", async () => {
      const onDaemonRestarted = vi.fn();
      const { user } = renderSettings({ initialSection: "Daemon", onDaemonRestarted });
      await user.click(await screen.findByRole("button", { name: /Restart Daemon/ }));
      await waitFor(() => expect(onDaemonRestarted).toHaveBeenCalledOnce());
    });
  });

  describe("docker image", () => {
    it("shows a ready image's versions and an available update, and rebuilds", async () => {
      const onRebuildImage = vi.fn();
      const { user } = renderSettings({
        initialSection: "Docker Image",
        onRebuildImage,
        imageUpdateAvailable: { current_version: "1.0", latest_version: "1.1", component: "claude" },
      });
      expect(await screen.findByText("Ready")).toBeTruthy();
      expect(screen.getByText("1.0")).toBeTruthy();
      expect(screen.getByText("unknown")).toBeTruthy();
      expect(screen.getByText("2026-01-01")).toBeTruthy();
      expect(screen.getByText("Claude Code update available: v1.0 → v1.1")).toBeTruthy();
      const btn = screen.getByRole("button", { name: /Rebuild Image/ });
      fireEvent.mouseEnter(btn);
      expect(btn.style.borderColor).not.toBe("");
      fireEvent.mouseLeave(btn);
      await user.click(btn);
      expect(onRebuildImage).toHaveBeenCalledOnce();
    });

    it("shows a build in progress with its phase", async () => {
      imageMock.mockResolvedValue({ status: { state: "idle" }, versions: { loop_version: "", claude_version: "2.0", built_at: "" } });
      renderSettings({ initialSection: "Docker Image", imageBuildStatus: { state: "building", phase: "pulling" } });
      expect(await screen.findByText("Building — pulling")).toBeTruthy();
      expect(screen.getAllByText("unknown")).toHaveLength(2);
      const btn = screen.getByRole("button", { name: /Building\.\.\./ });
      expect(btn).toHaveProperty("disabled", true);
      const before = btn.style.borderColor;
      fireEvent.mouseEnter(btn);
      expect(btn.style.borderColor).toBe(before);
    });

    it("shows a failed build's error, and none for one without", async () => {
      imageMock.mockRejectedValue(new Error("down"));
      const { rerender } = renderSettings({ initialSection: "Docker Image", imageBuildStatus: { state: "failed", error: "apt failed" } });
      expect(await screen.findByText("Failed")).toBeTruthy();
      expect(screen.getByText("apt failed")).toBeTruthy();
      expect(screen.queryByText("Loop")).toBeNull();
      rerender(<Settings open onClose={vi.fn()} initialSection="Docker Image" imageBuildStatus={{ state: "failed" }} />);
      expect(screen.queryByText("apt failed")).toBeNull();
    });

    it("fetches the image status again once a build completes", async () => {
      const { rerender } = renderSettings({ initialSection: "Docker Image" });
      await screen.findByText("Ready");
      expect(imageMock).toHaveBeenCalledTimes(1);
      imageMock.mockResolvedValue({ ...imageStatus, versions: { ...imageStatus.versions, loop_version: "2.0" } });
      rerender(<Settings open onClose={vi.fn()} initialSection="Docker Image" imageBuildStatus={{ state: "completed" }} />);
      expect(await screen.findByText("2.0")).toBeTruthy();
      imageMock.mockRejectedValue(new Error("down"));
      rerender(<Settings open onClose={vi.fn()} initialSection="Docker Image" imageBuildStatus={{ state: "completed", phase: "done" }} />);
      await waitFor(() => expect(console.warn).toHaveBeenCalledWith("[loop] fetching image status:", expect.any(Error)));
    });
  });

  describe("containers", () => {
    it("estimates what a reclaim frees and reclaims with unused images once confirmed", async () => {
      const d = deferred<Awaited<ReturnType<typeof reclaimDockerSpace>>>();
      reclaimMock.mockReturnValue(d.promise);
      const { user } = renderSettings({ initialSection: "Containers" });
      const est = await screen.findByTestId("reclaim-estimate");
      await waitFor(() => expect(within(est).getByText("Build cache")).toBeTruthy());
      expect(within(est).getAllByText("1.0 KB")).toHaveLength(2);
      expect(within(est).getByText("Unused images (2 tags)").closest("label")!.title).toBe("old:1\nold:2");
      expect(est.textContent).toContain("Total, about1.0 KB");
      await user.click(screen.getByTestId("reclaim-unused-images"));
      expect(est.textContent).toContain("Total, about3.0 KB");
      expect(screen.getByText(/Unused images are pulled or built again/)).toBeTruthy();

      await user.click(screen.getByTestId("reclaim-space"));
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      const btn = screen.getByTestId("reclaim-space");
      fireEvent.mouseEnter(btn);
      expect(btn.style.borderColor).not.toBe("");
      fireEvent.mouseLeave(btn);
      await user.click(btn);
      await user.click(screen.getByTestId("reclaim-confirm"));
      expect(reclaimMock).toHaveBeenCalledWith(true);
      const busy = screen.getByTestId("reclaim-space");
      expect(busy.textContent).toBe("Reclaiming…");
      const before = busy.style.borderColor;
      fireEvent.mouseEnter(busy);
      expect(busy.style.borderColor).toBe(before);
      await act(async () => d.resolve({ build_cache_reclaimed: 1024, images_reclaimed: 0, unused_images_reclaimed: 0, volumes_reclaimed: 0, total_reclaimed: 1024, orphan_volumes_removed: 0 }));
      expect(screen.getByText("Reclaimed 1.0 KB — build cache 1.0 KB.")).toBeTruthy();
      expect(reclaimableMock).toHaveBeenCalledTimes(4);

      await user.click(nav("daemon"));
      await user.click(nav("containers"));
      expect(screen.queryByText(/^Reclaimed/)).toBeNull();
    });

    it.each([
      [new Error("docker down"), "docker down"],
      [{}, "Reclaim failed"],
    ])("shows a failed reclaim's error %#", async (err, want) => {
      reclaimMock.mockRejectedValue(err);
      const { user } = renderSettings({ initialSection: "Containers" });
      await user.click(await screen.findByTestId("reclaim-space"));
      await user.click(screen.getByTestId("reclaim-confirm"));
      expect(reclaimMock).toHaveBeenCalledWith(false);
      expect(await screen.findByText(want)).toBeTruthy();
    });

    it("shows the quick estimate with volumes still sizing, then the sized one", async () => {
      const sized = deferred<DockerReclaimable>();
      reclaimableMock.mockImplementation((withVolumes?: boolean) => (withVolumes ? sized.promise : Promise.resolve({ ...estimate, volumes_sized: false })));
      renderSettings({ initialSection: "Containers" });
      expect(screen.queryByText("Reading Docker's disk usage…")).toBeNull();
      await loaded();
      const est = screen.getByTestId("reclaim-estimate");
      await waitFor(() => expect(within(est).getAllByText("sizing…")).toHaveLength(2));
      expect(est.textContent).toContain("+ volumes");
      expect(within(est).getByText("Deleted channels' Chrome profiles (0 volumes)")).toBeTruthy();
      await act(async () => sized.resolve(estimate));
      expect(within(est).queryByText("sizing…")).toBeNull();
    });

    it("keeps the sized estimate when the quick one arrives later", async () => {
      const quick = deferred<DockerReclaimable>();
      reclaimableMock.mockImplementation((withVolumes?: boolean) => (withVolumes ? Promise.resolve(estimate) : quick.promise));
      renderSettings({ initialSection: "Containers" });
      await loaded();
      const est = screen.getByTestId("reclaim-estimate");
      await waitFor(() => expect(within(est).getByText("Build cache")).toBeTruthy());
      await act(async () => quick.resolve({ ...estimate, volumes_sized: false }));
      expect(within(est).queryByText("sizing…")).toBeNull();
    });

    it("says it's reading the usage, then shows a failed estimate's error", async () => {
      const quick = deferred<DockerReclaimable>();
      const sized = deferred<DockerReclaimable>();
      reclaimableMock.mockImplementation((withVolumes?: boolean) => (withVolumes ? sized.promise : quick.promise));
      renderSettings({ initialSection: "Containers" });
      expect(await screen.findByText("Reading Docker's disk usage…")).toBeTruthy();
      await waitFor(() => expect(reclaimableMock).toHaveBeenCalledTimes(2));
      await act(async () => quick.reject(new Error("docker down")));
      expect(screen.getByText("docker down")).toBeTruthy();
      await act(async () => sized.reject({}));
      expect(screen.getByText("Couldn't read Docker's disk usage")).toBeTruthy();
    });

    it("drops estimates and errors that arrive after leaving the section", async () => {
      const quick = deferred<DockerReclaimable>();
      const sized = deferred<DockerReclaimable>();
      reclaimableMock.mockImplementationOnce(() => quick.promise).mockImplementationOnce(() => sized.promise);
      const { user } = renderSettings({ initialSection: "Containers" });
      await screen.findByText("Reading Docker's disk usage…");
      await user.click(nav("daemon"));
      await act(async () => {
        quick.resolve(estimate);
        sized.reject(new Error("late"));
      });
      expect(screen.queryByTestId("reclaim-estimate")).toBeNull();
      const s2 = deferred<DockerReclaimable>();
      reclaimableMock.mockImplementationOnce(() => Promise.reject(new Error("late too"))).mockImplementationOnce(() => s2.promise);
      await user.click(nav("containers"));
      await user.click(nav("daemon"));
      await act(async () => s2.resolve(estimate));
      expect(screen.queryByText("late too")).toBeNull();
    });
  });

  describe("built-ins", () => {
    it("restores the built-in workflows and shows what changed", async () => {
      const d = deferred<Awaited<ReturnType<typeof restoreBuiltins>>>();
      builtinsMock.mockReturnValue(d.promise);
      const { user } = renderSettings({ initialSection: "Workflows" });
      const bar = await screen.findByTestId("restore-builtins-workflows");
      expect(bar.textContent).toContain("Re-adds any built-ins missing from your config.");
      await user.click(within(bar).getByRole("button", { name: "Restore built-in workflows" }));
      expect(builtinsMock).toHaveBeenCalledWith("workflows");
      expect(within(bar).getByRole("button", { name: "Restoring…" })).toHaveProperty("disabled", true);
      globalMock.mockResolvedValue(cfg({ workflows: "seeded" }));
      await act(async () => d.resolve({ kind: "workflows", added: ["a", "b"], patched: ["c"], skipped: ["d"] }));
      expect(bar.textContent).toContain("Added: a, b · Patched: c · Already present: d");
      expect(field("workflows").value).toBe("seeded");
    });

    it("says when there is nothing to restore, and keeps unsaved edits", async () => {
      builtinsMock.mockResolvedValue({ kind: "shortcuts", added: [], patched: [], skipped: [] });
      const { user } = renderSettings({ initialSection: "Prompt Shortcuts" });
      const bar = await screen.findByTestId("restore-builtins-shortcuts");
      await user.type(field("shortcuts"), "mine");
      globalMock.mockResolvedValue(cfg({ shortcuts: "seeded" }));
      await user.click(within(bar).getByRole("button", { name: "Restore built-in shortcuts" }));
      expect(await within(bar).findByText("Nothing to restore")).toBeTruthy();
      expect(field("shortcuts").value).toBe("mine");
    });

    it("skips the config when the re-fetch fails", async () => {
      builtinsMock.mockResolvedValue({ kind: "workflows", added: ["a"], patched: [], skipped: [] });
      const { user } = renderSettings({ initialSection: "Workflows" });
      const bar = await screen.findByTestId("restore-builtins-workflows");
      globalMock.mockRejectedValue(new Error("down"));
      await user.click(within(bar).getByRole("button"));
      expect(await within(bar).findByText("Added: a")).toBeTruthy();
      expect(field("workflows").value).toBe("w1");
    });

    it.each([
      [new Error("forbidden"), "forbidden"],
      [null, "Restore failed"],
    ])("shows a failed restore's error %#", async (err, want) => {
      builtinsMock.mockRejectedValue(err);
      const { user } = renderSettings({ initialSection: "Workflows" });
      const bar = await screen.findByTestId("restore-builtins-workflows");
      await user.click(within(bar).getByRole("button"));
      expect(await within(bar).findByText(want)).toBeTruthy();
    });
  });

  describe("global sections", () => {
    it("re-fetches the config after a backup key is created, applying its desktop settings", async () => {
      backupMock.mockResolvedValue({ enabled: true, dir: "/backups", times_per_day: 1, keep_last: 1, keep_daily: 1, keep_weekly: 1, keep_monthly: 1 } as never);
      backupKeyMock.mockResolvedValue({ recipient: "age1", identity: "AGE-SECRET" } as never);
      const { user } = renderSettings({ initialSection: "Backup" });
      const btn = await screen.findByTestId("backup-create-key");
      globalMock.mockResolvedValue(cfg({ desktop: { theme: "light", font_sizes: { chat: 20 }, islands: true, hidden_pills: ["pr"] } }));
      await user.click(btn);
      await waitFor(() => expect(theme.setThemeName).toHaveBeenCalledWith("light"));
      expect(theme.setFontSizes).toHaveBeenCalledWith({ ...DEFAULT_FONT_SIZES, chat: 20 });
      expect(theme.setIslands).toHaveBeenCalledWith(true);
      expect(theme.setHiddenPills).toHaveBeenCalledWith(["pr"]);
    });

    it("re-fetches the config after a plugin toggle wrote it", async () => {
      pluginListMock.mockResolvedValue({ off: false, plugins: [{ id: "echo", version: "1", capabilities: [], status: "installed" }] });
      const { user } = renderSettings({ initialSection: "Plugins" });
      expect(await screen.findByTestId("plugins-section")).toBeTruthy();
      await user.click(await screen.findByTestId("plugin-default-echo"));
      expect(pluginEnabledMock).toHaveBeenCalledWith("echo", true);
      await waitFor(() => expect(globalMock).toHaveBeenCalledTimes(2));
      globalMock.mockRejectedValue(new Error("down"));
      await user.click(screen.getByTestId("plugin-default-echo"));
      await waitFor(() => expect(console.warn).toHaveBeenCalledWith("[loop] re-fetching global config:", expect.any(Error)));
    });

    it("restores a global revision and re-fetches the config", async () => {
      const { user } = renderSettings({ initialSection: "__global_history__" });
      expect(await screen.findByText("Global Config History")).toBeTruthy();
      const items = await screen.findAllByTestId("config-history-item");
      await user.click(items[1]!);
      await user.click(await screen.findByTestId("config-history-restore"));
      expect(restoreRevisionMock).toHaveBeenCalledWith(1);
      await waitFor(() => expect(globalMock).toHaveBeenCalledTimes(2));
    });

    it("logs a failed re-fetch after a restore", async () => {
      const { user } = renderSettings({ initialSection: "__global_history__" });
      const items = await screen.findAllByTestId("config-history-item");
      globalMock.mockRejectedValue(new Error("down"));
      await user.click(items[1]!);
      await user.click(await screen.findByTestId("config-history-restore"));
      await waitFor(() => expect(console.warn).toHaveBeenCalledWith("[loop] re-fetching global config:", expect.any(Error)));
    });

    it("shows the global config as JSON", async () => {
      renderSettings({ initialSection: "__global_json__" });
      expect(await screen.findByTestId("config-json-editor")).toBeTruthy();
      expect(screen.getByText("/cfg/config.json")).toBeTruthy();
    });
  });

  describe("project sections", () => {
    const project: Props = { projectDirPath: "/work", channelId: "ch-1" };

    it("shows the project's fields over the global ones it inherits", async () => {
      renderSettings({ ...project, initialSection: "__proj_Workspace" });
      await waitFor(() => expect(field("workspace").value).toBe("/work"));
      expect(projectMock).toHaveBeenCalledWith("ch-1");
    });

    it("shows the project's fields without a global config", async () => {
      globalMock.mockResolvedValue(null as never);
      const { rerender } = renderSettings({ ...project, initialSection: "__proj_Workspace" });
      await waitFor(() => expect(field("workspace").value).toBe("/work"));
      globalMock.mockResolvedValue(cfg(null));
      rerender(<Settings open={false} onClose={vi.fn()} {...project} initialSection="__proj_Workspace" />);
      rerender(<Settings open onClose={vi.fn()} {...project} initialSection="__proj_Workspace" />);
      await waitFor(() => expect(field("workspace").value).toBe("/work"));
    });

    it.each([
      [new Error("no .loop dir"), "no .loop dir"],
      ["forbidden", "forbidden"],
    ])("shows why the project config couldn't load %#", async (err, want) => {
      projectMock.mockRejectedValue(err);
      const { user } = renderSettings({ ...project, initialSection: "__project_json__" });
      expect((await screen.findByTestId("project-config-error")).textContent).toBe(want);
      await user.click(nav("workspace"));
      expect(screen.getByTestId("project-config-error").textContent).toBe(want);
      await user.click(nav("history", 1));
      expect(screen.queryByTestId("project-config-error")).toBeNull();
    });

    it("drops the project config when the channel goes away", async () => {
      const { rerender } = renderSettings({ ...project, initialSection: "__proj_Workspace" });
      await waitFor(() => expect(field("workspace")).toBeTruthy());
      rerender(<Settings open onClose={vi.fn()} projectDirPath="/work" channelId={null} initialSection="__proj_Workspace" />);
      await waitFor(() => expect(field("workspace")).toBeNull());
    });

    it("trusts a changed project config and tells plugin views", async () => {
      trustMock.mockResolvedValue({ trusted: false, current: "{}", approved: "", diff: "", hash: "h1" } as never);
      const heard = vi.fn();
      window.addEventListener(PLUGINS_CHANGED, heard);
      const onProjectTrusted = vi.fn();
      const { user } = renderSettings({ ...project, initialSection: "__project_json__", onProjectTrusted });
      await user.click(await screen.findByTestId("project-trust-button"));
      expect(trustConfigMock).toHaveBeenCalledWith("ch-1", "h1");
      await waitFor(() => expect(onProjectTrusted).toHaveBeenCalledOnce());
      expect(heard).toHaveBeenCalled();
      window.removeEventListener(PLUGINS_CHANGED, heard);
    });

    it("trusts a project config without a callback", async () => {
      trustMock.mockResolvedValue({ trusted: false, current: "{}", approved: "", diff: "", hash: "h1" } as never);
      const heard = vi.fn();
      window.addEventListener(PLUGINS_CHANGED, heard);
      const { user } = renderSettings({ ...project, initialSection: "__proj_Workspace" });
      await user.click(await screen.findByTestId("project-trust-button"));
      await waitFor(() => expect(heard).toHaveBeenCalled());
      window.removeEventListener(PLUGINS_CHANGED, heard);
    });

    it("shows the project's plugins and re-fetches its config after a toggle", async () => {
      trustMock.mockResolvedValue({ trusted: false, current: "{}", approved: "", diff: "", hash: "h1" } as never);
      pluginListMock.mockResolvedValue({ off: false, plugins: [{ id: "echo", version: "1", capabilities: [], status: "installed", enabled: false }] });
      const { user } = renderSettings({ ...project });
      await loaded();
      await user.click(nav("plugins", 1));
      expect(await screen.findByTestId("project-trust-notice")).toBeTruthy();
      await user.click(await screen.findByTestId("project-plugin-enabled-echo"));
      expect(pluginEnabledMock).toHaveBeenCalledWith("echo", true, "ch-1");
      await waitFor(() => expect(projectMock).toHaveBeenCalledTimes(2));
      projectMock.mockRejectedValue(new Error("down"));
      await user.click(screen.getByTestId("project-plugin-enabled-echo"));
      await waitFor(() => expect(console.warn).toHaveBeenCalledWith("[loop] re-fetching project config:", expect.any(Error)));
    });

    it("restores a project revision and re-fetches the project config", async () => {
      const { user } = renderSettings({ ...project, initialSection: "__project_history__" });
      expect(await screen.findByText("Project Config History")).toBeTruthy();
      expect(historyMock).toHaveBeenCalledWith("ch-1", undefined);
      const items = await screen.findAllByTestId("config-history-item");
      await user.click(items[1]!);
      projectMock.mockRejectedValue(new Error("down"));
      await user.click(await screen.findByTestId("config-history-restore"));
      expect(restoreRevisionMock).toHaveBeenCalledWith(1);
      await waitFor(() => expect(projectMock).toHaveBeenCalledTimes(2));
      await waitFor(() => expect(console.warn).toHaveBeenCalledWith("[loop] re-fetching project config:", expect.any(Error)));
    });

    it("re-fetches the project config after a restore", async () => {
      const { user } = renderSettings({ ...project, initialSection: "__project_history__" });
      const items = await screen.findAllByTestId("config-history-item");
      await user.click(items[1]!);
      await user.click(await screen.findByTestId("config-history-restore"));
      await waitFor(() => expect(projectMock).toHaveBeenCalledTimes(2));
    });

    it("shows no project sections without a channel", async () => {
      renderSettings({ projectDirPath: "/work", initialSection: "__project_history__" });
      await loaded();
      expect(screen.queryByTestId("config-history")).toBeNull();
      expect(screen.getAllByTestId("settings-nav-plugins")).toHaveLength(1);
    });
  });
});
