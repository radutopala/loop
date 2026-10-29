//go:build !linux && !darwin

package osutil

import "io/fs"

// inodeAndCtime is zero where the stat has neither.
func inodeAndCtime(fs.FileInfo) (uint64, int64) { return 0, 0 }
