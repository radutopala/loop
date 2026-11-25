import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  fetchPluginAudit,
  fetchPluginList,
  inspectPlugin,
  installPlugin,
  PLUGINS_CHANGED,
  type PluginCandidate,
  type PluginInfo,
  type PluginList,
  removePlugin,
  setPluginEnabled,
  setPluginScheduleEntry,
  setPluginSetting,
  stopPluginContainer,
} from "../../api/plugins";
import { useEventStream } from "../../hooks/useEventStream";
import { darkColors } from "../../theme";
import type { WSEvent } from "../../types";
import { GlobalPluginsSection, ProjectPluginsSection } from "./PluginsSection";

vi.mock("../../api/plugins", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/plugins")>()),
  fetchPluginAudit: vi.fn(),
  fetchPluginList: vi.fn(),
  inspectPlugin: vi.fn(),
  installPlugin: vi.fn(),
  removePlugin: vi.fn(),
  setPluginEnabled: vi.fn(),
  setPluginScheduleEntry: vi.fn(),
  setPluginSetting: vi.fn(),
  stopPluginContainer: vi.fn(),
}));

// The section listens to the daemon's event stream, a WebSocket.
vi.mock("../../hooks/useEventStream", () => ({ useEventStream: vi.fn() }));

const listMock = vi.mocked(fetchPluginList);
const auditMock = vi.mocked(fetchPluginAudit);
const inspectMock = vi.mocked(inspectPlugin);
const installMock = vi.mocked(installPlugin);
const removeMock = vi.mocked(removePlugin);
const enabledMock = vi.mocked(setPluginEnabled);
const scheduleMock = vi.mocked(setPluginScheduleEntry);
const settingMock = vi.mocked(setPluginSetting);
const stopMock = vi.mocked(stopPluginContainer);
const streamMock = vi.mocked(useEventStream);

const echo: PluginInfo = {
  id: "echo",
  name: "Echo",
  version: "1.2.0",
  capabilities: ["net", "schedule"],
  status: "installed",
  image: "ghcr.io/acme/echo:1.2.0@sha256:ab",
  dev: true,
  enabled_by_default: true,
  panels: [
    { id: "main", label: "Main", entry: "main.html" },
    { id: "side", label: "", entry: "side.html" },
  ],
  actions: ["digest", "sweep"],
  default_schedules: { digest: { type: "interval", schedule: "1h" } },
  global_schedules: { sweep: { enabled: true, type: "cron", schedule: "0 9 * * *" }, old: { enabled: true } },
  schedule_problems: ["old: no such action"],
  settings_schema: { lang: { type: "string" } },
  containers: [
    { channel_id: "ch-1", channel_name: "general", status: "running", updated_at: "" },
    { channel_id: "ch-2", status: "stopped", last_error: "exit 1", updated_at: "" },
  ],
};

const bare: PluginInfo = { id: "bare", version: "0.1.0", capabilities: [], status: "failed" };

const candidate: PluginCandidate = {
  id: "echo",
  name: "Echo",
  version: "1.3.0",
  api: "1",
  image: "ghcr.io/acme/echo:1.3.0",
  dev: false,
  capabilities: ["net", "fs"],
  new_capabilities: ["fs"],
  previous_version: "1.2.0",
};

const list = (plugins: PluginInfo[], extra: Partial<PluginList> = {}): PluginList => ({ off: false, plugins, ...extra });

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function renderGlobal() {
  const onConfigChanged = vi.fn();
  const user = userEvent.setup();
  const view = render(<GlobalPluginsSection colors={darkColors} onConfigChanged={onConfigChanged} />);
  return { onConfigChanged, user, ...view };
}

function renderProject(channelId = "ch-1") {
  const onConfigChanged = vi.fn();
  const user = userEvent.setup();
  const view = render(<ProjectPluginsSection colors={darkColors} channelId={channelId} onConfigChanged={onConfigChanged} />);
  return { onConfigChanged, user, ...view };
}

const lastOnEvent = () => streamMock.mock.calls.at(-1)![0].onEvent;

describe("GlobalPluginsSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listMock.mockResolvedValue(list([echo, bare]));
    for (const m of [removeMock, enabledMock, scheduleMock, settingMock, stopMock]) m.mockResolvedValue(undefined);
    auditMock.mockResolvedValue([]);
  });

  it("shows nothing but the note while the list loads, and the list's error", async () => {
    listMock.mockRejectedValue(new Error("daemon unreachable"));
    renderGlobal();
    expect(screen.queryByTestId("plugin-image")).toBeNull();
    expect(await screen.findByText("daemon unreachable")).toBeTruthy();
    expect(listMock).toHaveBeenCalledWith(null);
    expect(streamMock).toHaveBeenCalledWith(expect.objectContaining({ channelId: null }));
  });

  it("says plugins are off when the daemon has them off", async () => {
    listMock.mockResolvedValue({ off: true, plugins: [] });
    renderGlobal();
    expect(await screen.findByText(/Plugins are off\. Turn on Enabled above/)).toBeTruthy();
    expect(screen.queryByTestId("plugin-image")).toBeNull();
  });

  it("says when no plugins are installed", async () => {
    listMock.mockResolvedValue(list([]));
    renderGlobal();
    expect(await screen.findByText("No plugins installed.")).toBeTruthy();
    expect(screen.getByTestId("plugin-image")).toBeTruthy();
  });

  it("shows each plugin's details", async () => {
    renderGlobal();
    const card = within(await screen.findByTestId("plugin-card-echo"));
    expect(card.getByText("Echo")).toBeTruthy();
    expect(card.getByText("dev")).toBeTruthy();
    expect(card.getByText("ghcr.io/acme/echo:1.2.0@sha256:ab")).toBeTruthy();
    expect(card.getByText("net, schedule")).toBeTruthy();
    expect(card.getByText("Main, side")).toBeTruthy();
    expect(card.getByText("digest, sweep")).toBeTruthy();
    expect((card.getByTestId("plugin-default-echo") as HTMLInputElement).checked).toBe(true);
    expect(card.getByTestId("plugin-settings-echo")).toBeTruthy();
    expect(card.getByText("general")).toBeTruthy();
    expect(card.getByText(/exit 1/)).toBeTruthy();
    expect(card.getAllByTestId("plugin-container-stop")).toHaveLength(1);

    const b = within(screen.getByTestId("plugin-card-bare"));
    expect(b.getAllByText("bare")).toHaveLength(2);
    expect(b.getByText("failed")).toBeTruthy();
    expect(b.getByText("nothing")).toBeTruthy();
    expect(b.queryByText("dev")).toBeNull();
    expect(b.queryByText(/Panels:/)).toBeNull();
    expect(b.queryByText(/Actions:/)).toBeNull();
    expect((b.getByTestId("plugin-default-bare") as HTMLInputElement).checked).toBe(false);
    expect(b.getByText(/No containers yet/)).toBeTruthy();
  });

  it("turns a plugin on by default and reloads", async () => {
    const { onConfigChanged, user } = renderGlobal();
    await user.click(await screen.findByTestId("plugin-default-bare"));
    expect(enabledMock).toHaveBeenCalledWith("bare", true);
    await waitFor(() => expect(onConfigChanged).toHaveBeenCalledOnce());
    expect(listMock).toHaveBeenCalledTimes(2);
  });

  it("shows a failed toggle's error on the card", async () => {
    enabledMock.mockRejectedValue("read-only config");
    const { onConfigChanged, user } = renderGlobal();
    await user.click(await screen.findByTestId("plugin-default-echo"));
    expect(enabledMock).toHaveBeenCalledWith("echo", false);
    expect(await within(screen.getByTestId("plugin-card-echo")).findByText("read-only config")).toBeTruthy();
    expect(onConfigChanged).not.toHaveBeenCalled();
  });

  it("reloads on the plugins-changed window event and stream event, not on others", async () => {
    renderGlobal();
    await screen.findByTestId("plugin-card-echo");
    await act(async () => window.dispatchEvent(new Event(PLUGINS_CHANGED)));
    expect(listMock).toHaveBeenCalledTimes(2);
    await act(async () => lastOnEvent()({ type: "plugins.changed" } as WSEvent));
    expect(listMock).toHaveBeenCalledTimes(3);
    await act(async () => lastOnEvent()({ type: "message.created" } as WSEvent));
    expect(listMock).toHaveBeenCalledTimes(3);
  });

  it("stops listening for the window event once unmounted", async () => {
    const { unmount } = renderGlobal();
    await screen.findByTestId("plugin-card-echo");
    unmount();
    window.dispatchEvent(new Event(PLUGINS_CHANGED));
    expect(listMock).toHaveBeenCalledTimes(1);
  });

  it("drops a list that arrives after unmount", async () => {
    const d = deferred<PluginList>();
    listMock.mockReturnValue(d.promise);
    const { unmount } = renderGlobal();
    unmount();
    await act(async () => d.resolve(list([echo])));
    expect(screen.queryByTestId("plugin-card-echo")).toBeNull();
  });

  describe("installing", () => {
    it("reviews an image on Enter and installs it once approved", async () => {
      inspectMock.mockResolvedValue({ ...candidate, previous_version: undefined, new_capabilities: [], dev: true });
      installMock.mockResolvedValue(candidate);
      const { user, onConfigChanged } = renderGlobal();
      const input = await screen.findByTestId("plugin-image");
      expect((screen.getByTestId("plugin-inspect") as HTMLButtonElement).disabled).toBe(true);
      await user.type(input, "   {Enter}");
      expect(inspectMock).not.toHaveBeenCalled();
      await user.clear(input);
      await user.type(input, " ghcr.io/acme/echo:1.3.0 ");
      await user.keyboard("{Shift}");
      expect(inspectMock).not.toHaveBeenCalled();
      await user.click(screen.getByTestId("plugin-dev"));
      await user.keyboard("{Enter}");
      await user.click(input);
      await user.keyboard("{Enter}");
      expect(inspectMock).toHaveBeenCalledWith("ghcr.io/acme/echo:1.3.0", true);

      const review = within(await screen.findByTestId("plugin-review"));
      expect(review.getByText(/Echo 1\.3\.0/)).toBeTruthy();
      expect(review.getByText("dev")).toBeTruthy();
      expect(review.getByText("It asks for:")).toBeTruthy();
      expect(review.getAllByTestId("plugin-review-cap").map((e) => e.textContent)).toEqual(["net", "fs"]);
      await user.click(review.getByTestId("plugin-approve"));
      expect(installMock).toHaveBeenCalledWith(expect.objectContaining({ image: "ghcr.io/acme/echo:1.3.0" }), ["net", "fs"]);
      await waitFor(() => expect(screen.queryByTestId("plugin-review")).toBeNull());
      expect((screen.getByTestId("plugin-image") as HTMLInputElement).value).toBe("");
      expect((screen.getByTestId("plugin-dev") as HTMLInputElement).checked).toBe(false);
      expect(listMock).toHaveBeenCalledTimes(2);
      expect(onConfigChanged).not.toHaveBeenCalled();
    });

    it("shows an image's review without a name or capabilities, and cancels it", async () => {
      inspectMock.mockResolvedValue({ ...candidate, name: "", capabilities: [], new_capabilities: undefined as never, previous_version: undefined });
      const { user } = renderGlobal();
      await user.type(await screen.findByTestId("plugin-image"), "echo");
      await user.click(screen.getByTestId("plugin-inspect"));
      const review = within(await screen.findByTestId("plugin-review"));
      expect(review.getByText("echo 1.3.0")).toBeTruthy();
      expect(review.queryByText("dev")).toBeNull();
      expect(review.getByText("It asks for no capabilities.")).toBeTruthy();
      expect(review.getByTestId("plugin-approve").textContent).toBe("Approve and install");
      await user.click(review.getByRole("button", { name: "Cancel" }));
      expect(screen.queryByTestId("plugin-review")).toBeNull();
      expect((screen.getByTestId("plugin-image") as HTMLInputElement).value).toBe("echo");
    });

    it("shows a failed review's error and a busy button while it runs", async () => {
      const d = deferred<PluginCandidate>();
      inspectMock.mockReturnValue(d.promise);
      const { user } = renderGlobal();
      await user.type(await screen.findByTestId("plugin-image"), "nope");
      await user.click(screen.getByTestId("plugin-inspect"));
      expect(screen.getByTestId("plugin-inspect").textContent).toBe("Checking…");
      await act(async () => d.reject(new Error("no such image")));
      expect(screen.getByText("no such image")).toBeTruthy();
      expect(screen.getByTestId("plugin-inspect").textContent).toBe("Review");
    });

    it("keeps the review with the error when the install fails", async () => {
      inspectMock.mockResolvedValue(candidate);
      const d = deferred<PluginCandidate>();
      installMock.mockReturnValue(d.promise);
      const { user } = renderGlobal();
      await user.type(await screen.findByTestId("plugin-image"), "echo");
      await user.click(screen.getByTestId("plugin-inspect"));
      await user.click(await screen.findByTestId("plugin-approve"));
      expect(screen.getByTestId("plugin-approve").textContent).toBe("Installing…");
      await act(async () => d.reject(new Error("pull failed")));
      expect(screen.getByText("pull failed")).toBeTruthy();
      expect(screen.getByTestId("plugin-review")).toBeTruthy();
    });
  });

  describe("upgrading and removing", () => {
    it("upgrades from the image's repository, marking new capabilities", async () => {
      inspectMock.mockResolvedValue(candidate);
      installMock.mockResolvedValue(candidate);
      const { user, onConfigChanged } = renderGlobal();
      const card = within(await screen.findByTestId("plugin-card-echo"));
      await user.click(card.getByTestId("plugin-upgrade"));
      const inputs = card.getAllByTestId("plugin-image") as HTMLInputElement[];
      expect(inputs[0]!.value).toBe("ghcr.io/acme/echo");
      expect((card.getByTestId("plugin-dev") as HTMLInputElement).checked).toBe(true);
      await user.click(card.getByTestId("plugin-inspect"));
      expect(inspectMock).toHaveBeenCalledWith("ghcr.io/acme/echo", true);
      const review = within(await card.findByTestId("plugin-review"));
      expect(review.getByText(/Echo 1\.2\.0 → 1\.3\.0/)).toBeTruthy();
      expect(review.getAllByTestId("plugin-review-cap").map((e) => e.textContent)).toEqual(["net", "fs  (new)"]);
      await user.click(review.getByRole("button", { name: "Approve and upgrade" }));
      await waitFor(() => expect(card.getByTestId("plugin-upgrade")).toBeTruthy());
      expect(onConfigChanged).toHaveBeenCalledOnce();
    });

    it("cancels an upgrade from the image box and from the review", async () => {
      inspectMock.mockResolvedValue(candidate);
      const { user } = renderGlobal();
      const card = within(await screen.findByTestId("plugin-card-bare"));
      await user.click(card.getByTestId("plugin-upgrade"));
      expect((card.getByTestId("plugin-image") as HTMLInputElement).value).toBe("");
      await user.click(card.getByRole("button", { name: "Cancel" }));
      expect(card.queryByTestId("plugin-image")).toBeNull();

      await user.click(card.getByTestId("plugin-upgrade"));
      await user.type(card.getByTestId("plugin-image"), "bare");
      await user.click(card.getByTestId("plugin-inspect"));
      await user.click(await card.findByRole("button", { name: "Cancel" }));
      expect(card.getByTestId("plugin-upgrade")).toBeTruthy();
    });

    it("removes a plugin with its data once confirmed", async () => {
      const { user, onConfigChanged } = renderGlobal();
      const card = within(await screen.findByTestId("plugin-card-echo"));
      await user.click(card.getByTestId("plugin-remove"));
      await user.click(card.getByTestId("plugin-purge-echo"));
      await user.click(card.getByTestId("plugin-remove-confirm"));
      expect(removeMock).toHaveBeenCalledWith("echo", true);
      await waitFor(() => expect(onConfigChanged).toHaveBeenCalledOnce());
    });

    it("names a plugin without a name by its id, and cancels removing it", async () => {
      const { user } = renderGlobal();
      const card = within(await screen.findByTestId("plugin-card-bare"));
      await user.click(card.getByTestId("plugin-remove"));
      expect(card.getByTestId("plugin-remove-confirm").textContent).toBe("Remove bare");
      await user.click(card.getByRole("button", { name: "Cancel" }));
      expect(card.getByTestId("plugin-remove")).toBeTruthy();
      expect(removeMock).not.toHaveBeenCalled();
    });
  });

  describe("containers", () => {
    it("stops a running container and tells plugin views", async () => {
      const heard = vi.fn();
      window.addEventListener(PLUGINS_CHANGED, heard);
      const { user } = renderGlobal();
      await user.click(await screen.findByTestId("plugin-container-stop"));
      expect(stopMock).toHaveBeenCalledWith("echo", "ch-1");
      await waitFor(() => expect(heard).toHaveBeenCalledOnce());
      window.removeEventListener(PLUGINS_CHANGED, heard);
    });

    it("shows a failed stop's error", async () => {
      stopMock.mockRejectedValue(new Error("container gone"));
      const { user } = renderGlobal();
      await user.click(await screen.findByTestId("plugin-container-stop"));
      expect(await screen.findByText("container gone")).toBeTruthy();
    });

    it("shows a container's gate audit, refreshes and hides it", async () => {
      const d = deferred<Awaited<ReturnType<typeof fetchPluginAudit>>>();
      auditMock.mockReturnValueOnce(d.promise);
      const { user } = renderGlobal();
      const [first] = await screen.findAllByTestId("plugin-container-audit");
      await user.click(first!);
      expect(screen.getByText("Loading…")).toBeTruthy();
      expect(auditMock).toHaveBeenCalledWith("echo", "ch-1");
      await act(async () =>
        d.resolve([
          { ts: "2026-01-02T03:04:05Z", kind: "network", target: "evil.example:443", rule_id: "deny-all", decision: "deny" },
          { ts: "2026-01-02T03:04:06Z", kind: "file", target: "/etc/hosts", rule_id: "", decision: "allow" },
        ]),
      );
      expect(screen.getByText("2 records, 1 denied")).toBeTruthy();
      const entries = screen.getAllByTestId("plugin-audit-entry");
      expect(within(entries[0]!).getByText("deny-all")).toBeTruthy();
      expect(within(entries[1]!).getByTitle("/etc/hosts")).toBeTruthy();
      expect(entries[1]!.children).toHaveLength(4);

      auditMock.mockResolvedValueOnce([{ ts: "2026-01-02T03:04:05Z", kind: "network", target: "x", rule_id: "", decision: "deny" }]);
      await user.click(screen.getByRole("button", { name: "Refresh" }));
      expect(await screen.findByText("1 record, 1 denied")).toBeTruthy();

      await user.click(screen.getByRole("button", { name: "Hide gate audit" }));
      expect(screen.queryByTestId("plugin-audit")).toBeNull();
    });

    it("says when the gate denied nothing, and shows a failed audit's error", async () => {
      const { user } = renderGlobal();
      const buttons = await screen.findAllByTestId("plugin-container-audit");
      await user.click(buttons[0]!);
      expect(await screen.findByText("The gate hasn't denied anything here.")).toBeTruthy();
      expect(screen.queryByTestId("plugin-audit-entry")).toBeNull();
      auditMock.mockRejectedValue(new Error("audit unavailable"));
      await user.click(buttons[1]!);
      expect(await screen.findByText("audit unavailable")).toBeTruthy();
    });
  });

  describe("schedules", () => {
    it("shows each scheduled action in every project, its strays and problems", async () => {
      renderGlobal();
      const digest = within(await screen.findByTestId("plugin-schedule-echo-digest"));
      expect((digest.getByTestId("plugin-schedule-toggle-echo-digest") as HTMLInputElement).checked).toBe(false);
      expect(digest.getByText("Scheduled digest in every project")).toBeTruthy();
      expect(digest.getByRole("option", { name: "type: interval" })).toBeTruthy();
      expect((digest.getByTestId("plugin-schedule-schedule-echo-digest") as HTMLInputElement).placeholder).toBe("default 1h");
      expect(digest.queryByRole("button", { name: "Remove" })).toBeNull();

      const sweep = within(screen.getByTestId("plugin-schedule-echo-sweep"));
      expect((sweep.getByTestId("plugin-schedule-toggle-echo-sweep") as HTMLInputElement).checked).toBe(true);
      expect((sweep.getByTestId("plugin-schedule-type-echo-sweep") as HTMLSelectElement).value).toBe("cron");
      expect(sweep.getByRole("option", { name: "type: cron" })).toBeTruthy();
      expect((sweep.getByTestId("plugin-schedule-schedule-echo-sweep") as HTMLInputElement).placeholder).toBe("e.g. 0 9 * * * or 1h");
      expect(sweep.getByTestId("plugin-schedule-remove-echo-sweep").title).toBe("Remove the global entry; the tasks it made are deleted.");

      expect(within(screen.getByTestId("plugin-schedule-echo-old")).getByText("Scheduled old")).toBeTruthy();
      expect(screen.getByTestId("plugin-schedule-problems-echo").textContent).toBe("No task for: old: no such action");
    });

    it("turns a schedule on, sets its type and schedule, and removes an entry", async () => {
      const { user, onConfigChanged } = renderGlobal();
      await user.click(await screen.findByTestId("plugin-schedule-toggle-echo-digest"));
      expect(scheduleMock).toHaveBeenCalledWith("echo", "digest", { enabled: true }, undefined);
      await waitFor(() => expect(onConfigChanged).toHaveBeenCalledOnce());

      await user.selectOptions(screen.getByTestId("plugin-schedule-type-echo-sweep"), "");
      expect(scheduleMock).toHaveBeenLastCalledWith("echo", "sweep", { enabled: true, type: undefined, schedule: "0 9 * * *" }, undefined);
      await user.selectOptions(screen.getByTestId("plugin-schedule-type-echo-digest"), "interval");
      expect(scheduleMock).toHaveBeenLastCalledWith("echo", "digest", { enabled: false, type: "interval" }, undefined);

      const box = screen.getByTestId("plugin-schedule-schedule-echo-sweep");
      await user.click(box);
      await user.keyboard("{Enter}");
      await user.tab();
      expect(scheduleMock).toHaveBeenCalledTimes(3);
      await user.clear(box);
      await user.type(box, " 2h {Enter}");
      expect(scheduleMock).toHaveBeenLastCalledWith("echo", "sweep", { enabled: true, type: "cron", schedule: "2h" }, undefined);
      await user.clear(box);
      await user.tab();
      expect(scheduleMock).toHaveBeenLastCalledWith("echo", "sweep", { enabled: true, type: "cron", schedule: undefined }, undefined);

      await user.click(screen.getByTestId("plugin-schedule-remove-echo-old"));
      expect(scheduleMock).toHaveBeenLastCalledWith("echo", "old", undefined, undefined);
    });

    it("disables an action's row while it saves and shows a failed save's error", async () => {
      const d = deferred<void>();
      scheduleMock.mockReturnValue(d.promise);
      const { user } = renderGlobal();
      await user.click(await screen.findByTestId("plugin-schedule-remove-echo-sweep"));
      expect((screen.getByTestId("plugin-schedule-remove-echo-sweep") as HTMLButtonElement).disabled).toBe(true);
      expect((screen.getByTestId("plugin-schedule-toggle-echo-sweep") as HTMLInputElement).disabled).toBe(true);
      await act(async () => d.reject(new Error("bad schedule")));
      expect(within(screen.getByTestId("plugin-card-echo")).getByText("bad schedule")).toBeTruthy();
      expect((screen.getByTestId("plugin-schedule-remove-echo-sweep") as HTMLButtonElement).disabled).toBe(false);
    });

    it("shows a new schedule the plugin arrives with", async () => {
      renderGlobal();
      await screen.findByTestId("plugin-schedule-echo-sweep");
      listMock.mockResolvedValue(list([{ ...echo, global_schedules: { sweep: { schedule: "3h" } } }]));
      await act(async () => window.dispatchEvent(new Event(PLUGINS_CHANGED)));
      await waitFor(() => expect((screen.getByTestId("plugin-schedule-schedule-echo-sweep") as HTMLInputElement).value).toBe("3h"));
    });

    it("lists no schedules for a plugin without the schedule capability", async () => {
      listMock.mockResolvedValue(list([{ ...echo, capabilities: ["net"], actions: undefined }]));
      renderGlobal();
      await screen.findByTestId("plugin-card-echo");
      expect(screen.queryByTestId("plugin-schedule-echo-sweep")).toBeNull();
      expect(screen.queryByTestId("plugin-schedule-echo-old")).toBeNull();
    });

    it("lists a stray entry for a plugin without actions", async () => {
      listMock.mockResolvedValue(list([{ ...echo, actions: undefined, global_schedules: undefined, schedule_problems: undefined }]));
      renderGlobal();
      await screen.findByTestId("plugin-card-echo");
      expect(screen.queryByTestId("plugin-schedule-echo-sweep")).toBeNull();
      listMock.mockResolvedValue(list([{ ...echo, actions: undefined, schedule_problems: undefined }]));
      await act(async () => window.dispatchEvent(new Event(PLUGINS_CHANGED)));
      expect(await screen.findByTestId("plugin-schedule-echo-sweep")).toBeTruthy();
      expect(screen.queryByTestId("plugin-schedule-problems-echo")).toBeNull();
    });
  });
});

describe("ProjectPluginsSection", () => {
  const inProject: PluginInfo = {
    ...echo,
    enabled: true,
    project_enabled: undefined,
    global_schedules: { sweep: { enabled: true, schedule: "0 9 * * *" } },
    project_schedules: { digest: { enabled: false } },
    schedules: { sweep: { enabled: true, schedule: "0 9 * * *" } },
    schedule_problems: undefined,
  };
  const pendingOn: PluginInfo = { ...bare, id: "off", name: "Off", enabled: false, project_enabled: true, enabled_by_default: false };
  const setOff: PluginInfo = { ...bare, id: "quiet", enabled: false, project_enabled: false };
  const followsOff: PluginInfo = { ...bare, id: "plain", enabled: false, enabled_by_default: false };

  beforeEach(() => {
    vi.clearAllMocks();
    listMock.mockResolvedValue(list([inProject, pendingOn, setOff, followsOff]));
    for (const m of [enabledMock, scheduleMock, settingMock]) m.mockResolvedValue(undefined);
  });

  it("renders nothing until the list loads, then the list's error", async () => {
    const d = deferred<PluginList>();
    listMock.mockReturnValue(d.promise);
    const { container } = renderProject();
    expect(container.innerHTML).toBe("");
    expect(listMock).toHaveBeenCalledWith("ch-1");
    expect(streamMock).toHaveBeenCalledWith(expect.objectContaining({ channelId: "ch-1" }));
    await act(async () => d.reject(new Error("no such channel")));
    expect(screen.getByText("no such channel")).toBeTruthy();
  });

  it("drops a list and an error from the channel it left", async () => {
    const first = deferred<PluginList>();
    const second = deferred<PluginList>();
    listMock
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
      .mockReturnValueOnce(new Promise(() => {}));
    const { rerender } = renderProject("ch-1");
    rerender(<ProjectPluginsSection colors={darkColors} channelId="ch-2" onConfigChanged={vi.fn()} />);
    await act(async () => first.resolve(list([inProject])));
    expect(screen.queryByTestId("project-plugin-echo")).toBeNull();
    rerender(<ProjectPluginsSection colors={darkColors} channelId="ch-3" onConfigChanged={vi.fn()} />);
    await act(async () => second.reject(new Error("stale")));
    expect(screen.queryByText("stale")).toBeNull();
  });

  it("says plugins are off in the global config", async () => {
    listMock.mockResolvedValue({ off: true, plugins: [] });
    renderProject();
    expect(await screen.findByText("Plugins are off in the global config (Global → Plugins).")).toBeTruthy();
  });

  it("says when no plugins are installed", async () => {
    listMock.mockResolvedValue(list([]));
    renderProject();
    expect(await screen.findByText(/No plugins installed\. Install them under Global/)).toBeTruthy();
  });

  it("shows how each plugin's enablement is set in the project", async () => {
    renderProject();
    const e = within(await screen.findByTestId("project-plugin-echo"));
    expect(e.getByText("Follows the global default (on).")).toBeTruthy();
    expect((e.getByTestId("project-plugin-enabled-echo") as HTMLInputElement).checked).toBe(true);
    expect(e.queryByTestId("project-plugin-pending-echo")).toBeNull();
    expect(e.getAllByTestId("plugin-container")).toHaveLength(1);
    expect(e.getByText("general")).toBeTruthy();

    const o = within(screen.getByTestId("project-plugin-off"));
    expect(o.getByText("Off")).toBeTruthy();
    expect(o.getByText("Set in this project (on).")).toBeTruthy();
    expect(o.getByTestId("project-plugin-pending-off").textContent).toMatch(/says on, but it takes effect only once you trust/);

    const q = within(screen.getByTestId("project-plugin-quiet"));
    expect(q.getByText("Set in this project (off).")).toBeTruthy();
    expect(q.queryByTestId("project-plugin-pending-quiet")).toBeNull();
    expect(within(screen.getByTestId("project-plugin-plain")).getByText("Follows the global default (off).")).toBeTruthy();
  });

  it("marks a project's off that waits for trust", async () => {
    listMock.mockResolvedValue(list([{ ...bare, enabled: true, project_enabled: false }]));
    renderProject();
    expect((await screen.findByTestId("project-plugin-pending-bare")).textContent).toMatch(/says off, but/);
  });

  it("enables a plugin in the project", async () => {
    const d = deferred<void>();
    enabledMock.mockReturnValue(d.promise);
    const { user, onConfigChanged } = renderProject();
    await user.click(await screen.findByTestId("project-plugin-enabled-plain"));
    expect(enabledMock).toHaveBeenCalledWith("plain", true, "ch-1");
    expect((screen.getByTestId("project-plugin-enabled-plain") as HTMLInputElement).disabled).toBe(true);
    await act(async () => d.resolve());
    expect(onConfigChanged).toHaveBeenCalledOnce();
    expect(listMock).toHaveBeenCalledTimes(2);
  });

  it("shows a failed toggle's error", async () => {
    enabledMock.mockRejectedValue(new Error("project config untrusted"));
    const { user, onConfigChanged } = renderProject();
    await user.click(await screen.findByTestId("project-plugin-enabled-echo"));
    expect(enabledMock).toHaveBeenCalledWith("echo", false, "ch-1");
    expect(await screen.findByText("project config untrusted")).toBeTruthy();
    expect(onConfigChanged).not.toHaveBeenCalled();
  });

  it("shows the project's schedules, pending ones and the global entry they follow", async () => {
    renderProject();
    const digest = within(await screen.findByTestId("project-plugin-schedule-echo-digest"));
    expect(digest.getByText("Scheduled digest")).toBeTruthy();
    expect(digest.getByTestId("project-plugin-schedule-pending-echo-digest").textContent).toMatch(/asks for it off, but/);
    expect(digest.getByTestId("project-plugin-schedule-remove-echo-digest").title).toBe("Remove this project's entry; the task it made is deleted.");
    const sweep = within(screen.getByTestId("project-plugin-schedule-echo-sweep"));
    expect(sweep.getByText("Follows the global entry.")).toBeTruthy();
    expect((sweep.getByTestId("project-plugin-schedule-schedule-echo-sweep") as HTMLInputElement).placeholder).toBe("default 0 9 * * *");
    expect(sweep.queryByTestId("project-plugin-schedule-pending-echo-sweep")).toBeNull();
  });

  it("marks a pending schedule the project asks on", async () => {
    listMock.mockResolvedValue(list([{ ...inProject, project_schedules: { digest: { schedule: "2h" } }, schedules: {} }]));
    renderProject();
    expect((await screen.findByTestId("project-plugin-schedule-pending-echo-digest")).textContent).toMatch(/asks for it on, but/);
  });

  it("disables the schedules of a plugin off in the project", async () => {
    listMock.mockResolvedValue(list([{ ...inProject, enabled: false }]));
    renderProject();
    expect(((await screen.findByTestId("project-plugin-schedule-toggle-echo-sweep")) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByTestId("project-plugin-schedule-type-echo-sweep") as HTMLSelectElement).disabled).toBe(true);
  });

  it("saves a schedule and a setting in the project and reloads", async () => {
    const { user, onConfigChanged } = renderProject();
    await user.click(await screen.findByTestId("project-plugin-schedule-toggle-echo-sweep"));
    expect(scheduleMock).toHaveBeenCalledWith("echo", "sweep", { enabled: false, schedule: "0 9 * * *" }, "ch-1");
    await waitFor(() => expect(onConfigChanged).toHaveBeenCalledOnce());
    await user.type(screen.getByTestId("plugin-setting-echo-lang"), "en{Enter}");
    expect(settingMock).toHaveBeenCalledWith("echo", "lang", "en", "ch-1");
    await waitFor(() => expect(onConfigChanged).toHaveBeenCalledTimes(2));
    expect(listMock).toHaveBeenCalledTimes(3);
  });

  it("shows a failed schedule save's error", async () => {
    scheduleMock.mockRejectedValue(new Error("bad entry"));
    const { user } = renderProject();
    await user.click(await screen.findByTestId("project-plugin-schedule-remove-echo-digest"));
    expect(scheduleMock).toHaveBeenCalledWith("echo", "digest", undefined, "ch-1");
    expect(await screen.findByText("bad entry")).toBeTruthy();
  });
});
