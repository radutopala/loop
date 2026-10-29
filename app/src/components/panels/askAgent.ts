import { sendMessage } from "../../api/channels";
import { getChatInput, getWorkspace } from "../../uiBridge/registry";
import type { ChatPaneInput } from "../../uiBridge/types";

/** How much of the code an ask quotes. */
export const MAX_ASK_CHARS = 4000;

/** A fence for text: longer than any run of backticks in it. */
function fence(text: string): string {
  const longest = Math.max(0, ...[...text.matchAll(/`+/g)].map((m) => m[0].length));
  return "`".repeat(Math.max(3, longest + 1));
}

/**
 * askMessage is the chat message that asks the agent about lines start..end
 * of path: "Explain path:start-end", with what they're about (a walkthrough
 * step's title) when given, then their text in a fenced block, cut to
 * MAX_ASK_CHARS.
 */
export function askMessage(path: string, start: number, end: number, text: string, about?: string): string {
  const lines = start === end ? `${start}` : `${start}-${end}`;
  const cut = text.length > MAX_ASK_CHARS;
  const code = cut ? text.slice(0, MAX_ASK_CHARS) : text;
  const f = fence(code);
  return [`Explain ${path}:${lines}${about ? ` (${about})` : ""}`, "", `${f}\n${code.replace(/\n$/, "")}\n${f}`, ...(cut ? ["", `(The first ${MAX_ASK_CHARS} characters of the lines.)`] : [])].join(
    "\n",
  );
}

export interface AskDeps {
  /** The chat pane of the channel's workspace, if it shows one. */
  chat(channelId: string): ChatPaneInput | undefined;
  send(channelId: string, text: string): Promise<void>;
}

const appDeps: AskDeps = {
  chat: (channelId) => {
    const pane = getWorkspace(channelId)
      ?.view()
      .panes.find((p) => p.panel === "chat");
    return pane ? getChatInput(channelId, pane.id) : undefined;
  },
  send: (channelId, text) => sendMessage(channelId, text),
};

/** askAgent queues message in the channel's chat: as its chat pane's
 *  composer sends it (in the chat's mode) when the workspace shows one. */
export async function askAgent(channelId: string, message: string, deps: AskDeps = appDeps): Promise<void> {
  const chat = deps.chat(channelId);
  if (chat) await chat.send(message);
  else await deps.send(channelId, message);
}
