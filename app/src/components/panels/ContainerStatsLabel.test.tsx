import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ContainerStatsLabel } from "./ContainerStatsLabel";

describe("ContainerStatsLabel", () => {
  it("renders nothing without an entry", () => {
    const { container } = render(<ContainerStatsLabel />);
    expect(container.innerHTML).toBe("");
  });

  it("shows rounded CPU and memory with the full figures in its title", () => {
    render(<ContainerStatsLabel entry={{ container_id: "abcdef0123456789", type: "agent", cpu_percent: 12.46, mem_usage: 300 * 1024 * 1024, mem_limit: 2 * 1024 * 1024 * 1024 }} />);
    const label = screen.getByTestId("container-stats");
    expect(label.textContent).toBe("12% · 300M");
    expect(label.getAttribute("title")).toBe("Container abcdef012345 — CPU 12.5%, memory 300M of 2.0G");
  });
});
