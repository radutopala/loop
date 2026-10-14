package backup

import (
	"archive/zip"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// dbEntry is the database's name in a backup.
	dbEntry = "loop.db"
	// manifestEntry is the manifest's name in a backup.
	manifestEntry = "manifest.json"
	// globalPlaygrounds holds ~/.loop/playground in a backup, and
	// projectPlaygrounds a folder per project's .loop/playground.
	globalPlaygrounds  = "playground/global"
	projectPlaygrounds = "playground/projects"
)

// Manifest describes a backup: when Loop wrote it, and where each folder
// of playgrounds came from.
type Manifest struct {
	CreatedAt   time.Time    `json:"created_at"`
	Version     string       `json:"version"`
	Database    string       `json:"database"`
	Playgrounds []Playground `json:"playgrounds"`
}

// Playground is a playground dir in a backup.
type Playground struct {
	// Folder is the dir's folder in the backup.
	Folder string `json:"folder"`
	// Global is set for ~/.loop/playground.
	Global bool `json:"global,omitempty"`
	// Project is the project dir whose .loop/playground the folder holds.
	Project string `json:"project,omitempty"`
}

// playgroundSource is a playground dir to back up.
type playgroundSource struct {
	Playground
	dir string
}

// playgrounds lists the playground dirs that exist: the global one and
// each project's, each project in a folder named after it.
func (r *Runner) playgrounds(ctx context.Context) []playgroundSource {
	var out []playgroundSource
	add := func(p Playground, dir string) {
		if info, err := r.fs.Lstat(dir); err == nil && info.IsDir() {
			out = append(out, playgroundSource{Playground: p, dir: dir})
		}
	}
	add(Playground{Folder: globalPlaygrounds, Global: true}, filepath.Join(r.LoopDir, "playground"))
	used := map[string]bool{}
	for _, project := range r.Projects(ctx) {
		base := filepath.Base(project)
		name := base
		for i := 2; used[name]; i++ {
			name = base + "-" + strconv.Itoa(i)
		}
		used[name] = true
		add(Playground{Folder: path.Join(projectPlaygrounds, name), Project: project}, filepath.Join(project, ".loop", "playground"))
	}
	return out
}

// archive writes a zip of the snapshot, the playgrounds and the manifest
// to w.
func (r *Runner) archive(ctx context.Context, w io.Writer, snapshot string, at time.Time) error {
	zw := zip.NewWriter(w)
	if err := r.addFile(zw, dbEntry, snapshot, at); err != nil {
		return fmt.Errorf("adding the database: %w", err)
	}
	m := Manifest{CreatedAt: at, Version: r.Version, Database: dbEntry, Playgrounds: []Playground{}}
	for _, p := range r.playgrounds(ctx) {
		if err := r.addDir(zw, p); err != nil {
			return fmt.Errorf("adding %s: %w", p.dir, err)
		}
		m.Playgrounds = append(m.Playgrounds, p.Playground)
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: manifestEntry, Method: zip.Deflate, Modified: at})
	if err == nil {
		_, err = mw.Write(data)
	}
	return cmp.Or(err, zw.Close())
}

// addDir adds the regular files under p's dir to p's folder. Files that
// can't be read are left out, so one doesn't fail the whole backup.
func (r *Runner) addDir(zw *zip.Writer, p playgroundSource) error {
	return r.fs.WalkDir(p.dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			r.Logger.Warn("backing up a playground", "path", file, "error", err)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			r.Logger.Warn("backing up a playground", "path", file, "error", err)
			return nil
		}
		// WalkDir's paths are under p.dir, so Rel doesn't fail.
		rel, _ := filepath.Rel(p.dir, file)
		return r.addFile(zw, path.Join(p.Folder, filepath.ToSlash(rel)), file, info.ModTime())
	})
}

// addFile adds the file at src to zw as name. A file that can't be opened
// is left out, logged; one that fails while it's copied fails the backup.
func (r *Runner) addFile(zw *zip.Writer, name, src string, modified time.Time) error {
	in, err := r.fs.Open(src)
	if err != nil {
		if name == dbEntry {
			return err
		}
		r.Logger.Warn("backing up a playground", "path", src, "error", err)
		return nil
	}
	defer in.Close()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
	if err == nil {
		_, err = io.Copy(w, in)
	}
	return err
}
