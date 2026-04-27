package fsmigrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	containerimage "github.com/radutopala/loop/internal/container/image"
)

// migrations holds all filesystem migrations in order. Position in the slice
// is the version number (index 0 is the bootstrap placeholder, never executed).
// Append a new entry to ship a new filesystem change; never reorder or delete
// existing entries.
var migrations = []Migration{
	{Description: "bootstrap"},
	{
		Description: "refresh embedded container/ files",
		Apply:       refreshContainerFiles,
	},
}

// versionedContainerFiles are tracked by the daemon: each release ships a
// canonical version, and the migration overwrites stale on-disk copies after
// backing up any user changes to <name>.bkp. setup.sh is intentionally absent
// — it is treated as user-editable (skip-if-exists).
var versionedContainerFiles = []string{
	"Dockerfile",
	"entrypoint.sh",
	"agent-bashrc",
	"chrome.Dockerfile",
	"chrome-entrypoint.sh",
}

// refreshContainerFiles writes the container build assets embedded in the
// binary into ~/.loop/container/. It exists because defaultEnsureImage
// previously skipped this whole block when the Dockerfile was already
// present, leaving stale entrypoint.sh / agent-bashrc on disk after a
// daemon upgrade.
//
// Versioned files are overwritten so they track the daemon. Any pre-existing
// copy whose contents differ from the embedded version is preserved as
// <name>.bkp before the overwrite, so user edits are never silently lost.
// setup.sh is treated as user-editable: written only when missing.
func refreshContainerFiles(_ context.Context, c *Ctx) error {
	containerDir := filepath.Join(c.LoopDir, "container")
	if err := c.Sys.MkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("creating container directory: %w", err)
	}
	for _, name := range versionedContainerFiles {
		data := containerimage.MustRead(name)
		path := filepath.Join(containerDir, name)
		existing, err := c.Sys.ReadFile(path)
		switch {
		case err == nil:
			if !bytes.Equal(existing, data) {
				if err := c.Sys.WriteFile(path+".bkp", existing, 0644); err != nil {
					return fmt.Errorf("backing up %s: %w", name, err)
				}
			}
		case errors.Is(err, os.ErrNotExist):
			// no prior file, nothing to back up
		default:
			return fmt.Errorf("reading existing %s: %w", name, err)
		}
		if err := c.Sys.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	setupPath := filepath.Join(containerDir, "setup.sh")
	if _, err := c.Sys.Stat(setupPath); err != nil {
		if err := c.Sys.WriteFile(setupPath, containerimage.MustRead("setup.sh"), 0644); err != nil {
			return fmt.Errorf("writing setup.sh: %w", err)
		}
	}
	return nil
}
