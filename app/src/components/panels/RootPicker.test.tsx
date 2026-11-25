import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { RootEntry } from "../../api/files";
import { RootPicker } from "./RootPicker";

const roots: RootEntry[] = [
  { index: 0, path: "/home/u/proj", display_path: "~/proj", name: "proj" },
  { index: 1, path: "/srv/lib", name: "lib" },
];

describe("RootPicker", () => {
  it("shows the current root's display path and its full path with the title", () => {
    render(<RootPicker roots={roots} value={0} onChange={vi.fn()} testId="picker" title="Workspace root" />);
    const btn = screen.getByTestId("picker");
    expect(btn.textContent).toBe("~/proj");
    expect(btn.getAttribute("title")).toBe("Workspace root\n/home/u/proj");
    expect(btn.getAttribute("aria-expanded")).toBe("false");
  });

  it("falls back to the raw path and the first root, and omits an absent title", () => {
    render(<RootPicker roots={roots} value={7} onChange={vi.fn()} testId="picker" />);
    expect(screen.getByTestId("picker").getAttribute("title")).toBe("/home/u/proj");
    render(<RootPicker roots={roots} value={1} onChange={vi.fn()} testId="picker2" />);
    expect(screen.getByTestId("picker2").textContent).toBe("/srv/lib");
  });

  it("renders an empty label with only the title when there are no roots", () => {
    render(<RootPicker roots={[]} value={0} onChange={vi.fn()} testId="picker" title="Pick" />);
    const btn = screen.getByTestId("picker");
    expect(btn.textContent).toBe("");
    expect(btn.getAttribute("title")).toBe("Pick");
  });

  it("opens a menu listing each root with the current one checked and picks one", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<RootPicker roots={roots} value={0} onChange={onChange} testId="picker" />);
    await user.click(screen.getByTestId("picker"));
    expect(screen.getByTestId("picker").getAttribute("aria-expanded")).toBe("true");
    const items = screen.getAllByRole("menuitemradio");
    expect(items.map((i) => i.getAttribute("aria-checked"))).toEqual(["true", "false"]);
    expect(items[1]!.textContent).toContain("lib");
    expect(items[1]!.textContent).toContain("/srv/lib");
    await user.click(items[1]!);
    expect(onChange).toHaveBeenCalledWith(1);
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
