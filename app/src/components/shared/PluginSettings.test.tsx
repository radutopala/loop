import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { type PluginInfo, setPluginSetting } from "../../api/plugins";
import { darkColors } from "../../theme";
import { PluginSettings } from "./PluginSettings";

vi.mock("../../api/plugins", () => ({ setPluginSetting: vi.fn() }));

const setMock = vi.mocked(setPluginSetting);

const plugin: PluginInfo = {
  id: "echo",
  version: "1.0.0",
  capabilities: [],
  status: "running",
  settings_schema: {
    lang: { type: "string", title: "Language", description: "What to answer in", enum: ["en", "fr"], "x-order": 1 },
    loud: { type: "boolean", "x-order": 2 },
    limit: { type: "integer", minimum: 1, maximum: 9, default: 3, "x-order": 3 },
    ratio: { type: "number", "x-order": 4 },
    name: { type: "string", "x-order": 5 },
  },
  global_settings: { lang: "fr", limit: 5 },
  project_settings: { lang: "en" },
  settings_problems: ["limit: too big"],
};

function renderSettings(props: Partial<Parameters<typeof PluginSettings>[0]> = {}) {
  const onChanged = vi.fn();
  const user = userEvent.setup();
  const view = render(<PluginSettings colors={darkColors} plugin={plugin} onChanged={onChanged} {...props} />);
  return { onChanged, user, ...view };
}

const field = (key: string) => screen.getByTestId(`plugin-setting-echo-${key}`) as HTMLInputElement & HTMLSelectElement;

describe("PluginSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setMock.mockResolvedValue(undefined);
  });

  it("renders nothing for a plugin without settings", () => {
    const { container } = renderSettings({ plugin: { ...plugin, settings_schema: undefined } });
    expect(container.innerHTML).toBe("");
  });

  it("shows the global values, with each unset setting following the manifest default", () => {
    renderSettings();
    expect(screen.getByText("Language")).toBeTruthy();
    expect(screen.getByTitle("What to answer in")).toBeTruthy();
    expect(field("lang").value).toBe("1");
    expect(field("loud").value).toBe("");
    expect(screen.getByRole("option", { name: "true" })).toBeTruthy();
    expect(field("limit").value).toBe("5");
    expect(field("limit").placeholder).toBe("default (3) · 1–9");
    expect(field("limit").inputMode).toBe("decimal");
    expect(field("name").placeholder).toBe("default");
    expect(field("name").inputMode).toBe("");
    expect(screen.queryByTestId("plugin-settings-problems-echo")).toBeNull();
  });

  it("shows a project's own values over the global ones, and its left-out settings", () => {
    renderSettings({ channelId: "ch-1" });
    expect(field("lang").value).toBe("0");
    expect(field("limit").value).toBe("");
    expect(field("limit").placeholder).toBe("default (5) · 1–9");
    expect(field("ratio").placeholder).toBe("default");
    expect(screen.getByTestId("plugin-settings-problems-echo").textContent).toBe("Left out: limit: too big");
  });

  it("falls back to the manifest default and no values when the project has none", () => {
    renderSettings({ channelId: "ch-1", plugin: { ...plugin, project_settings: undefined, global_settings: undefined, settings_problems: [] } });
    expect(field("lang").value).toBe("");
    expect(field("limit").placeholder).toBe("default (3) · 1–9");
    expect(screen.queryByTestId("plugin-settings-problems-echo")).toBeNull();
  });

  it("uses no values for a global config without any", () => {
    renderSettings({ plugin: { ...plugin, global_settings: undefined } });
    expect(field("limit").value).toBe("");
  });

  it("saves a chosen option, and unsets the setting on default", async () => {
    const { onChanged, user } = renderSettings({ channelId: "ch-1" });
    await user.selectOptions(field("lang"), "1");
    expect(setMock).toHaveBeenCalledWith("echo", "lang", "fr", "ch-1");
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
    await user.selectOptions(field("loud"), "1");
    expect(setMock).toHaveBeenLastCalledWith("echo", "loud", false, "ch-1");
    await user.selectOptions(field("lang"), "");
    expect(setMock).toHaveBeenLastCalledWith("echo", "lang", undefined, "ch-1");
  });

  it("saves a typed number on Enter and a text as it loses focus", async () => {
    const { user } = renderSettings();
    await user.clear(field("limit"));
    await user.type(field("limit"), "7{Enter}");
    expect(setMock).toHaveBeenCalledWith("echo", "limit", 7, undefined);
    await user.type(field("name"), "bob");
    await user.tab();
    expect(setMock).toHaveBeenLastCalledWith("echo", "name", "bob", undefined);
  });

  it("doesn't save an unchanged value", async () => {
    const { user } = renderSettings();
    await user.click(field("limit"));
    await user.keyboard("{Enter}");
    await user.tab();
    expect(setMock).not.toHaveBeenCalled();
  });

  it("shows a typed value that isn't a number without saving it", async () => {
    const { user } = renderSettings();
    await user.type(field("ratio"), "x");
    await user.tab();
    expect(screen.getByText(`"x" isn't a number`)).toBeTruthy();
    expect(setMock).not.toHaveBeenCalled();
  });

  it("disables the inputs while a save is in flight", async () => {
    let finish!: () => void;
    setMock.mockReturnValue(new Promise<void>((r) => (finish = r)));
    const { user } = renderSettings();
    await user.selectOptions(field("lang"), "0");
    expect(field("name").disabled).toBe(true);
    await act(async () => finish());
    expect(field("name").disabled).toBe(false);
  });

  it.each([
    [new Error("config is read-only"), "config is read-only"],
    ["denied", "denied"],
  ])("shows a failed save's error %s", async (err, want) => {
    setMock.mockRejectedValue(err);
    const { onChanged, user } = renderSettings();
    await user.selectOptions(field("lang"), "0");
    expect(await screen.findByText(want)).toBeTruthy();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("shows a new value the plugin arrives with", () => {
    const { rerender, onChanged } = renderSettings();
    rerender(<PluginSettings colors={darkColors} plugin={{ ...plugin, global_settings: { limit: 8 } }} onChanged={onChanged} />);
    expect(field("limit").value).toBe("8");
  });
});
