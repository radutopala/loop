import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { fetchPlugins, PLUGINS_CHANGED, type PluginCardInfo, type PluginInfo, type PluginPanelInfo } from "../api/plugins";
import { useEventStream } from "../hooks/useEventStream";
import type { WSEvent } from "../types";
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

/** The installed plugins as channelId's project sees them, fetched again
 *  when they or their enablement change; null until they're listed. */
export function useInstalledPlugins(channelId: string | null): PluginInfo[] | null {
  const [plugins, setPlugins] = useState<PluginInfo[] | null>(null);
  const [epoch, setEpoch] = useState(0);
  const refetch = useCallback(() => setEpoch((n) => n + 1), []);
  useEffect(() => {
    let cancelled = false;
    fetchPlugins(channelId)
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
  }, [channelId, epoch]);
  useEffect(() => {
    window.addEventListener(PLUGINS_CHANGED, refetch);
    return () => window.removeEventListener(PLUGINS_CHANGED, refetch);
  }, [refetch]);
  const onEvent = useCallback(
    (ev: WSEvent) => {
      if (ev.type === "plugins.changed") refetch();
    },
    [refetch],
  );
  useEventStream({ channelId, onEvent });
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

/** The add-pane menu entries for the panels of the installed plugins the
 *  project enables. */
export function pluginPaneOptions({ plugins, shown }: PluginPanelsValue): PluginPaneOption[] {
  return (plugins ?? [])
    .filter((p) => p.enabled !== false)
    .flatMap((p) =>
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

/** The installed plugin and card a card shows, if both still exist. */
export function findPluginCard(plugins: PluginInfo[] | null, pluginId: string, cardId: string): { plugin: PluginInfo; card: PluginCardInfo } | null {
  const plugin = plugins?.find((p) => p.id === pluginId);
  const card = plugin?.cards?.find((c) => c.id === cardId);
  return plugin && card ? { plugin, card } : null;
}
