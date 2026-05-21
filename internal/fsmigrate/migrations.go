package fsmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"

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
	{
		Description: "refresh container/ files for Debian base switch",
		Apply:       refreshContainerFiles,
	},
	{
		Description: "refresh container/ files: HOST_UID/HOST_GID pinning + chrome alpine revert",
		Apply:       refreshContainerFiles,
	},
	{
		Description: "seed builtin code review prompt shortcut",
		Apply: func(ctx context.Context, c *Ctx) error {
			return seedBuiltinCodeReviewShortcut(ctx, c, json.MarshalIndent)
		},
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

// builtinCodeReviewShortcutName is the unique name we look for / write under.
// Whitespace is intentional — it's how the entry renders in the # picker.
const builtinCodeReviewShortcutName = "builtin code review"

// marshalIndentFunc matches json.MarshalIndent. Parameterized so tests can
// exercise the otherwise-unreachable marshal-error branch without resorting
// to package-level var save/restore.
type marshalIndentFunc func(v any, prefix, indent string) ([]byte, error)

// seedBuiltinCodeReviewShortcut appends a default shortcut to the user's
// existing ~/.loop/config.json. Fresh installs get the same entry via
// config.global.example.json on first onboard; this migration covers the
// upgrade path for installs that already have a config.
//
// No-ops when the file doesn't exist (onboard will handle it) or when an
// entry with the same name is already present (user may have added it
// themselves; never duplicate).
func seedBuiltinCodeReviewShortcut(_ context.Context, c *Ctx, marshal marshalIndentFunc) error {
	configPath := filepath.Join(c.LoopDir, "config.json")
	data, err := c.Sys.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", configPath, err)
	}

	standardized, err := hujson.Standardize(data)
	if err != nil {
		return fmt.Errorf("standardizing %s: %w", configPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(standardized, &cfg); err != nil {
		return fmt.Errorf("parsing %s: %w", configPath, err)
	}

	shortcuts, _ := cfg["prompt_shortcuts"].([]any)
	for _, item := range shortcuts {
		if m, ok := item.(map[string]any); ok && m["name"] == builtinCodeReviewShortcutName {
			return nil
		}
	}

	shortcuts = append(shortcuts, map[string]any{
		"name":        builtinCodeReviewShortcutName,
		"description": "Run Claude Code's built-in /code-review slash command",
		"prompt":      "/code-review",
	})
	cfg["prompt_shortcuts"] = shortcuts

	out, err := marshal(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing %s: %w", configPath, err)
	}
	if err := c.Sys.WriteFile(configPath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", configPath, err)
	}
	return nil
}
