import { describe, expect, it } from "vitest";
import { reclaimRows, reclaimSummary } from "./Settings";

describe("reclaimRows", () => {
  const estimate = {
    volumes_sized: true,
    build_cache: 1,
    dangling_images: 2,
    unused_images: 3,
    unused_image_tags: ["old:1", "old:latest"],
    anonymous_volumes: 4,
    orphan_volumes: 5,
    orphan_volume_names: ["loop-chrome-profile-gone"],
  };

  it("lists each source, the unused images one opt-in", () => {
    const rows = reclaimRows({
      ...estimate,
      build_cache: 1,
      dangling_images: 2,
      unused_images: 3,
      unused_image_tags: ["old:1", "old:latest"],
      anonymous_volumes: 4,
      orphan_volumes: 5,
      orphan_volume_names: ["loop-chrome-profile-gone"],
    });
    expect(rows).toEqual([
      { label: "Build cache", bytes: 1 },
      { label: "Dangling images", bytes: 2 },
      { label: "Unused anonymous volumes", bytes: 4 },
      { label: "Deleted channels' Chrome profiles (1 volume)", bytes: 5 },
      { label: "Unused images (2 tags)", bytes: 3, optIn: true },
    ]);
  });

  it("leaves volumes unsized until they are", () => {
    const bytes = reclaimRows({ ...estimate, volumes_sized: false }).map((r) => r.bytes);
    expect(bytes).toEqual([1, 2, null, null, 3]);
  });
});

describe("reclaimSummary", () => {
  const none = { build_cache_reclaimed: 0, images_reclaimed: 0, unused_images_reclaimed: 0, volumes_reclaimed: 0, total_reclaimed: 0, orphan_volumes_removed: 0 };

  it("names only what freed something", () => {
    expect(reclaimSummary({ ...none, build_cache_reclaimed: 2048, volumes_reclaimed: 1024, total_reclaimed: 3072, orphan_volumes_removed: 2 })).toBe(
      "Reclaimed 3.0 KB — build cache 2.0 KB, anonymous volumes 1.0 KB, 2 Chrome profiles of deleted channels removed.",
    );
  });

  it("says when nothing was freed", () => {
    expect(reclaimSummary(none)).toBe("Reclaimed 0 B.");
  });
});
