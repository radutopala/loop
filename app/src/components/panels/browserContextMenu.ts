import type { MenuItem } from "../shared/ContextMenu";

/** What a right-click in the page landed on, as the backend reports it. */
export interface PageContextMenu {
  link?: string;
  image?: string;
  selection?: string;
  editable?: boolean;
}

export interface PageMenuActions {
  openInNewTab: (url: string) => void;
  copy: (text: string) => void;
  paste: () => void;
  back: () => void;
  forward: () => void;
  reload: () => void;
}

// new_tab only opens http(s) pages, so a data: or blob: image can be copied
// but not opened.
function opensInTab(url: string): boolean {
  return /^https?:\/\//i.test(url);
}

/**
 * pageMenuItems builds the menu headless Chrome never draws: link and image
 * entries for what was under the pointer, Copy and Paste where they apply,
 * then page navigation. Each group after the first starts with a separator.
 */
export function pageMenuItems(menu: PageContextMenu, a: PageMenuActions): MenuItem[] {
  const items: MenuItem[] = [];
  const group = (more: MenuItem[]) => {
    const first = items.length === 0;
    more.forEach((m, i) => items.push(i === 0 && !first ? { ...m, separator: true } : m));
  };
  const target = (url: string | undefined, open: string, copy: string): MenuItem[] => {
    if (!url) return [];
    const out: MenuItem[] = [];
    if (opensInTab(url)) out.push({ label: open, onClick: () => a.openInNewTab(url) });
    out.push({ label: copy, onClick: () => a.copy(url) });
    return out;
  };
  group(target(menu.link, "Open link in new tab", "Copy link address"));
  group(target(menu.image, "Open image in new tab", "Copy image address"));
  const edit: MenuItem[] = [];
  const selection = menu.selection;
  if (selection) edit.push({ label: "Copy", onClick: () => a.copy(selection) });
  if (menu.editable) edit.push({ label: "Paste", onClick: a.paste });
  group(edit);
  group([
    { label: "Back", onClick: a.back },
    { label: "Forward", onClick: a.forward },
    { label: "Reload", onClick: a.reload },
  ]);
  return items;
}
