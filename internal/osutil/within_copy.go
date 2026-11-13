package osutil

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrCopyIntoItself is CopyIn refusing to copy a dir into itself or a dir
// under it.
var ErrCopyIntoItself = errors.New("cannot copy a directory into itself")

// CopyIn copies src, under srcRoot, to dst, under dstRoot, which must not
// exist yet. A dir is copied with everything in it; a symlink is copied as
// a symlink to the same target, never followed, so nothing outside srcRoot
// is read; other special files (sockets, devices, pipes) are left out.
// Files keep their permission bits. When the copy fails partway, what it
// made at dst is removed.
func CopyIn(srcRoot, src, dstRoot, dst string) error {
	srcRel, err := filepath.Rel(srcRoot, src)
	if err != nil {
		return err
	}
	dstRel, err := filepath.Rel(dstRoot, dst)
	if err != nil {
		return err
	}
	if filepath.Clean(srcRoot) == filepath.Clean(dstRoot) && (dstRel == srcRel || strings.HasPrefix(dstRel, srcRel+string(filepath.Separator))) {
		return ErrCopyIntoItself
	}
	sr, err := os.OpenRoot(srcRoot)
	if err != nil {
		return err
	}
	defer sr.Close()
	dr, err := os.OpenRoot(dstRoot)
	if err != nil {
		return err
	}
	defer dr.Close()
	info, err := sr.Lstat(srcRel)
	if err != nil {
		return err
	}
	if _, err := dr.Lstat(dstRel); err == nil {
		return &os.PathError{Op: "copy", Path: dstRel, Err: fs.ErrExist}
	}
	if err := copyEntry(sr, srcRel, dr, dstRel, info); err != nil {
		_ = dr.RemoveAll(dstRel)
		return err
	}
	return nil
}

// The copy helpers chain their steps on err, so a step that fails stops the
// rest and its error is returned.

func copyEntry(sr *os.Root, src string, dr *os.Root, dst string, info fs.FileInfo) error {
	switch mode := info.Mode(); {
	case mode&fs.ModeSymlink != 0:
		target, err := sr.Readlink(src)
		if err == nil {
			err = dr.Symlink(target, dst)
		}
		return err
	case mode.IsDir():
		return copyDir(sr, src, dr, dst, mode.Perm())
	case mode.IsRegular():
		return copyFile(sr, src, dr, dst, mode.Perm())
	default:
		return nil
	}
}

func copyDir(sr *os.Root, src string, dr *os.Root, dst string, perm fs.FileMode) error {
	entries, err := fs.ReadDir(sr.FS(), filepath.ToSlash(src))
	if err == nil {
		// Writable while it fills, whatever perm says.
		err = dr.Mkdir(dst, perm|0o700)
	}
	for i := 0; err == nil && i < len(entries); i++ {
		var info fs.FileInfo
		info, err = entries[i].Info()
		if err == nil {
			name := entries[i].Name()
			err = copyEntry(sr, filepath.Join(src, name), dr, filepath.Join(dst, name), info)
		}
	}
	if err == nil {
		err = dr.Chmod(dst, perm)
	}
	return err
}

func copyFile(sr *os.Root, src string, dr *os.Root, dst string, perm fs.FileMode) error {
	in, err := sr.Open(src)
	if err == nil {
		defer in.Close()
		var out *os.File
		out, err = dr.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			_, err = io.Copy(out, in)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
	}
	return err
}

func (RealSystem) CopyIn(srcRoot, src, dstRoot, dst string) error {
	return CopyIn(srcRoot, src, dstRoot, dst)
}
