/**
 * Links to a chat message: loop://channel/<channel-id>/<message-id>, where the
 * message id is the message's row id in the database. A channel link without
 * the message part is the sidebar's "Copy link".
 *
 * Electron hands the page everything after loop://channel/, and the page
 * keeps the same text in its URL hash, so both parse the same way.
 */
const PREFIX = "loop://channel/";

export interface ChannelTarget {
  channelId: string;
  messageId: number | null;
}

/** The link to a message in a channel. */
export function messageLink(channelId: string, messageId: number): string {
  return `${PREFIX}${channelId}/${messageId}`;
}

/** Parses "<channel-id>" or "<channel-id>/<message-id>" from a URL hash or a deep link. */
export function parseChannelTarget(target: string): ChannelTarget | null {
  const [channelId, messagePart, ...rest] = target.replace(/^#/, "").split("/");
  if (!channelId || rest.length > 0) return null;
  if (messagePart === undefined || messagePart === "") return { channelId, messageId: null };
  if (!/^\d+$/.test(messagePart)) return null;
  const messageId = Number(messagePart);
  return messageId > 0 ? { channelId, messageId } : { channelId, messageId: null };
}

/**
 * The in-app href for a loop://channel/ link, so following it moves the
 * page's hash instead of leaving the app; null for any other URL.
 */
export function inAppHref(url: string): string | null {
  if (!url.startsWith(PREFIX)) return null;
  const target = parseChannelTarget(url.slice(PREFIX.length));
  if (!target) return null;
  return target.messageId === null ? `#${target.channelId}` : `#${target.channelId}/${target.messageId}`;
}
