package container

import (
	"context"
	"slices"
	"strings"
)

// ReclaimScope tells the "reclaim Docker space" action what of the daemon's
// is Loop's to keep or to drop.
type ReclaimScope struct {
	// KeepImages are image refs never removed as unused, whether a container
	// uses them or not: the agent, Chrome and project images say.
	KeepImages []string
	// OrphanVolume reports whether a volume belongs to something Loop no
	// longer has, a deleted channel's Chrome profile say. Nil means none do.
	OrphanVolume func(name string) bool
}

// SetReclaimScope sets what reclaiming space keeps and drops besides the
// build cache, dangling images and anonymous volumes. Without it only the
// manager's own images are kept and no volume is an orphan.
func (m *ImageLifecycleManager) SetReclaimScope(fn func(context.Context) (ReclaimScope, error)) {
	m.reclaimScope = fn
}

// ReclaimOptions picks the opt-in parts of reclaiming space.
type ReclaimOptions struct {
	// UnusedImages also removes the tagged images no container uses, but
	// for the ones the scope keeps.
	UnusedImages bool `json:"unused_images"`
}

// Reclaimable estimates, in bytes, what reclaiming space would free, by
// source, so the UI can show it before anything is removed. Volumes are
// counted in bytes only when VolumesSized.
type Reclaimable struct {
	VolumesSized     bool     `json:"volumes_sized"`
	BuildCache       uint64   `json:"build_cache"`
	DanglingImages   uint64   `json:"dangling_images"`
	UnusedImages     uint64   `json:"unused_images"`
	UnusedImageTags  []string `json:"unused_image_tags"`
	AnonymousVolumes uint64   `json:"anonymous_volumes"`
	OrphanVolumes    uint64   `json:"orphan_volumes"`
	OrphanVolumeList []string `json:"orphan_volume_names"`
}

// ReclaimResult reports the bytes freed by a "reclaim Docker space" action,
// broken down by source so the UI can show what was cleaned.
type ReclaimResult struct {
	BuildCacheReclaimed   uint64 `json:"build_cache_reclaimed"`
	ImagesReclaimed       uint64 `json:"images_reclaimed"`
	UnusedImagesReclaimed uint64 `json:"unused_images_reclaimed"`
	VolumesReclaimed      uint64 `json:"volumes_reclaimed"`
	TotalReclaimed        uint64 `json:"total_reclaimed"`
	// OrphanVolumesRemoved counts the orphan volumes removed; Docker doesn't
	// say how much a removal frees, and sizing them first takes minutes.
	OrphanVolumesRemoved int `json:"orphan_volumes_removed"`
}

func (r ReclaimResult) withTotal() ReclaimResult {
	r.TotalReclaimed = r.BuildCacheReclaimed + r.ImagesReclaimed + r.UnusedImagesReclaimed + r.VolumesReclaimed
	return r
}

// reclaimPlan is what reclaiming space would remove, read from the daemon's
// disk usage.
type reclaimPlan struct {
	estimate      Reclaimable
	unusedImages  []DiskImage
	orphanVolumes []DiskVolume
}

// Reclaimable estimates what ReclaimSpace would free. Sizing the volumes is
// what takes the time, minutes on a large daemon, so it's optional.
func (m *ImageLifecycleManager) Reclaimable(ctx context.Context, volumeSizes bool) (Reclaimable, error) {
	plan, err := m.planReclaim(ctx, volumeSizes)
	if err != nil {
		return Reclaimable{}, err
	}
	return plan.estimate, nil
}

func (m *ImageLifecycleManager) planReclaim(ctx context.Context, volumeSizes bool) (*reclaimPlan, error) {
	scope := ReclaimScope{}
	if m.reclaimScope != nil {
		var err error
		if scope, err = m.reclaimScope(ctx); err != nil {
			return nil, err
		}
	}
	du, err := m.client.DiskUsage(ctx, volumeSizes)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, ref := range append([]string{m.imageName, m.sidecarImage}, scope.KeepImages...) {
		if ref != "" {
			keep[normalizeImageRef(ref)] = true
		}
	}
	plan := &reclaimPlan{estimate: Reclaimable{VolumesSized: volumeSizes, BuildCache: du.BuildCache, UnusedImageTags: []string{}, OrphanVolumeList: []string{}}}
	for _, img := range du.Images {
		if img.Containers != 0 {
			continue
		}
		tags := realTags(img.Tags)
		switch {
		case len(tags) == 0:
			plan.estimate.DanglingImages += img.UniqueSize()
		case !keptImage(img, tags, keep):
			img.Tags = tags
			plan.unusedImages = append(plan.unusedImages, img)
			plan.estimate.UnusedImages += img.UniqueSize()
			plan.estimate.UnusedImageTags = append(plan.estimate.UnusedImageTags, tags...)
		}
	}
	for _, v := range du.Volumes {
		if v.RefCount != 0 {
			continue
		}
		size := uint64(max(v.Size, 0))
		switch {
		case v.Anonymous():
			plan.estimate.AnonymousVolumes += size
		case scope.OrphanVolume != nil && scope.OrphanVolume(v.Name):
			plan.orphanVolumes = append(plan.orphanVolumes, v)
			plan.estimate.OrphanVolumes += size
			plan.estimate.OrphanVolumeList = append(plan.estimate.OrphanVolumeList, v.Name)
		}
	}
	slices.Sort(plan.estimate.UnusedImageTags)
	slices.Sort(plan.estimate.OrphanVolumeList)
	return plan, nil
}

// realTags drops the "<none>:<none>" placeholder older daemons report for an
// untagged image.
func realTags(tags []string) []string {
	return slices.DeleteFunc(slices.Clone(tags), func(t string) bool { return strings.HasPrefix(t, "<none>") })
}

// keptImage reports whether an image is one of Loop's: one the scope keeps,
// or a project image built on the agent image.
func keptImage(img DiskImage, tags []string, keep map[string]bool) bool {
	if _, child := img.Labels[ParentIDLabel]; child {
		return true
	}
	return slices.ContainsFunc(tags, func(t string) bool { return keep[normalizeImageRef(t)] })
}

// ReclaimSpace frees Docker disk: every BuildKit cache entry not in use by a
// running build (unusedFor=0, all), the unused images when opted in, dangling
// images, the anonymous volumes no container uses and the scope's orphan
// volumes, returning the bytes freed by each (the orphans are counted). Build-cache pruning is
// daemon-global, not scoped to Loop's builds. Named volumes other than
// orphans are never touched. When a step fails, what the earlier ones freed
// is still reported alongside the error so nothing looks silently lost.
//
// This is the action reached for when Docker is out of room, so it prunes
// reusable cache too: the alternative left most of the disk unreclaimed, which
// is worse than the slower build that follows.
func (m *ImageLifecycleManager) ReclaimSpace(ctx context.Context, opts ReclaimOptions) (ReclaimResult, error) {
	plan, err := m.planReclaim(ctx, false)
	if err != nil {
		return ReclaimResult{}, err
	}
	var res ReclaimResult
	freed, err := m.client.PruneBuildCache(ctx, 0, true)
	if err != nil {
		return res, err
	}
	res.BuildCacheReclaimed = freed
	if opts.UnusedImages {
		res.UnusedImagesReclaimed = m.removeUnusedImages(ctx, plan.unusedImages)
	}
	if freed, err = m.client.PruneDanglingImages(ctx); err != nil {
		return res.withTotal(), err
	}
	res.ImagesReclaimed = freed
	if freed, err = m.client.PruneAnonymousVolumes(ctx); err != nil {
		return res.withTotal(), err
	}
	res.VolumesReclaimed = freed
	for _, v := range plan.orphanVolumes {
		if err := m.client.RemoveVolume(ctx, v.Name); err != nil {
			m.logger.Warn("reclaim: keeping a volume", "volume", v.Name, "error", err)
			continue
		}
		res.OrphanVolumesRemoved++
	}
	return res.withTotal(), nil
}

// removeUnusedImages removes each image by untagging all its tags, and
// returns the bytes of the ones gone. An image that fails, one a container
// started using since say, stays.
func (m *ImageLifecycleManager) removeUnusedImages(ctx context.Context, images []DiskImage) uint64 {
	var freed uint64
	for _, img := range images {
		removed := true
		for _, tag := range img.Tags {
			if err := m.client.RemoveImage(ctx, tag); err != nil {
				m.logger.Warn("reclaim: keeping an image", "image", tag, "error", err)
				removed = false
				break
			}
		}
		if removed {
			freed += img.UniqueSize()
		}
	}
	return freed
}
