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
	"sync"

	"github.com/radutopala/loop/internal/osutil"
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

// digest fingerprints t, each file by file.
func digest(t tree, file func(name string, f fs.File, fi fs.FileInfo) (string, error)) (Files, error) {
	nop := func(string) error { return nil }
	return walk(t, file, func(string, string) error { return nil }, nop)
}

// readDigest reads f to fingerprint it.
func readDigest(_ string, f fs.File, fi fs.FileInfo) (string, error) {
	data, err := io.ReadAll(f)
	return FileDigest(data, fi.Mode()), err
}

// Cache fingerprints contexts, reading again only the files whose stat
// changed since it last read them: a context of large binaries costs a few
// stats while it stays as it is.
type Cache struct {
	mu sync.Mutex
	// dirs holds, for each context by its dir, what each file there was
	// when last read.
	dirs map[string]map[string]cachedFile
}

type cachedFile struct {
	stat   osutil.FileStamp
	digest string
}

// Digest fingerprints the context in r.
func (c *Cache) Digest(r *os.Root) (Files, error) {
	return c.digest(r.Name(), rootTree{r})
}

func (c *Cache) digest(dir string, t tree) (Files, error) {
	c.mu.Lock()
	last := c.dirs[dir]
	c.mu.Unlock()
	seen := map[string]cachedFile{}
	files, err := digest(t, func(name string, f fs.File, fi fs.FileInfo) (string, error) {
		k := osutil.StampOf(fi)
		if was, ok := last[name]; ok && was.stat == k {
			seen[name] = was
			return was.digest, nil
		}
		d, err := readDigest(name, f, fi)
		seen[name] = cachedFile{stat: k, digest: d}
		return d, err
	})
	if err != nil {
		return nil, err
	}
	// Only what the context has now is kept, so a removed file's goes.
	c.mu.Lock()
	if c.dirs == nil {
		c.dirs = map[string]map[string]cachedFile{}
	}
	c.dirs[dir] = seen
	c.mu.Unlock()
	return files, nil
}

// Snapshot copies the context in r to dst, an empty dir, and fingerprints
// the copy: what it returns is what a build from dst sees, however the
// context changes meanwhile.
func Snapshot(r *os.Root, dst string) (Files, error) {
	at := func(name string) string { return filepath.Join(dst, filepath.FromSlash(name)) }
	return walk(rootTree{r}, copyTo(dst),
		func(name, target string) error { return os.Symlink(target, at(name)) },
		func(name string) error { return os.Mkdir(at(name), 0o700) })
}

// copyTo fingerprints each file it's handed as it copies it into dst.
func copyTo(dst string) func(name string, f fs.File, fi fs.FileInfo) (string, error) {
	return func(name string, f fs.File, fi fs.FileInfo) (string, error) {
		data, err := io.ReadAll(f)
		if err != nil {
			return "", err
		}
		return FileDigest(data, fi.Mode()), os.WriteFile(filepath.Join(dst, filepath.FromSlash(name)), data, fi.Mode()&0o777)
	}
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

// walk fingerprints every file and symlink in t: each file by what file
// returns for it, opened, each symlink handed to link, and each dir below
// the top to dir, parents first. Anything else (a FIFO, a device) is
// refused.
func walk(t tree, file func(name string, f fs.File, fi fs.FileInfo) (string, error), link func(name, target string) error, dir func(name string) error) (Files, error) {
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
		f, fi, err := openFile(t, name)
		if err != nil {
			return err
		}
		defer f.Close()
		files[name], err = file(name, f, fi)
		return err
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// errNotRegular refuses a context entry that's neither a file, a dir nor
// a symlink.
var errNotRegular = errors.New("not a regular file")

// openFile opens name in t, which must be a regular file: it checks the
// file it opened, not the entry it was listed as, so one swapped in after
// the listing is refused too.
func openFile(t tree, name string) (fs.File, fs.FileInfo, error) {
	f, err := t.OpenFile(name)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = &fs.PathError{Op: "read", Path: name, Err: errNotRegular}
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}
