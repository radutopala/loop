/**
 * Formats the timestamp shown under (or above) a chat message.
 *
 * Same-day messages stay time-only — the common case, where a date would just
 * be noise. Anything older gets the date in front of the time, so scrolling
 * back through a channel no longer shows a wall of bare clock times with no
 * hint of which day they belong to. The year is added only when it differs
 * from the current one.
 */
export function formatMessageTimestamp(value: string, now: Date = new Date()): string {
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return "";

  const time = at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const sameDay = at.getFullYear() === now.getFullYear() && at.getMonth() === now.getMonth() && at.getDate() === now.getDate();
  if (sameDay) return time;

  const date = at.toLocaleDateString([], {
    month: "short",
    day: "numeric",
    ...(at.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }),
  });
  return `${date} ${time}`;
}
