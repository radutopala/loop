// Package tunnel manages a cloudflared quick tunnel used to expose a local
// playground-only HTTP listener over the internet. The cloudflared binary is
// downloaded lazily to ~/.loop/bin on first use and pinned to a known release
// by sha256 (fail-closed). The tunnel runs as a managed subprocess whose
// stdout is scraped for the assigned trycloudflare.com URL.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// pinnedVersion is the cloudflared release the checksums below correspond to.
// Bumping it requires refreshing assetChecksums with the new release digests.
const pinnedVersion = "2026.7.2"

// asset describes the platform-specific cloudflared download: the GitHub
// release asset name, its pinned sha256, and whether it is a .tgz that must be
// extracted (macOS) versus a raw binary (linux/windows).
type asset struct {
	name    string
	sha256  string
	isTarGz bool
}

// assetChecksums pins the sha256 of each supported cloudflared asset for
// pinnedVersion, keyed by "GOOS/GOARCH". Values are the release asset digests.
var assetChecksums = map[string]asset{
	"darwin/amd64":  {name: "cloudflared-darwin-amd64.tgz", sha256: "4ee0d3b48a990a2f9b5faec5838f73ec1f400aa8e0a4864be576adfafec406cb", isTarGz: true},
	"darwin/arm64":  {name: "cloudflared-darwin-arm64.tgz", sha256: "2086e51c61d6565781d84117a5007d0c826d03ffdc74acb91c08c167f9f8cd7c", isTarGz: true},
	"linux/amd64":   {name: "cloudflared-linux-amd64", sha256: "ec905ea7b7e327ff8abdde8cb64697a2152de74dbcdbf6aec9db8364eb3886cd"},
	"linux/arm64":   {name: "cloudflared-linux-arm64", sha256: "405df476437e027fc6d18729a5a77155c0a33a6082aeee60a799a688f3052e66"},
	"windows/amd64": {name: "cloudflared-windows-amd64.exe", sha256: "cdb5d4432f6ae1595654a692a51308b69d2bf7af961f5578d9391837cf072df9"},
}

// quickTunnelURLRe matches the trycloudflare.com URL cloudflared prints once
// the quick tunnel is established.
var quickTunnelURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// urlScrapeTimeout bounds how long we wait for cloudflared to announce its URL.
// A var (not const) so tests can shorten it.
var urlScrapeTimeout = 30 * time.Second

// Manager owns the cloudflared subprocess lifecycle. It is safe for concurrent
// use; callers coordinate start/stop through the exported methods.
type Manager struct {
	binDir string
	logger Logger

	mu        sync.Mutex
	cmd       *exec.Cmd
	publicURL string
	running   bool

	// Injectable seams for tests (no global mocks).
	httpGet     func(url string) (*http.Response, error)
	execCommand func(ctx context.Context, name string, args ...string) *exec.Cmd
	statFile    func(string) (os.FileInfo, error)
	killProcess func(cmd *exec.Cmd, timeout time.Duration)
	mkdirAll    func(string, os.FileMode) error
	writeFile   func(string, []byte, os.FileMode) error
	chmod       func(string, os.FileMode) error
	rename      func(string, string) error
	removeFile  func(string) error
	stderrPipe  func(*exec.Cmd) (io.ReadCloser, error)
}

// Logger is the minimal logging surface the manager needs.
type Logger interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

// NewManager builds a Manager storing the cloudflared binary under binDir
// (typically ~/.loop/bin).
func NewManager(binDir string, logger Logger) *Manager {
	return &Manager{
		binDir:      binDir,
		logger:      logger,
		httpGet:     http.Get,
		execCommand: exec.CommandContext,
		statFile:    os.Stat,
		killProcess: killProcessGroup,
		mkdirAll:    os.MkdirAll,
		writeFile:   os.WriteFile,
		chmod:       os.Chmod,
		rename:      os.Rename,
		removeFile:  os.Remove,
		stderrPipe:  func(c *exec.Cmd) (io.ReadCloser, error) { return c.StderrPipe() },
	}
}

// PublicURL returns the current tunnel URL, or "" when not running.
func (m *Manager) PublicURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publicURL
}

// Running reports whether the tunnel subprocess is active.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Start ensures the cloudflared binary is present (downloading + verifying it
// on first use) and spawns a quick tunnel pointing at localPort, returning the
// public trycloudflare.com URL once cloudflared announces it. Calling Start
// while already running returns the existing URL.
//
// ctx bounds only the wait for cloudflared to announce its URL. The subprocess
// itself is spawned with a background context so it OUTLIVES the caller (e.g.
// the HTTP request that triggered the share) — its lifetime is owned by Stop().
// Tying the process to a request context would kill the tunnel the moment the
// request returned (Cloudflare Error 1033).
func (m *Manager) Start(ctx context.Context, localPort int) (string, error) {
	m.mu.Lock()
	if m.running {
		url := m.publicURL
		m.mu.Unlock()
		return url, nil
	}
	m.mu.Unlock()

	binPath, err := m.ensureBinary()
	if err != nil {
		return "", err
	}

	cmd := m.execCommand(context.Background(), binPath,
		"tunnel", "--url", fmt.Sprintf("http://127.0.0.1:%d", localPort),
		"--no-autoupdate",
	)
	configureProcAttr(cmd)

	// cloudflared logs the quick-tunnel URL to stderr; scrape that and drain
	// stdout so the pipe buffer never blocks the child.
	stderr, err := m.stderrPipe(cmd)
	if err != nil {
		return "", fmt.Errorf("cloudflared stderr pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		go drain(stdout)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting cloudflared: %w", err)
	}

	url, err := scrapeURL(ctx, stderr)
	if err != nil {
		m.killProcess(cmd, 5*time.Second)
		return "", err
	}

	m.mu.Lock()
	m.cmd = cmd
	m.publicURL = url
	m.running = true
	m.mu.Unlock()

	m.logger.Info("cloudflared quick tunnel started", "url", url, "port", localPort)
	return url, nil
}

// Stop terminates the cloudflared subprocess (process-group SIGTERM →
// timeout → SIGKILL) and clears state. Safe to call when not running.
func (m *Manager) Stop() {
	m.mu.Lock()
	cmd := m.cmd
	running := m.running
	m.cmd = nil
	m.publicURL = ""
	m.running = false
	m.mu.Unlock()

	if !running || cmd == nil {
		return
	}
	m.killProcess(cmd, 5*time.Second)
	m.logger.Info("cloudflared quick tunnel stopped")
}

// scrapeURL reads cloudflared output until it finds the trycloudflare URL or
// the timeout/ctx elapses. Crucially, after the URL is found it KEEPS reading
// (draining) the reader in the background: cloudflared logs continuously to
// stderr, and if that pipe is left unread it fills its 64KB buffer, blocks the
// process on write (stalling the tunnel), and hangs cmd.Wait() during teardown.
func scrapeURL(ctx context.Context, r io.Reader) (string, error) {
	type result struct {
		url string
	}
	found := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		sent := false
		for scanner.Scan() {
			if !sent {
				if m := quickTunnelURLRe.FindString(scanner.Text()); m != "" {
					found <- result{url: m}
					sent = true
				}
			}
			// Keep consuming after the URL is found so the pipe never blocks.
		}
		if !sent {
			found <- result{}
		}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(urlScrapeTimeout):
		return "", fmt.Errorf("timed out waiting for cloudflared tunnel URL")
	case res := <-found:
		if res.url == "" {
			return "", fmt.Errorf("cloudflared exited before announcing a tunnel URL")
		}
		return res.url, nil
	}
}

func drain(r io.Reader) {
	_, _ = io.Copy(io.Discard, r)
}
