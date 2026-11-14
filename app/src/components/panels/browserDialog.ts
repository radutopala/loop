/**
 * A JavaScript dialog blocking a tab. Headless Chrome never shows one — the
 * page just stops until it is answered — so the pane shows it instead.
 * Alerts never get here: the backend accepts them on its own.
 */
export interface PageDialog {
  target_id: string;
  type: string; // "confirm", "prompt" or "beforeunload"
  message: string;
  default_prompt?: string;
  url?: string;
}

/** The dialog each tab is blocked on, by target ID. */
export type PageDialogs = Readonly<Record<string, PageDialog>>;

export function withDialog(dialogs: PageDialogs, d: PageDialog): PageDialogs {
  return { ...dialogs, [d.target_id]: d };
}

/** Drops targetId's dialog; returns dialogs itself when it had none. */
export function withoutDialog(dialogs: PageDialogs, targetId: string): PageDialogs {
  if (!(targetId in dialogs)) return dialogs;
  const { [targetId]: _, ...rest } = dialogs;
  return rest;
}

/** What the bar says above the page's own message. */
export function dialogHeading(d: PageDialog): string {
  switch (d.type) {
    case "prompt":
      return "The page asks:";
    case "beforeunload":
      return "Leave this page? Changes you made may not be saved.";
    default:
      return "The page asks you to confirm:";
  }
}

/** The labels on the accept and dismiss buttons. */
export function dialogButtons(d: PageDialog): { accept: string; dismiss: string } {
  return d.type === "beforeunload" ? { accept: "Leave", dismiss: "Stay" } : { accept: "OK", dismiss: "Cancel" };
}
