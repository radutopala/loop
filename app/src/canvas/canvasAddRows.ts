import { type PluginPanelsValue, pluginPaneOptions } from "../plugins/PluginPanelsContext";
import { PANEL_OPTIONS, type PanelType, type PaneMeta } from "../types/panels";

/** An entry of a canvas add-tile menu. */
export interface CanvasAddRow {
  key: string;
  panel: PanelType;
  label: string;
  meta?: PaneMeta;
  disabled: boolean;
}

/** The canvas add-tile menu entries: the built-in panels, then the installed
 *  plugins' panels. */
export function canvasAddRows(usedSingletons: Set<PanelType> | undefined, plugins: PluginPanelsValue, hiddenPanels?: PanelType[]): CanvasAddRow[] {
  const rows: CanvasAddRow[] = PANEL_OPTIONS.filter((opt) => !hiddenPanels?.includes(opt.panel)).map((opt) => ({
    key: opt.panel,
    panel: opt.panel,
    label: opt.label,
    disabled: !!usedSingletons?.has(opt.panel),
  }));
  for (const o of pluginPaneOptions(plugins)) rows.push({ key: `plugin-${o.key}`, panel: "plugin", label: o.ref.label, meta: { plugin: o.ref }, disabled: o.disabled });
  return rows;
}
