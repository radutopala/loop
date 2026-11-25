import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type BackupStatus, createBackupKey, fetchBackupStatus, startBackup } from "../../api/backup";
import { css } from "../../test/shared-colors";
import { darkColors } from "../../theme";
import { writeClipboard } from "../../utils/copyToClipboard";
import { BackupBar } from "./BackupBar";

vi.mock("../../api/backup", () => ({ fetchBackupStatus: vi.fn(), startBackup: vi.fn(), createBackupKey: vi.fn() }));
vi.mock("../../utils/copyToClipboard", () => ({ writeClipboard: vi.fn() }));

const statusMock = vi.mocked(fetchBackupStatus);
const startMock = vi.mocked(startBackup);
const keyMock = vi.mocked(createBackupKey);
const clipMock = vi.mocked(writeClipboard);

const base: BackupStatus = {
  enabled: true,
  dir: "/backups",
  times_per_day: 2,
  keep_last: 3,
  keep_daily: 7,
  keep_weekly: 4,
  keep_monthly: 6,
  running: false,
  count: 0,
  total_size: 0,
};

async function renderBar(props: Partial<Parameters<typeof BackupBar>[0]> = {}) {
  const onKeyCreated = vi.fn();
  const utils = render(<BackupBar colors={darkColors} refreshKey={0} dirty={false} onKeyCreated={onKeyCreated} {...props} />);
  await screen.findByTestId("backup-bar");
  return { ...utils, onKeyCreated };
}

describe("BackupBar", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    statusMock.mockResolvedValue(base);
    startMock.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("renders nothing until the status loads, and logs a failed load", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    statusMock.mockRejectedValue(new Error("down"));
    const { container } = render(<BackupBar colors={darkColors} refreshKey={0} dirty={false} onKeyCreated={vi.fn()} />);
    await waitFor(() => expect(warn).toHaveBeenCalledWith("[loop] fetching backup status:", expect.any(Error)));
    expect(container.innerHTML).toBe("");
  });

  it("shows the summary and an unencrypted warning, and re-reads on refreshKey", async () => {
    const { rerender } = await renderBar();
    expect(screen.getByTestId("backup-summary").textContent).toBe("No backups in /backups yet");
    expect(screen.getByTestId("backup-create-key").textContent).toBe("Create key");
    expect(screen.getByTestId("backup-encryption").style.color).toBe(css(darkColors.warning));
    expect(screen.getByTestId("backup-now")).toHaveProperty("disabled", false);
    rerender(<BackupBar colors={darkColors} refreshKey={1} dirty={false} onKeyCreated={vi.fn()} />);
    await waitFor(() => expect(statusMock).toHaveBeenCalledTimes(2));
  });

  it("can't back up without a directory", async () => {
    statusMock.mockResolvedValue({ ...base, dir: undefined });
    await renderBar();
    const now = screen.getByTestId("backup-now");
    expect(now).toHaveProperty("disabled", true);
    expect(now.style.opacity).toBe("0.6");
  });

  it("backs up now, then re-reads the status", async () => {
    await renderBar();
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-now"));
    });
    expect(startMock).toHaveBeenCalledOnce();
    expect(statusMock).toHaveBeenCalledTimes(2);
  });

  it("shows a failed start, cleared by the next attempt", async () => {
    startMock.mockRejectedValueOnce(new Error("no space left"));
    await renderBar();
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-now"));
    });
    expect(screen.getByTestId("backup-error").textContent).toBe("no space left");
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-now"));
    });
    expect(screen.queryByTestId("backup-error")).toBeNull();
  });

  it("polls while a backup runs and stops once it's done", async () => {
    vi.useFakeTimers();
    statusMock
      .mockResolvedValueOnce({ ...base, running: true })
      .mockResolvedValueOnce({ ...base, running: true })
      .mockResolvedValue(base);
    render(<BackupBar colors={darkColors} refreshKey={0} dirty={false} onKeyCreated={vi.fn()} />);
    await act(async () => {});
    expect(screen.getByTestId("backup-now").textContent).toBe("Backing up…");
    expect(screen.getByTestId("backup-now")).toHaveProperty("disabled", true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(statusMock).toHaveBeenCalledTimes(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(statusMock).toHaveBeenCalledTimes(3);
    expect(screen.getByTestId("backup-now").textContent).toBe("Back up now");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    expect(statusMock).toHaveBeenCalledTimes(3);
  });

  it("shows the daemon's last backup error with its time", async () => {
    const at = "2026-10-14T08:00:00Z";
    statusMock.mockResolvedValue({ ...base, error: "disk full", error_at: at });
    await renderBar();
    expect(screen.getByTestId("backup-error").textContent).toBe(`Failed ${new Date(at).toLocaleString()}: disk full`);
  });

  it("shows the daemon's error without a time", async () => {
    statusMock.mockResolvedValue({ ...base, error: "disk full" });
    await renderBar();
    expect(screen.getByTestId("backup-error").textContent).toBe("disk full");
  });

  it("can't create a key with unsaved edits", async () => {
    await renderBar({ dirty: true });
    const button = screen.getByTestId("backup-create-key");
    expect(button).toHaveProperty("disabled", true);
    expect(button.title).toBe("Save your changes first");
  });

  it("creates a key, shows the private key once, copies it and hides it", async () => {
    keyMock.mockResolvedValue({ recipient: "age1pub", identity: "AGE-SECRET-KEY-1" });
    clipMock.mockResolvedValue(undefined);
    const { onKeyCreated } = await renderBar();
    expect(screen.getByTestId("backup-create-key").title).toBe("");
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-create-key"));
    });
    expect(onKeyCreated).toHaveBeenCalledOnce();
    expect(statusMock).toHaveBeenCalledTimes(2);
    expect(screen.getByTestId("backup-private-key-value").textContent).toBe("AGE-SECRET-KEY-1");
    expect(screen.getByTestId("backup-copy-key").textContent).toBe("Copy");
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-copy-key"));
    });
    expect(clipMock).toHaveBeenCalledWith("AGE-SECRET-KEY-1");
    expect(screen.getByTestId("backup-copy-key").textContent).toBe("Copied");
    fireEvent.click(screen.getByTestId("backup-hide-key"));
    expect(screen.queryByTestId("backup-private-key")).toBeNull();
  });

  it("logs a failed copy", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    keyMock.mockResolvedValue({ recipient: "age1pub", identity: "SECRET" });
    clipMock.mockRejectedValue(new Error("blocked"));
    await renderBar();
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-create-key"));
    });
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-copy-key"));
    });
    expect(warn).toHaveBeenCalledWith("[loop] copying the private key:", expect.any(Error));
    expect(screen.getByTestId("backup-copy-key").textContent).toBe("Copy");
  });

  it("asks before replacing an existing key, and does nothing when declined", async () => {
    statusMock.mockResolvedValue({ ...base, recipient: "age1current" });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    await renderBar();
    expect(screen.getByTestId("backup-create-key").textContent).toBe("Create a new key");
    expect(screen.getByTestId("backup-encryption").textContent).toBe("Encrypted to the public key age1current");
    expect(screen.getByTestId("backup-encryption").style.color).toBe(css(darkColors.textDim));
    fireEvent.click(screen.getByTestId("backup-create-key"));
    expect(confirm).toHaveBeenCalledOnce();
    expect(keyMock).not.toHaveBeenCalled();
  });

  it("replaces a key when confirmed, and shows a failure", async () => {
    statusMock.mockResolvedValue({ ...base, recipient: "age1current", error: "old", error_at: "2026-10-14T08:00:00Z" });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    keyMock.mockRejectedValue(new Error("keygen failed"));
    await renderBar();
    await act(async () => {
      fireEvent.click(screen.getByTestId("backup-create-key"));
    });
    expect(keyMock).toHaveBeenCalledOnce();
    // The start error wins over the daemon's, without the daemon's time.
    expect(screen.getByTestId("backup-error").textContent).toBe("keygen failed");
  });
});
