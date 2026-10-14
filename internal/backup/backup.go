// Package backup copies the Loop database and the playgrounds to a
// directory on a schedule, such as one a Dropbox or OneDrive client syncs,
// as zip files, encrypted with age when a public key is set. It drops old
// backups by retention tiers, and restores a backup.
package backup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/radutopala/loop/internal/config"
)

const (
	filePrefix = "loop-"
	// zipSuffix ends an unencrypted backup, and ageSuffix an encrypted one.
	zipSuffix  = ".zip"
	ageSuffix  = ".zip.age"
	timeLayout = "20060102T150405Z"
	// partialPattern names a backup being written, which a sync client may
	// see but no restore should pick.
	partialPattern = ".loop-*.partial"
	// retryAfter is how long a failed backup waits before the next try.
	retryAfter = 15 * time.Minute
)

var (
	// ErrRunning is returned when a backup is already being written.
	ErrRunning = errors.New("a backup is already running")
	// ErrNoPath is returned when no backup directory is configured.
	ErrNoPath = errors.New("no backup directory is configured")
)

// Snapshotter writes a consistent copy of the database to a new file.
type Snapshotter interface {
	BackupInto(ctx context.Context, path string) error
}

// fileSystem is what backups and restores do to files, so tests can fail
// each step.
type fileSystem interface {
	MkdirAll(path string, perm fs.FileMode) error
	CreateTemp(dir, pattern string) (writeFile, error)
	// Create creates a new file, failing when path exists.
	Create(path string) (writeFile, error)
	Open(path string) (io.ReadCloser, error)
	OpenReaderAt(path string) (readerAtFile, error)
	Rename(from, to string) error
	Remove(path string) error
	ReadDir(path string) ([]fs.DirEntry, error)
	Lstat(path string) (fs.FileInfo, error)
	WalkDir(root string, fn fs.WalkDirFunc) error
}

// writeFile is a file being written.
type writeFile interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

// readerAtFile is a file read at offsets.
type readerAtFile interface {
	io.ReaderAt
	io.Closer
	Stat() (fs.FileInfo, error)
}

type osFS struct{}

func (osFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (osFS) CreateTemp(dir, pattern string) (writeFile, error) {
	return os.CreateTemp(dir, pattern)
}
func (osFS) Create(path string) (writeFile, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
}
func (osFS) Open(path string) (io.ReadCloser, error)        { return os.Open(path) }
func (osFS) OpenReaderAt(path string) (readerAtFile, error) { return os.Open(path) }
func (osFS) Rename(from, to string) error                   { return os.Rename(from, to) }
func (osFS) Remove(path string) error                       { return os.Remove(path) }
func (osFS) ReadDir(path string) ([]fs.DirEntry, error)     { return os.ReadDir(path) }
func (osFS) Lstat(path string) (fs.FileInfo, error)         { return os.Lstat(path) }
func (osFS) WalkDir(root string, fn fs.WalkDirFunc) error   { return filepath.WalkDir(root, fn) }

// Backup is a backup file in the backup directory.
type Backup struct {
	File      string    `json:"file"`
	Path      string    `json:"path"`
	At        time.Time `json:"at"`
	Size      int64     `json:"size"`
	Encrypted bool      `json:"encrypted"`
}

// Status is the backup schedule and how it went.
type Status struct {
	Enabled     bool   `json:"enabled"`
	Dir         string `json:"dir,omitempty"`
	TimesPerDay int    `json:"times_per_day"`
	KeepLast    int    `json:"keep_last"`
	KeepDaily   int    `json:"keep_daily"`
	KeepWeekly  int    `json:"keep_weekly"`
	KeepMonthly int    `json:"keep_monthly"`
	// Recipient is the public key new backups are encrypted to; empty
	// writes them unencrypted.
	Recipient string     `json:"recipient,omitempty"`
	Running   bool       `json:"running"`
	Last      *Backup    `json:"last,omitempty"`
	Count     int        `json:"count"`
	TotalSize int64      `json:"total_size"`
	NextAt    *time.Time `json:"next_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	ErrorAt   *time.Time `json:"error_at,omitempty"`
}

// Deps is what a Runner backs up, and where.
type Deps struct {
	Store  Snapshotter
	Config func() (*config.Config, error)
	// LoopDir is the ~/.loop dir, whose playgrounds are backed up.
	LoopDir string
	// WorkDir holds the database snapshot while it's zipped into the
	// backup directory; the database's own dir, so there's room.
	WorkDir string
	// Home is what a leading ~/ in the backup path means.
	Home string
	// Version is the Loop version the manifest records.
	Version string
	// Projects lists the project dirs whose playgrounds are backed up.
	Projects func(context.Context) []string
	Logger   *slog.Logger
}

// Runner writes the backups the config schedules.
type Runner struct {
	Deps
	now     func() time.Time
	fs      fileSystem
	encrypt func(io.Writer, ...age.Recipient) (io.WriteCloser, error)

	mu      sync.Mutex
	running bool
	err     string
	errAt   time.Time
}

// NewRunner returns a Runner writing the backups d's config schedules.
func NewRunner(d Deps) *Runner {
	return &Runner{
		Deps:    d,
		now:     time.Now,
		fs:      osFS{},
		encrypt: age.Encrypt,
	}
}

// settings is the backup config with its path made absolute and its
// public key parsed.
type settings struct {
	config.BackupConfig
	dir       string
	recipient *age.X25519Recipient
}

func (r *Runner) settings() (settings, error) {
	cfg, err := r.Config()
	if err != nil {
		return settings{}, fmt.Errorf("loading config: %w", err)
	}
	s := settings{BackupConfig: cfg.Backup}
	path := s.Path
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		path = filepath.Join(r.Home, rest)
	}
	switch {
	case path == "":
	case !filepath.IsAbs(path):
		return s, fmt.Errorf("the backup path %q isn't absolute", s.Path)
	default:
		s.dir = filepath.Clean(path)
	}
	if s.Recipient != "" {
		if s.recipient, err = age.ParseX25519Recipient(s.Recipient); err != nil {
			return s, fmt.Errorf("the backup public key isn't an age public key (age1...): %w", err)
		}
	}
	return s, nil
}

func (s settings) interval() time.Duration {
	return 24 * time.Hour / time.Duration(s.TimesPerDay)
}

// Run writes each backup when it's due, checking every tick until ctx is
// done.
func (r *Runner) Run(ctx context.Context, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		r.runIfDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) runIfDue(ctx context.Context) {
	s, err := r.settings()
	if err != nil || !s.Enabled || s.dir == "" {
		return
	}
	backups, _ := r.list(s.dir)
	next := r.nextAt(s, backups)
	if r.now().Before(next) {
		return
	}
	if !r.begin() {
		return
	}
	if _, err := r.write(ctx, s); err != nil {
		r.Logger.Warn("backing up", "dir", s.dir, "error", err)
	}
}

// nextAt is when the next scheduled backup is due: an interval after the
// newest, or a while after a failure.
func (r *Runner) nextAt(s settings, backups []Backup) time.Time {
	var next time.Time
	if len(backups) > 0 {
		next = backups[0].At.Add(s.interval())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if retry := r.errAt.Add(retryAfter); !r.errAt.IsZero() && retry.After(next) {
		next = retry
	}
	return next
}

// Start writes a backup now, in the background, whether or not backups
// are scheduled.
func (r *Runner) Start() error {
	s, err := r.settings()
	if err != nil {
		return err
	}
	if s.dir == "" {
		return ErrNoPath
	}
	if !r.begin() {
		return ErrRunning
	}
	go func() {
		if _, err := r.write(context.Background(), s); err != nil {
			r.Logger.Warn("backing up", "dir", s.dir, "error", err)
		}
	}()
	return nil
}

// begin marks a backup as running, unless one already is.
func (r *Runner) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

// write writes a backup to s.dir and drops the ones no retention tier
// keeps, recording how it went. begin must have been called.
func (r *Runner) write(ctx context.Context, s settings) (*Backup, error) {
	b, err := r.backup(ctx, s)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = false
	if err != nil {
		r.err, r.errAt = err.Error(), r.now()
		return nil, err
	}
	r.err, r.errAt = "", time.Time{}
	r.Logger.Info("backed up", "path", b.Path, "size", b.Size)
	return b, nil
}

func (r *Runner) backup(ctx context.Context, s settings) (*Backup, error) {
	if err := r.fs.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", s.dir, err)
	}
	at := r.now().UTC().Truncate(time.Second)
	// A backup is never overwritten: a sync client could keep only the
	// newest content of a file name.
	existing, _ := r.list(s.dir)
	if slices.ContainsFunc(existing, func(b Backup) bool { return b.At.Equal(at) }) {
		return nil, fmt.Errorf("a backup for %s already exists", at.Format(time.RFC3339))
	}
	file := filePrefix + at.Format(timeLayout) + zipSuffix
	if s.recipient != nil {
		file = filePrefix + at.Format(timeLayout) + ageSuffix
	}
	snapshot := filepath.Join(r.WorkDir, ".backup-"+at.Format(timeLayout)+".db")
	_ = r.fs.Remove(snapshot)
	defer func() { _ = r.fs.Remove(snapshot) }()
	if err := r.Store.BackupInto(ctx, snapshot); err != nil {
		return nil, fmt.Errorf("snapshotting the database: %w", err)
	}
	size, err := r.save(ctx, s, snapshot, at, file)
	if err != nil {
		return nil, err
	}
	r.prune(s)
	return &Backup{File: file, Path: filepath.Join(s.dir, file), At: at, Size: size, Encrypted: s.recipient != nil}, nil
}

// save writes the archive of snapshot and the playgrounds into s.dir as
// file, encrypted when s has a public key, through a partial file so a
// sync client never uploads half a backup under the final name. It
// returns the file's size.
func (r *Runner) save(ctx context.Context, s settings, snapshot string, at time.Time, file string) (int64, error) {
	out, err := r.fs.CreateTemp(s.dir, partialPattern)
	if err != nil {
		return 0, fmt.Errorf("creating the backup: %w", err)
	}
	counter := &countingWriter{w: out}
	err = r.encrypted(counter, s, func(w io.Writer) error {
		return r.archive(ctx, w, snapshot, at)
	})
	// Sync and close even when writing failed, but report its error.
	err = cmp.Or(err, out.Sync())
	err = cmp.Or(err, out.Close())
	if err == nil {
		final := filepath.Join(s.dir, file)
		if _, statErr := r.fs.Lstat(final); statErr == nil {
			err = fmt.Errorf("%s already exists", file)
		} else {
			err = r.fs.Rename(out.Name(), final)
		}
	}
	if err != nil {
		_ = r.fs.Remove(out.Name())
		return 0, fmt.Errorf("writing the backup: %w", err)
	}
	return counter.n, nil
}

// encrypted runs write on w, or on an age encryptor writing to w when s
// has a public key.
func (r *Runner) encrypted(w io.Writer, s settings, write func(io.Writer) error) error {
	if s.recipient == nil {
		return write(w)
	}
	enc, err := r.encrypt(w, s.recipient)
	if err != nil {
		return fmt.Errorf("encrypting: %w", err)
	}
	return cmp.Or(write(enc), enc.Close())
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// prune drops the backups no retention tier keeps, and partial files left
// by a backup that didn't finish.
func (r *Runner) prune(s settings) {
	entries, err := r.fs.ReadDir(s.dir)
	if err != nil {
		r.Logger.Warn("listing backups to prune", "dir", s.dir, "error", err)
		return
	}
	var old []string
	for _, e := range entries {
		if ok, _ := filepath.Match(partialPattern, e.Name()); ok {
			old = append(old, e.Name())
		}
	}
	backups, _ := r.list(s.dir)
	keep := retain(backups, s.Retention)
	for _, b := range backups {
		if !keep[b.File] {
			old = append(old, b.File)
		}
	}
	for _, name := range old {
		if err := r.fs.Remove(filepath.Join(s.dir, name)); err != nil {
			r.Logger.Warn("pruning a backup", "file", name, "error", err)
		}
	}
}

// parseName returns the time a backup file name holds and whether it's
// encrypted, and false when name isn't a backup's.
func parseName(name string) (time.Time, bool, bool) {
	stamp, ok := strings.CutPrefix(name, filePrefix)
	if !ok {
		return time.Time{}, false, false
	}
	encrypted := true
	stamp, ok = strings.CutSuffix(stamp, ageSuffix)
	if !ok {
		encrypted = false
		if stamp, ok = strings.CutSuffix(stamp, zipSuffix); !ok {
			return time.Time{}, false, false
		}
	}
	at, err := time.Parse(timeLayout, stamp)
	if err != nil {
		return time.Time{}, false, false
	}
	return at, encrypted, true
}

// list returns the backups in dir, newest first. Files that aren't named
// like a backup are left out, so they're never pruned.
func (r *Runner) list(dir string) ([]Backup, error) {
	entries, err := r.fs.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		at, encrypted, ok := parseName(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		b := Backup{File: e.Name(), Path: filepath.Join(dir, e.Name()), At: at, Encrypted: encrypted}
		if info, err := e.Info(); err == nil {
			b.Size = info.Size()
		}
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b Backup) int { return b.At.Compare(a.At) })
	return out, nil
}

// Status reports the schedule, the backups and the last failure.
func (r *Runner) Status() Status {
	s, err := r.settings()
	st := Status{
		Enabled:     s.Enabled,
		Dir:         s.dir,
		TimesPerDay: s.TimesPerDay,
		KeepLast:    s.Retention.KeepLast,
		KeepDaily:   s.Retention.KeepDaily,
		KeepWeekly:  s.Retention.KeepWeekly,
		KeepMonthly: s.Retention.KeepMonthly,
		Recipient:   s.Recipient,
	}
	var backups []Backup
	if err == nil && s.dir != "" {
		backups, err = r.list(s.dir)
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
	}
	if len(backups) > 0 {
		st.Last = &backups[0]
	}
	st.Count = len(backups)
	for _, b := range backups {
		st.TotalSize += b.Size
	}
	if s.Enabled && s.dir != "" && s.TimesPerDay > 0 {
		next := r.nextAt(s, backups)
		if now := r.now(); now.After(next) {
			next = now
		}
		st.NextAt = &next
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st.Running = r.running
	switch {
	case err != nil:
		st.Error = err.Error()
	case r.err != "":
		st.Error = r.err
		errAt := r.errAt
		st.ErrorAt = &errAt
	}
	return st
}
