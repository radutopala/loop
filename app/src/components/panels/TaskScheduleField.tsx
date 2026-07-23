import type { CSSProperties } from "react";
import { datetimeLocalToRFC3339, type IntervalUnit, intervalPartsToString, parseIntervalToParts, rfc3339ToDatetimeLocal } from "../../utils/taskUtils";

export type TaskType = "cron" | "interval" | "once" | "manual";

interface TaskScheduleFieldProps {
  type: TaskType;
  value: string;
  onChange: (value: string) => void;
  inputStyle: CSSProperties;
  selectStyle: CSSProperties;
}

/**
 * The schedule input for a scheduled-task form, shaped by the task type:
 * a raw text box for cron, a number + unit picker for interval (stored as a
 * Go duration), a native date-time picker for "once" (stored as RFC3339 UTC),
 * and nothing at all for "manual" (which has no schedule). Shared by
 * TasksPanel and GlobalTasksPanel so the conversions live in one place.
 */
export function TaskScheduleField({ type, value, onChange, inputStyle, selectStyle }: TaskScheduleFieldProps) {
  if (type === "manual") return null;

  if (type === "once") {
    return <input type="datetime-local" value={rfc3339ToDatetimeLocal(value)} onChange={(e) => onChange(datetimeLocalToRFC3339(e.target.value))} style={{ ...inputStyle, flex: 2 }} />;
  }

  if (type === "interval") {
    const { value: amount, unit } = parseIntervalToParts(value);
    return (
      <div style={{ display: "flex", gap: 6, flex: 2 }}>
        <input
          type="number"
          min={1}
          data-testid="task-interval-value"
          value={amount}
          onChange={(e) => onChange(intervalPartsToString(Number(e.target.value), unit))}
          style={{ ...inputStyle, flex: 1 }}
        />
        <select data-testid="task-interval-unit" value={unit} onChange={(e) => onChange(intervalPartsToString(amount, e.target.value as IntervalUnit))} style={{ ...selectStyle, flex: 1 }}>
          <option value="s">seconds</option>
          <option value="m">minutes</option>
          <option value="h">hours</option>
          <option value="d">days</option>
        </select>
      </div>
    );
  }

  // cron: raw 5-field expression.
  return <input type="text" placeholder="*/30 * * * *" value={value} onChange={(e) => onChange(e.target.value)} style={{ ...inputStyle, flex: 2 }} />;
}
