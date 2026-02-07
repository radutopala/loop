package container

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MountSecurity validates mount paths against an allowlist to prevent
// unauthorized filesystem access from containers.
type MountSecurity struct {
	allowlist []string
}

// NewMountSecurity creates a MountSecurity with the given allowlist of permitted paths.
func NewMountSecurity(allowlist []string) *MountSecurity {
	cleaned := make([]string, 0, len(allowlist))
	for _, p := range allowlist {
		cleaned = append(cleaned, filepath.Clean(p))
	}
	return &MountSecurity{allowlist: cleaned}
}

// ValidateMountPath checks whether the given path is within the allowlist.
// It rejects path traversal attempts and paths outside the allowlist.
func (ms *MountSecurity) ValidateMountPath(path string) error {
	if path == "" {
		return fmt.Errorf("mount path cannot be empty")
	}

	if strings.Contains(path, "..") {
		return fmt.Errorf("mount path contains path traversal: %s", path)
	}

	cleaned := filepath.Clean(path)

	if !filepath.IsAbs(cleaned) {
		return fmt.Errorf("mount path must be absolute: %s", path)
	}

	if len(ms.allowlist) == 0 {
		return fmt.Errorf("mount allowlist is empty, all mounts denied")
	}

	for _, allowed := range ms.allowlist {
		if cleaned == allowed || strings.HasPrefix(cleaned, allowed+string(filepath.Separator)) {
			return nil
		}
	}

	return fmt.Errorf("mount path %s is not within allowlist", path)
}
