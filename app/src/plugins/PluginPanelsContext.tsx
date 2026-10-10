import { createContext, useContext, useEffect, useMemo, useState } from "react";
import { fetchPlugins, type PluginInfo, type PluginPanelInfo } from "../api/plugins";
import type { PaneNode, PluginPaneRef } from "../types/panels";
import { logErr } from "../utils/log";

/** The installed plugins' panels, and which of them the layout shows. */
export interface PluginPanelsValue {
  /** null until they're listed. */
  plugins: PluginInfo[] | null;
  /** pluginPaneKey of every plugin pane in the layout. */
  shown: Set<string>;
}

const PluginPanelsContext = createContext<PluginPanelsValue>({ plugins: [], shown: new Set() });

export const PluginPanelsProvider = PluginPanelsContext.Provider;

export function usePluginPanels(): PluginPanelsValue {
  return useContext(PluginPanelsContext);
}

export function pluginPaneKey(ref: Pick<PluginPaneRef, "id" | "panel">): string {
  return `${ref.id}/${ref.panel}`;
}

/** The installed plugins, fetched once per mount; null until they are. */
export function useInstalledPlugins(): PluginInfo[] | null {
  const [plugins, setPlugins] = useState<PluginInfo[] | null>(null);
  useEffect(() => {
    let cancelled = false;
    fetchPlugins()
      .then((p) => {
        if (!cancelled) setPlugins(p);
      })
      .catch((err) => {
        logErr("listing plugins")(err);
        if (!cancelled) setPlugins([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);
  return plugins;
}

/** pluginPaneKey of the plugin panes in tree. */
export function shownPluginPanes(tree: PaneNode | null | undefined): Set<string> {
  const shown = new Set<string>();
  const walk = (n: PaneNode) => {
    if (n.type === "leaf") {
      if (n.panel === "plugin" && n.plugin) shown.add(pluginPaneKey(n.plugin));
    } else n.children.forEach(walk);
  };
  if (tree) walk(tree);
  return shown;
}

/** The value for PluginPanelsProvider. */
export function usePluginPanelsValue(plugins: PluginInfo[] | null, shown: Set<string>): PluginPanelsValue {
  const key = [...shown].sort().join("\0");
  // key stands for shown's contents, so a new Set with the same panes keeps the value.
  return useMemo(() => ({ plugins, shown }), [plugins, key]);
}

/** An entry of an add-pane menu for a plugin panel. */
export interface PluginPaneOption {
  key: string;
  ref: PluginPaneRef;
  description: string;
  /** A singleton panel the layout already shows. */
  disabled: boolean;
}

/** The add-pane menu entries for the installed plugins' panels. */
export function pluginPaneOptions({ plugins, shown }: PluginPanelsValue): PluginPaneOption[] {
  return (plugins ?? []).flatMap((p) =>
    (p.panels ?? []).map((panel) => {
      const ref: PluginPaneRef = { id: p.id, panel: panel.id, label: panel.label || p.name || p.id };
      const key = pluginPaneKey(ref);
      return { key, ref, description: `${p.name || p.id} plugin`, disabled: !!panel.singleton && shown.has(key) };
    }),
  );
}

/** The installed plugin and panel a plugin pane shows, if both still exist. */
export function findPluginPanel(plugins: PluginInfo[] | null, ref: PluginPaneRef | undefined): { plugin: PluginInfo; panel: PluginPanelInfo } | null {
  if (!ref) return null;
  const plugin = plugins?.find((p) => p.id === ref.id);
  const panel = plugin?.panels?.find((x) => x.id === ref.panel);
  return plugin && panel ? { plugin, panel } : null;
}
