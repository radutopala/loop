package tunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TunnelSuite struct {
	suite.Suite
	binDir string
}

func TestTunnelSuite(t *testing.T) {
	suite.Run(t, new(TunnelSuite))
}

func (s *TunnelSuite) SetupTest() {
	s.binDir = s.T().TempDir()
}

// nopLogger satisfies Logger without output.
type nopLogger struct{}

func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

func newTestManager(binDir string) *Manager {
	return NewManager(binDir, nopLogger{})
}

// fakeResponse builds an *http.Response with the given body + status.
func fakeResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d", status),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

// --- scrapeURL ---

func (s *TunnelSuite) TestScrapeURLFound() {
	r := bytes.NewBufferString("some log line\n2026-07-15 INF |  https://happy-cloud-1234.trycloudflare.com  |\nmore\n")
	url, err := scrapeURL(context.Background(), r)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "https://happy-cloud-1234.trycloudflare.com", url)
}

// TestScrapeURLKeepsDrainingAfterURL is a regression test: after the URL is
// found, scrapeURL must keep consuming the reader so a continuously-logging
// cloudflared never blocks on a full stderr pipe.
func (s *TunnelSuite) TestScrapeURLKeepsDrainingAfterURL() {
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("INF https://drain-test.trycloudflare.com\n"))
		// Write far more than a pipe buffer would hold; if scrapeURL stopped
		// reading after the URL, these writes would block forever.
		for range 5000 {
			_, _ = pw.Write([]byte("more cloudflared log output line that keeps coming\n"))
		}
		_ = pw.Close()
	}()

	url, err := scrapeURL(context.Background(), pr)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "https://drain-test.trycloudflare.com", url)
	// The writer goroutine must be able to finish (it can't if the reader stalls).
	require.Eventually(s.T(), func() bool {
		_, err := pw.Write([]byte("x"))
		return err != nil // pipe closed → writer completed
	}, 2*time.Second, 10*time.Millisecond)
}

func (s *TunnelSuite) TestScrapeURLExitsBeforeURL() {
	r := bytes.NewBufferString("no url here\njust logs\n")
	_, err := scrapeURL(context.Background(), r)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "before announcing")
}

func (s *TunnelSuite) TestScrapeURLContextCancelled() {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := scrapeURL(ctx, pr)
	require.ErrorIs(s.T(), err, context.Canceled)
}

// --- ensureBinary: download + checksum ---

// rawBinary returns fake binary bytes and their sha256.
func rawBinary(content string) ([]byte, string) {
	b := []byte(content)
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:])
}

func (s *TunnelSuite) TestEnsureBinaryUsesExistingFile() {
	m := newTestManager(s.binDir)
	path := filepath.Join(s.binDir, binaryName())
	require.NoError(s.T(), os.WriteFile(path, []byte("existing"), 0o755))
	// httpGet must never be called.
	m.httpGet = func(string) (*http.Response, error) {
		s.T().Fatal("httpGet should not be called when binary exists")
		return nil, nil
	}
	got, err := m.ensureBinary()
	require.NoError(s.T(), err)
	require.Equal(s.T(), path, got)
}

func (s *TunnelSuite) TestEnsureBinaryDownloadRawSuccess() {
	key := currentKey()
	a := assetChecksums[key]
	if a.isTarGz {
		s.T().Skip("raw-binary asset test only meaningful on non-tgz platforms")
	}
	body, sum := rawBinary("#!/bin/sh\necho cloudflared\n")
	m := newTestManager(s.binDir)
	// Override the pinned checksum for this platform to match our fake body.
	restore := overrideChecksum(key, asset{name: a.name, sha256: sum, isTarGz: false})
	defer restore()
	m.httpGet = func(url string) (*http.Response, error) {
		require.Contains(s.T(), url, a.name)
		return fakeResponse(http.StatusOK, body), nil
	}
	path, err := m.ensureBinary()
	require.NoError(s.T(), err)
	got, _ := os.ReadFile(path)
	require.Equal(s.T(), body, got)
}

func (s *TunnelSuite) TestEnsureBinaryChecksumMismatch() {
	key := currentKey()
	a := assetChecksums[key]
	restore := overrideChecksum(key, asset{name: a.name, sha256: "deadbeef", isTarGz: a.isTarGz})
	defer restore()
	m := newTestManager(s.binDir)
	m.httpGet = func(string) (*http.Response, error) {
		return fakeResponse(http.StatusOK, []byte("wrong content")), nil
	}
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "checksum mismatch")
}

func (s *TunnelSuite) TestEnsureBinaryDownloadHTTPError() {
	m := newTestManager(s.binDir)
	m.httpGet = func(string) (*http.Response, error) {
		return fakeResponse(http.StatusNotFound, nil), nil
	}
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
}

func (s *TunnelSuite) TestEnsureBinaryTgzExtraction() {
	key := currentKey()
	a := assetChecksums[key]
	// Build a tgz containing a "cloudflared" entry.
	tgz := makeTgz(s.T(), "cloudflared", []byte("BINARY"))
	sum := sha256.Sum256(tgz)
	restore := overrideChecksum(key, asset{name: a.name, sha256: hex.EncodeToString(sum[:]), isTarGz: true})
	defer restore()
	m := newTestManager(s.binDir)
	m.httpGet = func(string) (*http.Response, error) {
		return fakeResponse(http.StatusOK, tgz), nil
	}
	path, err := m.ensureBinary()
	require.NoError(s.T(), err)
	got, _ := os.ReadFile(path)
	require.Equal(s.T(), []byte("BINARY"), got)
}

// --- Start / Stop ---

func (s *TunnelSuite) TestStartStopWithFakeExec() {
	m := newTestManager(s.binDir)
	// Pretend the binary is already installed.
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.binDir, binaryName()), []byte("x"), 0o755))

	var killed bool
	m.killProcess = func(cmd *exec.Cmd, _ time.Duration) {
		killed = true
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	// Fake cloudflared: a shell command that prints a trycloudflare URL to
	// stderr then sleeps so the process stays alive.
	m.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c",
			"echo 'INF |  https://test-tunnel-42.trycloudflare.com  |' 1>&2; sleep 30")
	}

	url, err := m.Start(context.Background(), 12345)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "https://test-tunnel-42.trycloudflare.com", url)
	require.True(s.T(), m.Running())
	require.Equal(s.T(), url, m.PublicURL())

	// Starting again returns the same URL without spawning.
	url2, err := m.Start(context.Background(), 12345)
	require.NoError(s.T(), err)
	require.Equal(s.T(), url, url2)

	m.Stop()
	require.False(s.T(), m.Running())
	require.Empty(s.T(), m.PublicURL())
	require.True(s.T(), killed)
}

// TestStartProcessOutlivesCallerContext is a regression test: the cloudflared
// subprocess must NOT be tied to the context passed to Start (e.g. an HTTP
// request context). If it were, the tunnel would die the instant that context
// is cancelled — producing Cloudflare Error 1033 for anyone opening the URL.
func (s *TunnelSuite) TestStartProcessOutlivesCallerContext() {
	m := newTestManager(s.binDir)
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.binDir, binaryName()), []byte("x"), 0o755))

	// The fake honors whatever context Start hands to execCommand. If Start
	// (incorrectly) forwarded the caller ctx, cancelling it below would reap
	// the process and flip Running() to a dead process.
	var childCtx context.Context
	m.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		childCtx = ctx
		return exec.CommandContext(ctx, "sh", "-c",
			"echo 'https://outlive-test.trycloudflare.com' 1>&2; sleep 30")
	}

	ctx, cancel := context.WithCancel(context.Background())
	url, err := m.Start(ctx, 8080)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "https://outlive-test.trycloudflare.com", url)

	// Cancel the context that was passed into Start.
	cancel()
	// The child was spawned with a background context, not the caller's, so it
	// keeps running.
	require.NotNil(s.T(), childCtx)
	require.NoError(s.T(), childCtx.Err(), "child context must not be the cancelled caller context")
	require.True(s.T(), m.Running())

	m.Stop()
	require.False(s.T(), m.Running())
}

func (s *TunnelSuite) TestStartBinaryError() {
	m := newTestManager(s.binDir)
	m.httpGet = func(string) (*http.Response, error) {
		return fakeResponse(http.StatusOK, []byte("bad")), nil
	}
	// No existing binary + checksum mismatch → Start fails at ensureBinary.
	_, err := m.Start(context.Background(), 9999)
	require.Error(s.T(), err)
}

func (s *TunnelSuite) TestStopWhenNotRunning() {
	m := newTestManager(s.binDir)
	m.Stop() // must not panic
	require.False(s.T(), m.Running())
}

func (s *TunnelSuite) TestEnsureBinaryUnsupportedPlatform() {
	m := newTestManager(s.binDir)
	// Remove all known keys so the current platform is "unsupported".
	saved := assetChecksums
	assetChecksums = map[string]asset{}
	defer func() { assetChecksums = saved }()
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not supported")
}

func (s *TunnelSuite) TestEnsureBinaryHTTPGetError() {
	m := newTestManager(s.binDir)
	m.httpGet = func(string) (*http.Response, error) {
		return nil, fmt.Errorf("network down")
	}
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "network down")
}

func (s *TunnelSuite) TestEnsureBinaryMkdirError() {
	m := newTestManager(s.binDir)
	m.mkdirAll = func(string, os.FileMode) error { return fmt.Errorf("mkdir boom") }
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "mkdir boom")
}

func (s *TunnelSuite) TestEnsureBinaryWriteError() {
	key := currentKey()
	a := assetChecksums[key]
	body, sum := rawBinary("bin")
	restore := overrideChecksum(key, asset{name: a.name, sha256: sum, isTarGz: false})
	defer restore()
	m := newTestManager(s.binDir)
	if a.isTarGz {
		// On tgz platforms, wrap the fake body in a tgz so extraction succeeds.
		tgz := makeTgz(s.T(), "cloudflared", body)
		sum2 := sha256.Sum256(tgz)
		restore2 := overrideChecksum(key, asset{name: a.name, sha256: hex.EncodeToString(sum2[:]), isTarGz: true})
		defer restore2()
		m.httpGet = func(string) (*http.Response, error) { return fakeResponse(http.StatusOK, tgz), nil }
	} else {
		m.httpGet = func(string) (*http.Response, error) { return fakeResponse(http.StatusOK, body), nil }
	}
	m.writeFile = func(string, []byte, os.FileMode) error { return fmt.Errorf("write boom") }
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "write boom")
}

func (s *TunnelSuite) TestEnsureBinaryChmodError() {
	m, restore := s.managerWithValidDownload()
	defer restore()
	m.chmod = func(string, os.FileMode) error { return fmt.Errorf("chmod boom") }
	var removed bool
	m.removeFile = func(string) error { removed = true; return nil }
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "chmod boom")
	require.True(s.T(), removed)
}

func (s *TunnelSuite) TestEnsureBinaryRenameError() {
	m, restore := s.managerWithValidDownload()
	defer restore()
	m.rename = func(string, string) error { return fmt.Errorf("rename boom") }
	var removed bool
	m.removeFile = func(string) error { removed = true; return nil }
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "rename boom")
	require.True(s.T(), removed)
}

func (s *TunnelSuite) TestExtractCloudflaredNotFound() {
	tgz := makeTgz(s.T(), "some-other-file", []byte("x"))
	_, err := extractCloudflared(tgz)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found in archive")
}

func (s *TunnelSuite) TestExtractCloudflaredBadGzip() {
	_, err := extractCloudflared([]byte("not a gzip stream"))
	require.Error(s.T(), err)
}

func (s *TunnelSuite) TestScrapeURLTimeout() {
	prev := urlScrapeTimeout
	urlScrapeTimeout = 10 * time.Millisecond
	defer func() { urlScrapeTimeout = prev }()
	pr, pw := io.Pipe()
	defer pw.Close()
	_, err := scrapeURL(context.Background(), pr)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "timed out")
}

func (s *TunnelSuite) TestKillProcessGroupRealProcess() {
	cmd := exec.Command("sh", "-c", "sleep 30")
	configureProcAttr(cmd)
	require.NoError(s.T(), cmd.Start())
	killProcessGroup(cmd, 2*time.Second)
	// Process must be gone; a second Wait returns an error (already waited).
	require.Error(s.T(), cmd.Wait())
}

func (s *TunnelSuite) TestKillProcessGroupNoProcess() {
	killProcessGroup(&exec.Cmd{}, time.Second) // must not panic
}

func (s *TunnelSuite) TestStartStderrPipeError() {
	m := newTestManager(s.binDir)
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.binDir, binaryName()), []byte("x"), 0o755))
	m.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "sleep 1")
	}
	m.stderrPipe = func(*exec.Cmd) (io.ReadCloser, error) {
		return nil, fmt.Errorf("pipe boom")
	}
	_, err := m.Start(context.Background(), 1234)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "stderr pipe")
}

// errReader fails on Read to exercise the download body-read error path.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, fmt.Errorf("read boom") }

func (s *TunnelSuite) TestEnsureBinaryBodyReadError() {
	m := newTestManager(s.binDir)
	m.httpGet = func(string) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(errReader{})}, nil
	}
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading cloudflared download")
}

func (s *TunnelSuite) TestStartCmdStartError() {
	m := newTestManager(s.binDir)
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.binDir, binaryName()), []byte("x"), 0o755))
	// Point execCommand at a non-existent binary so cmd.Start() fails.
	m.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(s.binDir, "does-not-exist-binary"))
	}
	_, err := m.Start(context.Background(), 1234)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting cloudflared")
}

func (s *TunnelSuite) TestStartScrapeFailKillsProcess() {
	m := newTestManager(s.binDir)
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.binDir, binaryName()), []byte("x"), 0o755))
	var killed bool
	m.killProcess = func(cmd *exec.Cmd, _ time.Duration) {
		killed = true
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	// Process exits immediately without ever printing a URL.
	m.execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo no-url-here 1>&2; exit 0")
	}
	_, err := m.Start(context.Background(), 1234)
	require.Error(s.T(), err)
	require.True(s.T(), killed)
	require.False(s.T(), m.Running())
}

func (s *TunnelSuite) TestKillProcessGroupEscalatesToSIGKILL() {
	// The whole group ignores SIGTERM (sh traps it and stays alive in a busy
	// loop), forcing the SIGKILL escalation path after the timeout.
	cmd := exec.Command("sh", "-c", "trap '' TERM; while true; do :; done")
	configureProcAttr(cmd)
	require.NoError(s.T(), cmd.Start())
	// Give the shell a moment to install the TERM trap before we signal it,
	// otherwise the default disposition kills it before escalation kicks in.
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	killProcessGroup(cmd, 150*time.Millisecond)
	require.Error(s.T(), cmd.Wait())
	require.GreaterOrEqual(s.T(), time.Since(start), 140*time.Millisecond)
}

func (s *TunnelSuite) TestEnsureBinaryExtractError() {
	key := currentKey()
	a := assetChecksums[key]
	// Force the tgz path with a valid checksum but a body that isn't a valid
	// gzip stream, so extractCloudflared fails inside ensureBinary.
	bad := []byte("not-a-gzip")
	sum := sha256.Sum256(bad)
	restore := overrideChecksum(key, asset{name: a.name, sha256: hex.EncodeToString(sum[:]), isTarGz: true})
	defer restore()
	m := newTestManager(s.binDir)
	m.httpGet = func(string) (*http.Response, error) { return fakeResponse(http.StatusOK, bad), nil }
	_, err := m.ensureBinary()
	require.Error(s.T(), err)
}

func (s *TunnelSuite) TestExtractCloudflaredTruncatedEntry() {
	// A valid tar header for "cloudflared" declaring more bytes than follow,
	// so io.ReadAll on the entry hits an unexpected EOF.
	var tbuf bytes.Buffer
	tw := tar.NewWriter(&tbuf)
	require.NoError(s.T(), tw.WriteHeader(&tar.Header{
		Name: "cloudflared", Mode: 0o755, Size: 512, Typeflag: tar.TypeReg,
	}))
	_, _ = tw.Write(make([]byte, 100)) // fewer than declared; skip tw.Close()
	// Truncate the raw tar bytes mid-entry, then gzip them.
	raw := tbuf.Bytes()
	var gbuf bytes.Buffer
	gzw := gzip.NewWriter(&gbuf)
	_, _ = gzw.Write(raw)
	require.NoError(s.T(), gzw.Close())
	_, err := extractCloudflared(gbuf.Bytes())
	require.Error(s.T(), err)
}

func (s *TunnelSuite) TestExtractCloudflaredTarReadError() {
	// A valid gzip stream whose decompressed content is not a valid tar
	// archive → tar.Next returns a non-EOF error.
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	_, _ = gzw.Write([]byte("this is gzipped but not a tar archive at all"))
	require.NoError(s.T(), gzw.Close())
	_, err := extractCloudflared(buf.Bytes())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading cloudflared archive")
}

// managerWithValidDownload returns a Manager whose httpGet yields a
// checksum-valid asset for the current platform, plus a restore func.
func (s *TunnelSuite) managerWithValidDownload() (*Manager, func()) {
	key := currentKey()
	a := assetChecksums[key]
	m := newTestManager(s.binDir)
	if a.isTarGz {
		tgz := makeTgz(s.T(), "cloudflared", []byte("BIN"))
		sum := sha256.Sum256(tgz)
		restore := overrideChecksum(key, asset{name: a.name, sha256: hex.EncodeToString(sum[:]), isTarGz: true})
		m.httpGet = func(string) (*http.Response, error) { return fakeResponse(http.StatusOK, tgz), nil }
		return m, restore
	}
	body, sum := rawBinary("BIN")
	restore := overrideChecksum(key, asset{name: a.name, sha256: sum, isTarGz: false})
	m.httpGet = func(string) (*http.Response, error) { return fakeResponse(http.StatusOK, body), nil }
	return m, restore
}

// --- helpers ---

func currentKey() string {
	k := runtime.GOOS + "/" + runtime.GOARCH
	if _, ok := assetChecksums[k]; ok {
		return k
	}
	// Fall back to any supported key so tests run on unusual platforms.
	return "linux/amd64"
}

func overrideChecksum(key string, a asset) func() {
	prev, existed := assetChecksums[key]
	assetChecksums[key] = a
	return func() {
		if existed {
			assetChecksums[key] = prev
		} else {
			delete(assetChecksums, key)
		}
	}
}

func makeTgz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gzw.Close())
	return buf.Bytes()
}
