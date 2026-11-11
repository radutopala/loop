package dictation

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// HTTPClient abstracts HTTP calls for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Progress is the state of the model download.
type Progress struct {
	Active   bool   `json:"active"`
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Error    string `json:"error"`
}

// Downloader fetches models into a directory, one at a time.
type Downloader struct {
	dir     string
	baseURL string
	client  HTTPClient
	logger  *slog.Logger

	mu       sync.Mutex
	progress Progress
}

// NewDownloader returns a downloader into dir.
func NewDownloader(dir string, client HTTPClient, logger *slog.Logger) *Downloader {
	return &Downloader{dir: dir, baseURL: modelBaseURL, client: client, logger: logger}
}

// Path is where m is stored.
func (d *Downloader) Path(m Model) string { return filepath.Join(d.dir, m.FileName()) }

// Ready reports whether m is downloaded.
func (d *Downloader) Ready(m Model) bool {
	info, err := os.Stat(d.Path(m))
	return err == nil && info.Mode().IsRegular() && info.Size() == m.Size
}

// Progress returns the current download's state, or the last one's.
func (d *Downloader) Progress() Progress {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.progress
}

// Start downloads m in the background unless it's downloaded or a download
// is running. done, when not nil, is closed once the download ends.
func (d *Downloader) Start(ctx context.Context, m Model, done chan<- struct{}) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.progress.Active || d.Ready(m) {
		return false
	}
	d.progress = Progress{Active: true, Total: m.Size}
	go func() {
		err := d.fetch(ctx, m)
		d.mu.Lock()
		d.progress.Active = false
		if err != nil {
			d.progress.Error = err.Error()
			d.logger.Warn("dictation model download failed", "model", m.Name, "error", err)
		} else {
			d.logger.Info("dictation model downloaded", "model", m.Name)
		}
		d.mu.Unlock()
		if done != nil {
			close(done)
		}
	}()
	return true
}

// errChecksum reports a download that isn't the expected file.
var errChecksum = errors.New("checksum mismatch")

func (d *Downloader) fetch(ctx context.Context, m Model) error {
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+m.FileName(), nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", m.FileName(), resp.StatusCode)
	}
	part := d.Path(m) + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h, progressWriter{d}), resp.Body)
	if err = cmp.Or(err, f.Close()); err == nil && (n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256) {
		err = fmt.Errorf("download %s: %w", m.FileName(), errChecksum)
	}
	if err == nil {
		err = os.Rename(part, d.Path(m))
	}
	if err != nil {
		_ = os.Remove(part)
	}
	return err
}

// progressWriter counts downloaded bytes into the progress.
type progressWriter struct{ d *Downloader }

func (w progressWriter) Write(p []byte) (int, error) {
	w.d.mu.Lock()
	w.d.progress.Received += int64(len(p))
	w.d.mu.Unlock()
	return len(p), nil
}
