import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DaemonUnavailable } from "./DaemonUnavailable";

type Bridge = { restartDaemon: () => Promise<unknown> };

function setBridge(bridge: Bridge | undefined) {
  Object.defineProperty(window, "loopAPI", { value: bridge, configurable: true, writable: true });
}

describe("DaemonUnavailable", () => {
  afterEach(() => {
    setBridge(undefined);
  });

  it("offers a plain retry in a browser, showing the error", async () => {
    let finish!: () => void;
    const onRetry = vi.fn(() => new Promise<void>((r) => (finish = r)));
    render(<DaemonUnavailable apiUrl="http://localhost:1234" error="ECONNREFUSED" running={false} onRetry={onRetry} />);
    expect(screen.getByText("Loop daemon isn't running")).toBeTruthy();
    expect(screen.getByText("http://localhost:1234")).toBeTruthy();
    expect(screen.getByText(/which could not be reached/)).toBeTruthy();
    expect(screen.getByText("ECONNREFUSED")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledOnce();
    expect((screen.getByRole("button", { name: "Retrying…" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => finish());
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  it("starts a stopped daemon through the desktop bridge before retrying", async () => {
    const order: string[] = [];
    let finish!: () => void;
    setBridge({
      restartDaemon: vi.fn(() => {
        order.push("restart");
        return new Promise((r) => (finish = () => r({})));
      }),
    });
    const onRetry = vi.fn(async () => {
      order.push("retry");
    });
    render(<DaemonUnavailable apiUrl="u" error="" running={false} onRetry={onRetry} />);
    expect(screen.queryByText("ECONNREFUSED")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Start daemon" }));
    expect(screen.getByRole("button", { name: "Starting…" })).toBeTruthy();
    await act(async () => finish());
    expect(order).toEqual(["restart", "retry"]);
    expect(screen.getByRole("button", { name: "Start daemon" })).toBeTruthy();
  });

  it("offers a restart when the daemon runs but rejected the request", async () => {
    const restart = vi.fn(async () => ({}));
    setBridge({ restartDaemon: restart });
    const onRetry = vi.fn(async () => {});
    render(<DaemonUnavailable apiUrl="u" error="403" running onRetry={onRetry} />);
    expect(screen.getByText("Couldn't load from the Loop daemon")).toBeTruthy();
    expect(screen.getByText(/which is running but rejected the request/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Restart daemon" }));
    expect(restart).toHaveBeenCalledOnce();
    expect(onRetry).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Restart daemon" })).toHaveProperty("disabled", false);
  });
});
