/**
 * Shared formatting helpers for the scheduled-task panels (TasksPanel and
 * GlobalTasksPanel): relative timestamps for past runs, next-run countdown
 * labels, and the per-schedule-type accent colors.
 */

export function timeAgo(dateStr: string): string {
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "-";
  const diff = Date.now() - d.getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 0) return `in ${-mins}m`;
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export function nextRunLabel(dateStr: string): string {
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "-";
  const diff = d.getTime() - Date.now();
  if (diff < 0) return "overdue";
  const mins = Math.floor(diff / 60000);
  if (mins < 60) return `in ${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `in ${hours}h`;
  const days = Math.floor(hours / 24);
  return `in ${days}d`;
}

export const TYPE_COLORS: Record<string, string> = {
  cron: "#818cf8",
  interval: "#34d399",
  once: "#fbbf24",
  manual: "#94a3b8",
};

/** Default schedule string seeded when the task type changes in a form. */
export function defaultScheduleForType(type: string): string {
  switch (type) {
    case "cron":
      return "*/30 * * * *";
    case "interval":
      return "30m";
    default:
      return ""; // once → filled via the time picker; manual → no schedule
  }
}

/**
 * Convert a stored RFC3339 schedule (e.g. "2026-06-11T09:00:00Z") to the
 * "YYYY-MM-DDTHH:mm" form an <input type="datetime-local"> expects, in the
 * browser's local timezone. Returns "" for empty/unparseable input.
 */
export function rfc3339ToDatetimeLocal(rfc: string): string {
  if (!rfc) return "";
  const d = new Date(rfc);
  if (isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/**
 * Convert a datetime-local value (local time, no zone) back to an RFC3339 UTC
 * timestamp (e.g. "2026-06-11T06:00:00Z") for the API. Returns "" for empty/
 * unparseable input.
 */
export function datetimeLocalToRFC3339(local: string): string {
  if (!local) return "";
  const d = new Date(local);
  if (isNaN(d.getTime())) return "";
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}
