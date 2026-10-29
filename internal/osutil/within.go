package osutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// The *In functions reach path, which lies under root, through an os.Root
// opened at root: a ".." or a symlink on the way that leads outside root
// fails instead of being followed, at the moment of the call, so a link
// swapped in after a check can't redirect it. They're for files under a
// project's .loop dir, which the agent may write: a symlink planted there
// can't make the daemon read or write a host file outside the project.

// ReadFileIn reads path under root.
func ReadFileIn(root, path string) ([]byte, error) {
	var data []byte
	err := inRoot(root, path, func(r *os.Root, rel string) (err error) {
		data, err = r.ReadFile(rel)
		return err
	})
	return data, err
}

// OpenFileIn opens path under root to read. The caller closes it.
func OpenFileIn(root, path string) (*os.File, error) {
	var f *os.File
	err := inRoot(root, path, func(r *os.Root, rel string) (err error) {
		f, err = r.Open(rel)
		return err
	})
	return f, err
}

// WriteFileIn writes path under root, creating it with perm if missing.
func WriteFileIn(root, path string, data []byte, perm os.FileMode) error {
	return inRoot(root, path, func(r *os.Root, rel string) error {
		return r.WriteFile(rel, data, perm)
	})
}

// CheckIn returns an error when path leads outside root, dangling link or
// not. A path that doesn't exist yet, or a missing root, is fine: there is
// nothing to follow. It lets a handler refuse with a clear message before it
// starts; the write itself goes through WriteFileIn.
func CheckIn(root, path string) error {
	err := inRoot(root, path, func(r *os.Root, rel string) error {
		_, err := r.Stat(rel)
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// MkdirAllIn creates the dir path under root, with any missing parents.
func MkdirAllIn(root, path string, perm os.FileMode) error {
	return inRoot(root, path, func(r *os.Root, rel string) error {
		return r.MkdirAll(rel, perm)
	})
}

// OpenRootIn opens dir, under parent, as an os.Root to work in. The way to
// dir may not leave parent, and dir itself may not be a symlink, not even
// one that stays in parent: the agent can point such a link anywhere in
// parent, at files it can't write itself (a repo's hooks). A link swapped in
// around the open is caught as well: the dir opened must be the one found at
// dir afterwards. The caller closes the root.
func OpenRootIn(parent, dir string) (*os.Root, error) {
	var root *os.Root
	err := inRoot(parent, dir, func(p *os.Root, rel string) error {
		r, err := p.OpenRoot(rel)
		if err != nil {
			return err
		}
		opened, _ := r.Stat(".")
		at, _ := p.Lstat(rel)
		if !os.SameFile(opened, at) {
			r.Close()
			return &os.PathError{Op: "openat", Path: rel, Err: errEscapes}
		}
		root = r
		return nil
	})
	return root, err
}

// errEscapes has the text of the error os.Root gives a path that leads
// outside it, which os doesn't export; IsEscape matches on it.
var errEscapes = errors.New("path escapes from parent")

// IsEscape reports whether err is an os.Root, or one of the *In functions,
// refusing a path that leads outside its root. os.Root's MkdirAll wraps the
// refusal in a second PathError.
func IsEscape(err error) bool {
	var pe *os.PathError
	for errors.As(err, &pe) {
		if pe.Err.Error() == errEscapes.Error() {
			return true
		}
		err = pe.Err
	}
	return false
}

func inRoot(root, path string, fn func(r *os.Root, rel string) error) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return fn(r, rel)
}

func (RealSystem) ReadFileIn(root, path string) ([]byte, error) { return ReadFileIn(root, path) }
func (RealSystem) WriteFileIn(root, path string, data []byte, perm os.FileMode) error {
	return WriteFileIn(root, path, data, perm)
}
func (RealSystem) MkdirAllIn(root, path string, perm os.FileMode) error {
	return MkdirAllIn(root, path, perm)
}
