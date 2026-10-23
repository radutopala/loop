import { describe, expect, it } from "vitest";
import { type MountToggle, mountTarget, mountToggles, syncConfigMounts } from "./configMounts";

const GH: MountToggle = { key: "github.mount_gh_config", mount: "~/.config/gh:~/.config/gh" };
const GLAB: MountToggle = { key: "gitlab.mount_glab_config", mount: "~/Library/Application Support/glab-cli:~/.config/glab-cli", sourceKey: "gitlab.glab_config_dir" };
const toggles = [GH, GLAB];

describe("mountToggles", () => {
  it("collects nested x-mount fields", () => {
    expect(
      mountToggles({
        mounts: { type: "array" },
        github: { type: "object", properties: { gh_user: { type: "string" }, mount_gh_config: { type: "boolean", "x-mount": GH.mount } } },
        gitlab: { type: "object", properties: { mount_glab_config: { type: "boolean", "x-mount": GLAB.mount, "x-mount-source": "gitlab.glab_config_dir" } } },
      }),
    ).toEqual([
      { key: "github.mount_gh_config", mount: GH.mount, sourceKey: undefined },
      { key: "gitlab.mount_glab_config", mount: GLAB.mount, sourceKey: "gitlab.glab_config_dir" },
    ]);
    expect(mountToggles(undefined)).toEqual([]);
  });
});

describe("mountTarget", () => {
  it.each([
    ["~/.config/gh:~/.config/gh", "~/.config/gh"],
    ["/a:/b/:ro", "/b"],
    ["/a:/b:rw", "/b"],
    ["vol:/data", "/data"],
    ["bad", ""],
  ])("%s → %s", (mount, want) => {
    expect(mountTarget(mount)).toBe(want);
  });
});

describe("syncConfigMounts", () => {
  const on = (cfg: Record<string, any>, key: string, v: any) => {
    const [a, b] = key.split(".") as [string, string];
    return { ...cfg, [a]: { ...cfg[a], [b]: v } };
  };

  it("adds the entry when turned on", () => {
    const prev = { mounts: ["/a:/a"] };
    expect(syncConfigMounts(prev, on(prev, GH.key, true), GH.key, toggles).mounts).toEqual(["/a:/a", GH.mount]);
  });

  it("creates the list when there is none", () => {
    expect(syncConfigMounts({}, on({}, GLAB.key, true), GLAB.key, toggles).mounts).toEqual([GLAB.mount]);
  });

  it("skips a mount already at the path or inside it", () => {
    const own = ["/srv/gh:~/.config/gh/:ro", "~/Library/Application Support/glab-cli/config.yml:~/.config/glab-cli/config.yml:ro"];
    let cfg: Record<string, any> = { mounts: own };
    cfg = syncConfigMounts(cfg, on(cfg, GH.key, true), GH.key, toggles);
    cfg = syncConfigMounts(cfg, on(cfg, GLAB.key, true), GLAB.key, toggles);
    expect(cfg.mounts).toBe(own);
  });

  it("does not take a sibling path as covering", () => {
    const prev = { mounts: ["/x:~/.config/ghost"] };
    expect(syncConfigMounts(prev, on(prev, GH.key, true), GH.key, toggles).mounts).toEqual(["/x:~/.config/ghost", GH.mount]);
  });

  it("skips an entry the project inherits", () => {
    const next = on({}, GH.key, true);
    expect(syncConfigMounts({}, next, GH.key, toggles, [GH.mount])).toBe(next);
  });

  it("removes the mounts at the path when turned off", () => {
    const prev = { github: { mount_gh_config: true }, mounts: ["/a:/a", GH.mount, "/srv/gh/hosts.yml:~/.config/gh/hosts.yml"] };
    expect(syncConfigMounts(prev, on(prev, GH.key, false), GH.key, toggles).mounts).toEqual(["/a:/a"]);
  });

  it("drops the list when the last entry goes", () => {
    const prev = { github: { mount_gh_config: true }, mounts: [GH.mount] };
    expect(syncConfigMounts(prev, on(prev, GH.key, undefined), GH.key, toggles)).toEqual({ github: { mount_gh_config: undefined } });
  });

  it("follows the source dir while on", () => {
    let cfg: Record<string, any> = { gitlab: { mount_glab_config: true }, mounts: ["/a:/a", GLAB.mount] };
    cfg = syncConfigMounts(cfg, on(cfg, "gitlab.glab_config_dir", "~/glab-work"), "gitlab.glab_config_dir", toggles);
    expect(cfg.mounts).toEqual(["/a:/a", "~/glab-work:~/.config/glab-cli"]);
    cfg = syncConfigMounts(cfg, on(cfg, "gitlab.glab_config_dir", ""), "gitlab.glab_config_dir", toggles);
    expect(cfg.mounts).toEqual(["/a:/a", GLAB.mount]);
  });

  it("leaves mounts alone for a source change while off and for other fields", () => {
    const prev = { mounts: ["/a:/a"] };
    const srcChange = on(prev, "gitlab.glab_config_dir", "~/w");
    expect(syncConfigMounts(prev, srcChange, "gitlab.glab_config_dir", toggles)).toBe(srcChange);
    const other = { ...prev, claude_model: "x" };
    expect(syncConfigMounts(prev, other, "claude_model", toggles)).toBe(other);
  });
});
