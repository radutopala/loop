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

export type IntervalUnit = "m" | "h" | "d";

/**
 * Parse a Go duration interval string (e.g. "30m", "2h", "48h") into a
 * number + unit for the interval builder. Compound/odd durations collapse to
 * the largest clean unit (e.g. "2h30m" → 150 minutes), which is behaviourally
 * identical for next-run scheduling. Unparseable input falls back to 30m.
 */
export function parseIntervalToParts(dur: string): { value: number; unit: IntervalUnit } {
  const mins = goDurationToMinutes(dur);
  if (mins == null || mins < 1) return { value: 30, unit: "m" };
  if (mins % 1440 === 0) return { value: mins / 1440, unit: "d" };
  if (mins % 60 === 0) return { value: mins / 60, unit: "h" };
  return { value: mins, unit: "m" };
}

/**
 * Compose a number + unit back into a Go duration string. Go has no day unit,
 * so days are emitted as hours (1 day → "24h"); this round-trips through
 * parseIntervalToParts and schedules identically.
 */
export function intervalPartsToString(value: number, unit: IntervalUnit): string {
  const n = Number.isFinite(value) && value >= 1 ? Math.floor(value) : 1;
  if (unit === "d") return `${n * 24}h`;
  return `${n}${unit}`;
}

/** Sum a Go duration string (h/m/s segments) to whole minutes, or null. */
function goDurationToMinutes(dur: string): number | null {
  if (!dur) return null;
  const re = /(\d+(?:\.\d+)?)(h|m|s)/g;
  let total = 0;
  let consumed = 0;
  let matched = false;
  let m: RegExpExecArray | null;
  while ((m = re.exec(dur)) !== null) {
    matched = true;
    consumed += m[0].length;
    const n = parseFloat(m[1] ?? "0");
    total += m[2] === "h" ? n * 60 : m[2] === "m" ? n : n / 60;
  }
  if (!matched || consumed !== dur.length) return null;
  return Math.round(total);
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
