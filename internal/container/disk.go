package container

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
)

// anonymousVolumeLabel is the label Docker (23+) puts on the volumes it
// creates for a container's unnamed mounts.
const anonymousVolumeLabel = "com.docker.volume.anonymous"

// DiskUsage is what the Docker daemon holds on disk, as far as reclaiming
// space goes.
type DiskUsage struct {
	Images     []DiskImage
	Volumes    []DiskVolume
	BuildCache uint64 // bytes of build cache no running build uses
}

// DiskImage is an image and the containers, running or not, that use it.
type DiskImage struct {
	ID         string
	Tags       []string
	Labels     map[string]string
	Containers int64 // -1 when the daemon didn't count them
	Size       int64
	SharedSize int64 // -1 when the daemon didn't count it
}

// UniqueSize is the bytes removing the image would free: the layers no other
// image shares.
func (i DiskImage) UniqueSize() uint64 {
	size := i.Size
	if i.SharedSize > 0 {
		size -= i.SharedSize
	}
	return uint64(max(size, 0))
}

// DiskVolume is a volume and the containers that mount it.
type DiskVolume struct {
	Name     string
	Labels   map[string]string
	RefCount int64 // -1 when the daemon didn't count them
	Size     int64 // -1 when the daemon didn't count it
}

// Anonymous reports whether Docker created the volume for a container's
// unnamed mount.
func (v DiskVolume) Anonymous() bool {
	_, ok := v.Labels[anonymousVolumeLabel]
	return ok
}

// DiskUsage reports the daemon's images, volumes and build cache. Sizing
// volumes means walking them, which takes minutes on a large daemon, so
// without volumeSizes only the volumes no container uses are listed, unsized.
func (c *Client) DiskUsage(ctx context.Context, volumeSizes bool) (*DiskUsage, error) {
	objects := []types.DiskUsageObject{types.ImageObject, types.BuildCacheObject}
	if volumeSizes {
		objects = append(objects, types.VolumeObject)
	}
	du, err := c.api.DiskUsage(ctx, types.DiskUsageOptions{Types: objects})
	if err != nil {
		return nil, fmt.Errorf("reading docker disk usage: %w", err)
	}
	if !volumeSizes {
		resp, err := c.api.VolumeList(ctx, volume.ListOptions{Filters: filters.NewArgs(filters.Arg("dangling", "true"))})
		if err != nil {
			return nil, fmt.Errorf("listing unused volumes: %w", err)
		}
		for _, v := range resp.Volumes {
			if v != nil {
				du.Volumes = append(du.Volumes, &volume.Volume{Name: v.Name, Labels: v.Labels, UsageData: &volume.UsageData{Size: -1}})
			}
		}
	}
	out := &DiskUsage{}
	for _, img := range du.Images {
		if img == nil {
			continue
		}
		out.Images = append(out.Images, DiskImage{
			ID: img.ID, Tags: img.RepoTags, Labels: img.Labels,
			Containers: img.Containers, Size: img.Size, SharedSize: img.SharedSize,
		})
	}
	for _, v := range du.Volumes {
		if v == nil {
			continue
		}
		dv := DiskVolume{Name: v.Name, Labels: v.Labels, RefCount: -1, Size: -1}
		if v.UsageData != nil {
			dv.RefCount, dv.Size = v.UsageData.RefCount, v.UsageData.Size
		}
		out.Volumes = append(out.Volumes, dv)
	}
	for _, rec := range du.BuildCache {
		if rec != nil && !rec.InUse && rec.Size > 0 {
			out.BuildCache += uint64(rec.Size)
		}
	}
	return out, nil
}

// PruneAnonymousVolumes removes the anonymous volumes no container uses and
// returns the bytes freed. Named volumes are never touched: they hold state
// someone chose to keep, caches and databases say.
func (c *Client) PruneAnonymousVolumes(ctx context.Context) (uint64, error) {
	report, err := c.api.VolumesPrune(ctx, filters.NewArgs(filters.Arg("label", anonymousVolumeLabel)))
	if err != nil {
		return 0, fmt.Errorf("pruning anonymous volumes: %w", err)
	}
	return report.SpaceReclaimed, nil
}

// RemoveImage untags ref, removing the image once no tag is left. It isn't
// forced, so an image a container uses stays. A ref already gone counts as
// removed: the daemon drops an image's digest refs along with its last tag.
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	if _, err := c.api.ImageRemove(ctx, ref, image.RemoveOptions{PruneChildren: true}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("removing image %s: %w", ref, err)
	}
	return nil
}

// RemoveVolume removes a volume. It isn't forced, so a volume a container
// mounts stays. A volume already gone counts as removed.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	if err := c.api.VolumeRemove(ctx, name, false); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("removing volume %s: %w", name, err)
	}
	return nil
}
