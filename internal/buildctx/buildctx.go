// Package buildctx fingerprints a project's image build context
// (.loop/container), so the image is built only from a version the owner
// trusted, and copies it out to build from.
package buildctx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Files is a build context's fingerprint: each file's slash path in the
// context to its FileDigest, and each symlink's to "symlink:" and its
// target. Directories are only there through what they hold.
type Files map[string]string

// FileDigest is the fingerprint of a file with content data and mode: its
// sha256, and whether it's executable, which COPY keeps.
func FileDigest(data []byte, mode fs.FileMode) string {
	sum := sha256.Sum256(data)
	d := "sha256:" + hex.EncodeToString(sum[:])
	if mode&0o111 != 0 {
		d += " +x"
	}
	return d
}

// Digest fingerprints the context in r.
func Digest(r *os.Root) (Files, error) {
	nop := func(string) error { return nil }
	return walk(rootTree{r}, func(string, []byte, fs.FileMode) error { return nil }, func(string, string) error { return nil }, nop)
}

// Snapshot copies the context in r to dst, an empty dir, and fingerprints
// the copy: what it returns is what a build from dst sees, however the
// context changes meanwhile.
func Snapshot(r *os.Root, dst string) (Files, error) {
	at := func(name string) string { return filepath.Join(dst, filepath.FromSlash(name)) }
	return walk(rootTree{r},
		func(name string, data []byte, mode fs.FileMode) error {
			return os.WriteFile(at(name), data, mode&0o777)
		},
		func(name, target string) error { return os.Symlink(target, at(name)) },
		func(name string) error { return os.Mkdir(at(name), 0o700) })
}

// tree is the context walk reads: an os.Root, or a fake in tests.
type tree interface {
	fs.ReadDirFS
	ReadLink(name string) (string, error)
	// OpenFile opens name to read without blocking on a FIFO.
	OpenFile(name string) (fs.File, error)
}

type rootTree struct{ r *os.Root }

func (t rootTree) Open(name string) (fs.File, error) { return t.r.FS().Open(name) }

func (t rootTree) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(t.r.FS(), name) }

func (t rootTree) ReadLink(name string) (string, error) { return t.r.Readlink(name) }

func (t rootTree) OpenFile(name string) (fs.File, error) {
	return t.r.OpenFile(name, os.O_RDONLY|nonblock, 0)
}

// walk fingerprints every file and symlink in t, handing each to file or
// link, and each dir below the top to dir, parents first. Anything else (a
// FIFO, a device) is refused.
func walk(t tree, file func(name string, data []byte, mode fs.FileMode) error, link func(name, target string) error, dir func(name string) error) (Files, error) {
	files := Files{}
	err := fs.WalkDir(t, ".", func(name string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case name == ".":
			return nil
		case d.IsDir():
			return dir(name)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := t.ReadLink(name)
			if err != nil {
				return err
			}
			files[name] = "symlink:" + target
			return link(name, target)
		}
		data, mode, err := readFile(t, name)
		if err != nil {
			return err
		}
		files[name] = FileDigest(data, mode)
		return file(name, data, mode)
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// errNotRegular refuses a context entry that's neither a file, a dir nor
// a symlink.
var errNotRegular = errors.New("not a regular file")

// readFile reads name in t, with the mode of what it opened, which must
// be a regular file: it checks the file it opened, not the entry it was
// listed as, so one swapped in after the listing is refused too.
func readFile(t tree, name string) ([]byte, fs.FileMode, error) {
	f, err := t.OpenFile(name)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() {
		return nil, 0, &fs.PathError{Op: "read", Path: name, Err: errNotRegular}
	}
	data, err := io.ReadAll(f)
	return data, fi.Mode(), err
}
