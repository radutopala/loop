import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { datetimeLocalToRFC3339, rfc3339ToDatetimeLocal } from "../../utils/taskUtils";
import { TaskScheduleField, type TaskType } from "./TaskScheduleField";

function renderField(type: TaskType, value: string) {
  const onChange = vi.fn();
  const view = render(<TaskScheduleField type={type} value={value} onChange={onChange} inputStyle={{ color: "red" }} selectStyle={{ color: "blue" }} />);
  return { onChange, ...view };
}

describe("TaskScheduleField", () => {
  it("renders nothing for a manual task", () => {
    const { container } = renderField("manual", "");
    expect(container.innerHTML).toBe("");
  });

  it("shows a cron text box and passes the raw expression through", () => {
    const { onChange } = renderField("cron", "0 * * * *");
    const input = screen.getByPlaceholderText("*/30 * * * *") as HTMLInputElement;
    expect(input.value).toBe("0 * * * *");
    expect(input.style.flexGrow).toBe("2");
    expect(input.style.color).toBe("red");
    fireEvent.change(input, { target: { value: "*/5 * * * *" } });
    expect(onChange).toHaveBeenCalledWith("*/5 * * * *");
  });

  it("shows a date-time picker for a once task and converts to RFC3339", () => {
    const stored = "2026-06-11T09:00:00Z";
    const { onChange, container } = renderField("once", stored);
    const input = container.querySelector('input[type="datetime-local"]') as HTMLInputElement;
    expect(input.value).toBe(rfc3339ToDatetimeLocal(stored));
    fireEvent.change(input, { target: { value: "2026-07-01T10:30" } });
    expect(onChange).toHaveBeenCalledWith(datetimeLocalToRFC3339("2026-07-01T10:30"));
  });

  it("splits an interval into amount and unit and recomposes on change", () => {
    const { onChange } = renderField("interval", "2h");
    const amount = screen.getByTestId("task-interval-value") as HTMLInputElement;
    const unit = screen.getByTestId("task-interval-unit") as HTMLSelectElement;
    expect(amount.value).toBe("2");
    expect(unit.value).toBe("h");
    expect(unit.style.color).toBe("blue");

    fireEvent.change(amount, { target: { value: "5" } });
    expect(onChange).toHaveBeenLastCalledWith("5h");

    fireEvent.change(unit, { target: { value: "d" } });
    expect(onChange).toHaveBeenLastCalledWith("48h");
  });
});
