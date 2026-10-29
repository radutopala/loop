package buildctx

import (
	"io/fs"
	"syscall"
)

// inodeAndCtime returns fi's inode and change time.
func inodeAndCtime(fi fs.FileInfo) (uint64, int64) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return st.Ino, st.Ctimespec.Nano()
}
