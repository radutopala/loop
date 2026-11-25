// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { type AppPanelState, useAppPanelState } from "./useAppPanelState";

type Panel = Parameters<AppPanelState["togglePanel"]>[0];

const PANELS: { name: Panel; key: keyof AppPanelState }[] = [
  { name: "settings", key: "settingsOpen" },
  { name: "readme", key: "readmeOpen" },
  { name: "containers", key: "containersOpen" },
  { name: "tasks", key: "tasksOpen" },
  { name: "workflows", key: "workflowsOpen" },
  { name: "shares", key: "sharesOpen" },
];

function openPanels(s: AppPanelState): Panel[] {
  return PANELS.filter((p) => s[p.key]).map((p) => p.name);
}

describe("useAppPanelState", () => {
  it("starts with every panel closed and nothing pending", () => {
    const { result } = renderHook(() => useAppPanelState());
    expect(openPanels(result.current)).toEqual([]);
    expect(result.current.settingsDirPath).toBeNull();
    expect(result.current.settingsSection).toBeNull();
    expect(result.current.configDirty).toBe(false);
    expect(result.current.pendingSelectId).toBeNull();
  });

  it.each(PANELS)("togglePanel($name) opens it alone, closing the others, then closes it", ({ name }) => {
    const { result } = renderHook(() => useAppPanelState());
    const other = name === "readme" ? "tasks" : "readme";
    act(() => result.current.togglePanel(other));
    act(() => result.current.togglePanel(name));
    expect(openPanels(result.current)).toEqual([name]);
    act(() => result.current.togglePanel(name));
    expect(openPanels(result.current)).toEqual([]);
  });

  it("togglePanel(settings) clears the dir path and section; other panels keep them", () => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => result.current.openProjectTrust("/p"));
    act(() => result.current.togglePanel("readme"));
    expect(result.current.settingsDirPath).toBe("/p");
    expect(result.current.settingsSection).toBe("__project_json__");
    act(() => result.current.togglePanel("settings"));
    expect(result.current.settingsOpen).toBe(true);
    expect(result.current.settingsDirPath).toBeNull();
    expect(result.current.settingsSection).toBeNull();
  });

  it("openSettings toggles settings, closing the others, with the given or no dir path", () => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => result.current.togglePanel("shares"));
    act(() => result.current.openSettings("/d"));
    expect(openPanels(result.current)).toEqual(["settings"]);
    expect(result.current.settingsDirPath).toBe("/d");
    act(() => result.current.openSettings());
    expect(openPanels(result.current)).toEqual([]);
    expect(result.current.settingsDirPath).toBeNull();
  });

  it("openConfig opens on a dir, switches dirs while open, and closes on the same dir", () => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => result.current.togglePanel("containers"));
    act(() => result.current.openConfig("/a"));
    expect(openPanels(result.current)).toEqual(["settings"]);
    expect(result.current.settingsDirPath).toBe("/a");
    act(() => result.current.openConfig("/b"));
    expect(result.current.settingsOpen).toBe(true);
    expect(result.current.settingsDirPath).toBe("/b");
    act(() => result.current.openConfig("/b"));
    expect(result.current.settingsOpen).toBe(false);
    expect(result.current.settingsSection).toBeNull();
  });

  it("openProjectTrust and openContainersSettings open settings on their section", () => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => result.current.togglePanel("workflows"));
    act(() => result.current.openProjectTrust("/p"));
    expect(openPanels(result.current)).toEqual(["settings"]);
    expect(result.current.settingsSection).toBe("__project_json__");
    act(() => result.current.openProjectTrust("/p"));
    expect(result.current.settingsOpen).toBe(true);
    act(() => result.current.togglePanel("tasks"));
    act(() => result.current.openContainersSettings());
    expect(openPanels(result.current)).toEqual(["settings"]);
    expect(result.current.settingsDirPath).toBeNull();
    expect(result.current.settingsSection).toBe("Containers");
  });

  it("toggleSettingsKeyboard closes others only when opening", () => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => result.current.togglePanel("tasks"));
    act(() => result.current.toggleSettingsKeyboard());
    expect(openPanels(result.current)).toEqual(["settings"]);
    act(() => result.current.openProjectTrust("/p"));
    act(() => result.current.toggleSettingsKeyboard());
    expect(openPanels(result.current)).toEqual([]);
    expect(result.current.settingsDirPath).toBeNull();
    expect(result.current.settingsSection).toBeNull();
  });

  it("forceOpenSettings opens settings even when already open, with no dir path", () => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => result.current.openConfig("/a"));
    act(() => result.current.forceOpenSettings());
    expect(openPanels(result.current)).toEqual(["settings"]);
    expect(result.current.settingsDirPath).toBeNull();
    act(() => result.current.togglePanel("readme"));
    act(() => result.current.forceOpenSettings());
    expect(openPanels(result.current)).toEqual(["settings"]);
  });

  it.each(PANELS)("closePanel($name) closes only that panel", ({ name }) => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => result.current.togglePanel(name));
    act(() => result.current.closePanel(name === "readme" ? "tasks" : "readme"));
    expect(openPanels(result.current)).toEqual([name]);
    act(() => result.current.closePanel(name));
    expect(openPanels(result.current)).toEqual([]);
  });

  it("closeAllPanels closes everything and clears configDirty; the setters update state", () => {
    const { result } = renderHook(() => useAppPanelState());
    act(() => {
      result.current.setConfigDirty(true);
      result.current.setPendingSelectId("ch");
    });
    act(() => result.current.togglePanel("shares"));
    expect(result.current.configDirty).toBe(true);
    expect(result.current.pendingSelectId).toBe("ch");
    act(() => result.current.closeAllPanels());
    expect(openPanels(result.current)).toEqual([]);
    expect(result.current.configDirty).toBe(false);
    expect(result.current.pendingSelectId).toBe("ch");
  });
});
