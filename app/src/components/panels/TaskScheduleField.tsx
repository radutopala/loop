import type { CSSProperties } from "react";
import { rfc3339ToDatetimeLocal, datetimeLocalToRFC3339 } from "../../utils/taskUtils";

export type TaskType = "cron" | "interval" | "once" | "manual";

interface TaskScheduleFieldProps {
  type: TaskType;
  value: string;
  onChange: (value: string) => void;
  inputStyle: CSSProperties;
}

/**
 * The schedule input for a scheduled-task form, shaped by the task type:
 * a text box for cron/interval, a native date-time picker for "once" (stored
 * as RFC3339 UTC), and nothing at all for "manual" (which has no schedule).
 * Shared by TasksPanel and GlobalTasksPanel so the once↔RFC3339 conversion
 * lives in one place.
 */
export function TaskScheduleField({ type, value, onChange, inputStyle }: TaskScheduleFieldProps) {
  if (type === "manual") return null;

  if (type === "once") {
    return (
      <input
        type="datetime-local"
        value={rfc3339ToDatetimeLocal(value)}
        onChange={(e) => onChange(datetimeLocalToRFC3339(e.target.value))}
        style={{ ...inputStyle, flex: 2 }}
      />
    );
  }

  return (
    <input
      type="text"
      placeholder={type === "cron" ? "*/30 * * * *" : "30m"}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      style={{ ...inputStyle, flex: 2 }}
    />
  );
}
