import { type PluginPanelsValue, pluginPaneOptions } from "../plugins/PluginPanelsContext";
import { AGENT_OPEN_MODE_OPTIONS, EXCLUSIVE_PANELS, PANEL_OPTIONS, type PanelType, type PaneMeta, SINGLETON_PANELS } from "../types/panels";

/** One entry of an add-pane menu. */
export interface AddPaneRow {
  key: string;
  panel: PanelType;
  label: string;
  meta?: PaneMeta;
  /** A singleton the layout already shows, or excluded by one it shows. */
  disabled: boolean;
  /** Set for plugin panels, whose description isn't their panel type's. */
  description?: string;
}

/** The add-pane menu entries: the built-in panels (docker-agent once per
 *  open mode), then the installed plugins' panels. */
export function addPaneRows(used: Set<PanelType>, plugins: PluginPanelsValue, hiddenPanels?: PanelType[]): AddPaneRow[] {
  const rows: AddPaneRow[] = [];
  for (const { panel, label } of PANEL_OPTIONS) {
    if (hiddenPanels?.includes(panel)) continue;
    const disabled = (SINGLETON_PANELS.includes(panel) && used.has(panel)) || EXCLUSIVE_PANELS.some((g) => g.includes(panel) && g.some((x) => x !== panel && used.has(x)));
    if (panel === "docker-agent") {
      for (const m of AGENT_OPEN_MODE_OPTIONS) rows.push({ key: `${panel}-${m.mode}`, panel, label: `${label} (${m.label})`, meta: { openMode: m.mode }, disabled, description: m.description });
    } else {
      rows.push({ key: panel, panel, label, disabled });
    }
  }
  for (const o of pluginPaneOptions(plugins)) {
    rows.push({ key: `plugin-${o.key}`, panel: "plugin", label: o.ref.label, meta: { plugin: o.ref }, disabled: o.disabled, description: o.description });
  }
  return rows;
}
