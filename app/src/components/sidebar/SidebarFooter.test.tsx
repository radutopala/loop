import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { css, expectHover } from "../../test/chat-bits-sidebar-chrome-hover";
import { darkColors as c } from "../../theme";
import type { UpdateStatus } from "../../types";
import { SidebarFooter } from "./SidebarFooter";

const button = (name: string | RegExp) => screen.getByRole("button", { name }) as HTMLButtonElement;

describe("SidebarFooter", () => {
  it.each([
    ["Settings", "onOpenSettings"],
    ["Tasks", "onOpenTasks"],
    ["Workflows", "onOpenWorkflows"],
    ["Playground Shares", "onOpenShares"],
    ["Containers", "onOpenContainers"],
    ["README", "onOpenReadme"],
  ] as const)("%s calls %s and lights up on hover", async (label, prop) => {
    const handler = vi.fn();
    render(<SidebarFooter {...{ [prop]: handler }} />);
    const btn = button(label);
    expectHover(btn, { backgroundColor: c.hoverBg, color: c.textLight }, { backgroundColor: "transparent", color: c.textDim });
    await userEvent.click(btn);
    expect(handler).toHaveBeenCalledOnce();
  });

  it("shows no update, image or share extras by default", () => {
    render(<SidebarFooter shareCount={0} />);
    expect(screen.queryByText(/Update|Image|Claude update/)).toBeNull();
    expect(screen.queryByTestId("sidebar-shares-count")).toBeNull();
    expect(screen.getAllByRole("button")).toHaveLength(6);
  });

  it.each([
    [1, "1 playground publicly shared"],
    [3, "3 playgrounds publicly shared"],
  ])("counts %i shared playground(s)", (n, title) => {
    render(<SidebarFooter shareCount={n} />);
    const badge = screen.getByTestId("sidebar-shares-count");
    expect(badge.textContent).toBe(String(n));
    expect(badge.title).toBe(title);
  });

  describe("app update", () => {
    const status = (s: Partial<UpdateStatus>): UpdateStatus => ({ available: true, downloading: false, downloaded: false, ...s });

    function renderUpdate(s: Partial<UpdateStatus>) {
      const onDownloadUpdate = vi.fn();
      const onInstallUpdate = vi.fn();
      render(<SidebarFooter updateStatus={status(s)} onDownloadUpdate={onDownloadUpdate} onInstallUpdate={onInstallUpdate} />);
      return { onDownloadUpdate, onInstallUpdate };
    }

    it("hides when no update is available", () => {
      render(<SidebarFooter updateStatus={{ available: false, downloading: false, downloaded: false }} />);
      expect(screen.queryByText(/Update/)).toBeNull();
    });

    it.each([
      [{}, "Update available"],
      [{ version: "1.2.3" }, "Update available v1.2.3"],
      [{ error: "net" }, "Update failed — click to retry"],
    ])("downloads from %j labelled %s", async (s, label) => {
      const { onDownloadUpdate, onInstallUpdate } = renderUpdate(s);
      const btn = button(label);
      expect(btn.style.color).toBe(css(c.active));
      expectHover(btn, { backgroundColor: c.hoverBg }, { backgroundColor: "transparent" });
      await userEvent.click(btn);
      expect(onDownloadUpdate).toHaveBeenCalledOnce();
      expect(onInstallUpdate).not.toHaveBeenCalled();
    });

    it("installs a downloaded update", async () => {
      const { onDownloadUpdate, onInstallUpdate } = renderUpdate({ downloaded: true });
      const btn = button("Restart to update");
      expect(btn.style.color).toBe(css(c.active));
      await userEvent.click(btn);
      expect(onInstallUpdate).toHaveBeenCalledOnce();
      expect(onDownloadUpdate).not.toHaveBeenCalled();
    });

    it("is disabled and dim while downloading, with no hover", () => {
      renderUpdate({ downloading: true });
      const btn = button("Downloading...");
      expect(btn.disabled).toBe(true);
      expect(btn.style.color).toBe(css(c.textDim));
      expect(btn.style.cursor).toBe("default");
      expectHover(btn, { backgroundColor: "transparent" }, { backgroundColor: "transparent" });
    });
  });

  describe("container image", () => {
    const update = { current_version: "1", latest_version: "2", component: "claude" };

    it("shows a build in progress instead of the update", () => {
      render(<SidebarFooter imageBuildStatus={{ state: "building" }} imageUpdateAvailable={update} />);
      expect(screen.getByText("Building image...")).toBeTruthy();
      expect(screen.queryByText("Claude update available")).toBeNull();
    });

    it("shows a failed build instead of the update", () => {
      render(<SidebarFooter imageBuildStatus={{ state: "failed" }} imageUpdateAvailable={update} />);
      expect(screen.getByText("Image build failed")).toBeTruthy();
      expect(screen.queryByText("Claude update available")).toBeNull();
    });

    it.each([[undefined], [{ state: "completed" as const }]])("offers a rebuild for an available update with build status %j", async (build) => {
      const onRebuildImage = vi.fn();
      render(<SidebarFooter imageBuildStatus={build} imageUpdateAvailable={update} onRebuildImage={onRebuildImage} />);
      const btn = button("Claude update available");
      expectHover(btn, { backgroundColor: c.hoverBg }, { backgroundColor: "transparent" });
      await userEvent.click(btn);
      expect(onRebuildImage).toHaveBeenCalledOnce();
    });
  });
});
