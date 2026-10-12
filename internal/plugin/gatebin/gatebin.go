// Package gatebin finds the linux loop binary plugin containers run their
// agentgate with. A plugin image doesn't contain loop, so the binary is
// mounted in: the release of the daemon's own version, downloaded once and
// checked against the release's checksums, or a build the owner names.
package gatebin

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ReleaseURL is where loop's releases are downloaded from.
const ReleaseURL = "https://github.com/radutopala/loop/releases/download"

// maxArchiveBytes caps a downloaded release archive.
const maxArchiveBytes = 256 << 20

// releaseRe matches the version of a release build; anything else is a dev
// build, which has no release to download.
var releaseRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// Config configures a Resolver.
type Config struct {
	// Override, when set, is the binary to use: a linux build of this loop.
	Override string
	// Version is the daemon's version.
	Version string
	// CacheDir is where downloaded binaries are kept.
	CacheDir string
	// BaseURL is where releases are downloaded from; ReleaseURL when empty.
	BaseURL string
	// Arch returns the architecture plugin containers run on, as Go names
	// it (amd64, arm64).
	Arch func(ctx context.Context) (string, error)
	// Get fetches a URL.
	Get func(ctx context.Context, url string) (*http.Response, error)
}

// Resolver finds the gate binary, once.
type Resolver struct {
	cfg  Config
	mu   sync.Mutex
	path string
}

// New returns a Resolver.
func New(cfg Config) *Resolver {
	if cfg.BaseURL == "" {
		cfg.BaseURL = ReleaseURL
	}
	return &Resolver{cfg: cfg}
}

// Path returns the host path of the gate binary, downloading it the first
// time it's needed.
func (r *Resolver) Path(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.path != "" {
		return r.path, nil
	}
	path, err := r.resolve(ctx)
	if err != nil {
		return "", err
	}
	r.path = path
	return path, nil
}

func (r *Resolver) resolve(ctx context.Context) (string, error) {
	if r.cfg.Override != "" {
		info, err := os.Stat(r.cfg.Override)
		if err != nil {
			return "", fmt.Errorf("plugins.gate_binary: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("plugins.gate_binary: %s is not a file", r.cfg.Override)
		}
		return filepath.Abs(r.cfg.Override)
	}
	if !releaseRe.MatchString(r.cfg.Version) {
		return "", fmt.Errorf("loop %s is a dev build with no release to take the plugin gate from; set plugins.gate_binary to a linux build of it (make build-linux)", r.cfg.Version)
	}
	arch, err := r.cfg.Arch(ctx)
	if err != nil {
		return "", fmt.Errorf("finding the plugin containers' architecture: %w", err)
	}
	version := strings.TrimPrefix(r.cfg.Version, "v")
	dir := filepath.Join(r.cfg.CacheDir, version, "linux_"+arch)
	path := filepath.Join(dir, "loop")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	archive := fmt.Sprintf("loop_%s_linux_%s.tar.gz", version, arch)
	base := fmt.Sprintf("%s/v%s/", r.cfg.BaseURL, version)
	sums, err := r.fetch(ctx, base+"checksums.txt")
	if err != nil {
		return "", err
	}
	want, err := checksum(sums, archive)
	if err != nil {
		return "", err
	}
	body, err := r.fetch(ctx, base+archive)
	if err != nil {
		return "", err
	}
	if got := sha256.Sum256(body); hex.EncodeToString(got[:]) != want {
		return "", fmt.Errorf("%s doesn't match its checksum", archive)
	}
	bin, err := extract(body)
	if err != nil {
		return "", fmt.Errorf("%s: %w", archive, err)
	}
	if err := writeAtomic(dir, path, bin); err != nil {
		return "", err
	}
	return path, nil
}

// fetch downloads url, up to maxArchiveBytes.
func (r *Resolver) fetch(ctx context.Context, url string) ([]byte, error) {
	resp, err := r.cfg.Get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	if len(body) > maxArchiveBytes {
		return nil, fmt.Errorf("downloading %s: over %d bytes", url, maxArchiveBytes)
	}
	return body, nil
}

// checksum returns the sha256 checksums.txt lists for name.
func checksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("the release's checksums.txt has no %s", name)
}

// extract returns the loop binary in a .tar.gz.
func extract(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no loop binary in it")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "loop" {
			return io.ReadAll(tr)
		}
	}
}

// writeAtomic writes an executable to path through a temp file in dir, so a
// half-written binary is never found.
func writeAtomic(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating the gate binary dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".loop-*")
	if err != nil {
		return fmt.Errorf("writing the gate binary: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("writing the gate binary: %w", err)
	}
	return nil
}

// HTTPGet fetches url with client; the default Config.Get.
func HTTPGet(client *http.Client) func(ctx context.Context, url string) (*http.Response, error) {
	return func(ctx context.Context, url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
}
