import type { MenuItem } from "../shared/ContextMenu";

/** The options of a <select> the user clicked in the page, as the backend reports them. */
export interface PageSelectMenu {
  selected: number;
  options: { label: string; disabled?: boolean; group?: string }[];
}

/**
 * selectMenuItems builds the dropdown headless Chrome opens where the
 * screencast cannot show it. Disabled options are left out; each keeps its
 * index in the page's list, so the pick names the right one. The first option
 * of each <optgroup> starts a group, with the group's label as its hint.
 */
export function selectMenuItems(menu: PageSelectMenu, choose: (index: number) => void): MenuItem[] {
  const items: MenuItem[] = [];
  let group: string | undefined;
  menu.options.forEach((o, i) => {
    if (o.disabled) return;
    const g = o.group || undefined;
    const starts = g !== group;
    group = g;
    items.push({
      label: o.label,
      onClick: () => choose(i),
      checked: i === menu.selected,
      ...(starts && items.length > 0 ? { separator: true } : {}),
      ...(starts && g ? { hint: g } : {}),
    });
  });
  return items;
}
