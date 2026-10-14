package backup

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
)

const (
	// ageHeader starts every age-encrypted file.
	ageHeader = "age-encryption.org/v1\n"
	// sqliteHeader starts every SQLite database file.
	sqliteHeader = "SQLite format 3\x00"
)

// RestoreOptions says which backup Restore restores, and where.
type RestoreOptions struct {
	// File is the backup.
	File string
	// Identity returns the private keys that decrypt an encrypted backup.
	// It's only called for one.
	Identity func() ([]age.Identity, error)
	// DBPath is the database the backup's replaces.
	DBPath string
	// LoopDir is the ~/.loop dir the global playgrounds go back to.
	LoopDir string
	// StopDaemon runs before the database is replaced, and StartDaemon
	// after, even when the restore failed.
	StopDaemon  func() error
	StartDaemon func() error
}

// RestoreResult is what Restore did.
type RestoreResult struct {
	Manifest Manifest
	// MovedAside are the new paths of the database files the backup's
	// replaced.
	MovedAside []string
	// Restored are the playground dirs put back, and Kept the ones left
	// alone because a playground of that name exists.
	Restored []string
	Kept     []string
}

// Restorer restores backups.
type Restorer struct {
	now func() time.Time
	fs  fileSystem
}

// NewRestorer returns a Restorer working on the disk.
func NewRestorer() *Restorer {
	return &Restorer{now: time.Now, fs: osFS{}}
}

// Restore replaces the database with the backup's, keeping the one it
// replaces next to it, and puts back the backup's playgrounds that don't
// exist. The result is nil when the database wasn't replaced. The backup is read and its database unpacked before the daemon
// is stopped, so a bad backup or a wrong key changes nothing.
func (r *Restorer) Restore(o RestoreOptions) (*RestoreResult, error) {
	dbDir := filepath.Dir(o.DBPath)
	zr, done, err := r.open(o.File, o.Identity)
	if err != nil {
		return nil, err
	}
	defer done()
	m, err := readManifest(zr)
	if err != nil {
		return nil, err
	}
	tmpDB, err := r.unpackDB(zr, dbDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.fs.Remove(tmpDB) }()
	if err := o.StopDaemon(); err != nil {
		return nil, fmt.Errorf("stopping the daemon: %w", err)
	}
	res := &RestoreResult{Manifest: m}
	err = r.swapDB(o.DBPath, tmpDB, res)
	if err != nil {
		// The database is as it was.
		res = nil
	} else {
		err = r.restorePlaygrounds(zr, m, o.LoopDir, res)
	}
	if startErr := o.StartDaemon(); startErr != nil {
		err = errors.Join(err, fmt.Errorf("starting the daemon: %w", startErr))
	}
	return res, err
}

// open opens the backup at file, decrypting an encrypted one as it's
// read, so no decrypted copy is written. done closes it.
func (r *Restorer) open(file string, identity func() ([]age.Identity, error)) (*zip.Reader, func(), error) {
	f, err := r.fs.OpenReaderAt(file)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the backup: %w", err)
	}
	done := func() { _ = f.Close() }
	zr, err := readArchive(f, identity)
	if err != nil {
		done()
		return nil, nil, err
	}
	return zr, done, nil
}

func readArchive(f readerAtFile, identity func() ([]age.Identity, error)) (*zip.Reader, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("opening the backup: %w", err)
	}
	var src io.ReaderAt = f
	size := info.Size()
	head := make([]byte, len(ageHeader))
	if n, _ := f.ReadAt(head, 0); n == len(head) && string(head) == ageHeader {
		ids, err := identity()
		if err != nil {
			return nil, err
		}
		if src, size, err = age.DecryptReaderAt(f, size, ids...); err != nil {
			return nil, fmt.Errorf("decrypting the backup: %w", err)
		}
	}
	zr, err := zip.NewReader(src, size)
	if err != nil {
		return nil, fmt.Errorf("reading the backup: %w", err)
	}
	return zr, nil
}

// readManifest reads the backup's manifest, checking each playground
// folder is one Loop writes.
func readManifest(zr *zip.Reader) (Manifest, error) {
	var m Manifest
	data, err := readEntry(zr, manifestEntry)
	if err != nil {
		return m, fmt.Errorf("not a Loop backup: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("reading %s: %w", manifestEntry, err)
	}
	for _, p := range m.Playgrounds {
		folder := path.Clean(p.Folder)
		ok := folder == p.Folder && strings.HasPrefix(folder, "playground/")
		if p.Global {
			ok = ok && p.Project == ""
		} else {
			ok = ok && filepath.IsAbs(p.Project)
		}
		if !ok {
			return m, fmt.Errorf("%s lists an unexpected playground folder %q", manifestEntry, p.Folder)
		}
	}
	return m, nil
}

func readEntry(zr *zip.Reader, name string) ([]byte, error) {
	f, err := zr.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// unpackDB writes the backup's database to a new file in dir, checking it
// is one, and returns its path.
func (r *Restorer) unpackDB(zr *zip.Reader, dir string) (string, error) {
	in, err := zr.Open(dbEntry)
	if err != nil {
		return "", fmt.Errorf("not a Loop backup: %w", err)
	}
	defer in.Close()
	head := make([]byte, len(sqliteHeader))
	if _, err := io.ReadFull(in, head); err != nil || string(head) != sqliteHeader {
		return "", fmt.Errorf("the backup's %s isn't a SQLite database", dbEntry)
	}
	out, err := r.fs.CreateTemp(dir, ".restore-*.db")
	if err != nil {
		return "", fmt.Errorf("unpacking the database: %w", err)
	}
	_, err = io.Copy(out, io.MultiReader(bytes.NewReader(head), in))
	err = cmp.Or(err, out.Sync())
	if err = cmp.Or(err, out.Close()); err != nil {
		_ = r.fs.Remove(out.Name())
		return "", fmt.Errorf("unpacking the database: %w", err)
	}
	return out.Name(), nil
}

// swapDB moves the database and its WAL files aside, keeping their
// pairing, and moves tmpDB in its place. When a step fails, what was
// moved goes back.
func (r *Restorer) swapDB(dbPath, tmpDB string, res *RestoreResult) error {
	aside := r.asideName(dbPath)
	type move struct{ from, to string }
	var moved []move
	undo := func() {
		for _, m := range slices.Backward(moved) {
			_ = r.fs.Rename(m.to, m.from)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		from := dbPath + suffix
		if _, err := r.fs.Lstat(from); err != nil {
			continue
		}
		to := aside + suffix
		if err := r.fs.Rename(from, to); err != nil {
			undo()
			return fmt.Errorf("moving the database aside: %w", err)
		}
		moved = append(moved, move{from, to})
	}
	if err := r.fs.Rename(tmpDB, dbPath); err != nil {
		undo()
		return fmt.Errorf("putting the backup's database in place: %w", err)
	}
	for _, m := range moved {
		res.MovedAside = append(res.MovedAside, m.to)
	}
	return nil
}

// asideName names the replaced database after the time, numbered when an
// earlier restore in the same second used the name, so none is overwritten.
func (r *Restorer) asideName(dbPath string) string {
	base := dbPath + ".before-restore-" + r.now().UTC().Format(timeLayout)
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name += "-" + strconv.Itoa(i)
		}
		if !slices.ContainsFunc([]string{"", "-wal", "-shm"}, func(suffix string) bool {
			_, err := r.fs.Lstat(name + suffix)
			return err == nil
		}) {
			return name
		}
	}
}

// restorePlaygrounds puts back each of the backup's playgrounds that
// doesn't exist; one that does is left as it is.
func (r *Restorer) restorePlaygrounds(zr *zip.Reader, m Manifest, loopDir string, res *RestoreResult) error {
	for _, p := range m.Playgrounds {
		target := filepath.Join(loopDir, "playground")
		if !p.Global {
			target = filepath.Join(p.Project, ".loop", "playground")
		}
		// Each playground's files, by its name, in the backup's order.
		var names []string
		files := map[string][]*zip.File{}
		for _, f := range zr.File {
			rel, ok := strings.CutPrefix(f.Name, p.Folder+"/")
			name, _, _ := strings.Cut(rel, "/")
			if !ok || strings.HasSuffix(rel, "/") || name == "" {
				continue
			}
			if files[name] == nil {
				names = append(names, name)
			}
			files[name] = append(files[name], f)
		}
		for _, name := range names {
			dest := filepath.Join(target, name)
			if _, err := r.fs.Lstat(dest); err == nil {
				res.Kept = append(res.Kept, dest)
				continue
			}
			for _, f := range files[name] {
				rel := filepath.FromSlash(strings.TrimPrefix(f.Name, p.Folder+"/"))
				if !filepath.IsLocal(rel) {
					return fmt.Errorf("the backup has an unsafe path %q", f.Name)
				}
				if err := r.writeFile(f, filepath.Join(target, rel)); err != nil {
					return fmt.Errorf("restoring the playground %s: %w", dest, err)
				}
			}
			res.Restored = append(res.Restored, dest)
		}
	}
	return nil
}

// writeFile writes f's content to the new file dest.
func (r *Restorer) writeFile(f *zip.File, dest string) error {
	if err := r.fs.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := f.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := r.fs.Create(dest)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return cmp.Or(err, out.Close())
}
