package osutil

import (
	"os"
	"path/filepath"
)

// RenameIn renames oldpath to newpath, both under root, like os.Rename: a
// symlink at newpath is replaced, not followed.
func RenameIn(root, oldpath, newpath string) error {
	return inRoot(root, oldpath, func(r *os.Root, oldRel string) error {
		newRel, err := filepath.Rel(root, newpath)
		if err != nil {
			return err
		}
		return r.Rename(oldRel, newRel)
	})
}

// RemoveIn removes path under root, like os.Remove.
func RemoveIn(root, path string) error {
	return inRoot(root, path, func(r *os.Root, rel string) error {
		return r.Remove(rel)
	})
}

func (RealSystem) RenameIn(root, oldpath, newpath string) error {
	return RenameIn(root, oldpath, newpath)
}
func (RealSystem) RemoveIn(root, path string) error { return RemoveIn(root, path) }

// RootFS reaches the files under Root through the *In functions, for code
// that takes a filesystem: a path that leads outside Root, by a symlink or
// by not being under it, fails.
type RootFS struct{ Root string }

func (f RootFS) Stat(name string) (os.FileInfo, error) {
	var fi os.FileInfo
	err := inRoot(f.Root, name, func(r *os.Root, rel string) (err error) {
		fi, err = r.Stat(rel)
		return err
	})
	return fi, err
}

func (f RootFS) ReadFile(name string) ([]byte, error) { return ReadFileIn(f.Root, name) }
func (f RootFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return WriteFileIn(f.Root, name, data, perm)
}
func (f RootFS) MkdirAll(path string, perm os.FileMode) error { return MkdirAllIn(f.Root, path, perm) }
func (f RootFS) Rename(oldpath, newpath string) error         { return RenameIn(f.Root, oldpath, newpath) }
func (f RootFS) Remove(name string) error                     { return RemoveIn(f.Root, name) }
