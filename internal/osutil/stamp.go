package osutil

import (
	"io/fs"
	"time"
)

// FileStamp is what a file's stat says of its content: any write changes
// it. The change time and inode can't be set back the way a write's
// modification time can; where the OS has neither, they're zero.
type FileStamp struct {
	size         int64
	mode         fs.FileMode
	mtime, ctime int64
	ino          uint64
}

// StampOf returns fi's FileStamp.
func StampOf(fi fs.FileInfo) FileStamp {
	ino, ctime := inodeAndCtime(fi)
	return FileStamp{size: fi.Size(), mode: fi.Mode(), mtime: fi.ModTime().UnixNano(), ctime: ctime, ino: ino}
}

// ChangedSince reports whether the file was written, or its modification
// time set, at t or later. A stamp that is changed since a moment just before
// it was read may not change with a further write in the same clock tick.
func (s FileStamp) ChangedSince(t time.Time) bool {
	n := t.UnixNano()
	return s.mtime >= n || s.ctime >= n
}
