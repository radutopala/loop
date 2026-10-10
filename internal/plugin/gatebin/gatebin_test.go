package gatebin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GatebinSuite struct {
	suite.Suite
	dir     string
	archive []byte
	sums    string
	// files maps a URL path to what the test server answers it with.
	files map[string]string
	srv   *httptest.Server
	gets  int
}

func TestGatebin(t *testing.T) {
	suite.Run(t, new(GatebinSuite))
}

func tarGz(t require.TestingT, entries map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		typ := byte(tar.TypeReg)
		if strings.HasSuffix(name, "/") {
			typ = tar.TypeDir
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: typ, Mode: 0o755, Size: int64(len(body))}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (s *GatebinSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.archive = tarGz(s.T(), map[string]string{"README.md": "hi", "loop": "ELF"})
	s.sums = sha([]byte("other")) + "  loop_2026.10.3_linux_amd64.tar.gz\n" + sha(s.archive) + "  loop_2026.10.3_linux_arm64.tar.gz\n"
	s.files = map[string]string{
		"/v2026.10.3/checksums.txt":                     s.sums,
		"/v2026.10.3/loop_2026.10.3_linux_arm64.tar.gz": string(s.archive),
	}
	s.gets = 0
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.gets++
		body, ok := s.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	s.T().Cleanup(s.srv.Close)
}

func (s *GatebinSuite) resolver(mod func(*Config)) *Resolver {
	cfg := Config{
		Version:  "2026.10.3",
		CacheDir: filepath.Join(s.dir, "cache"),
		BaseURL:  s.srv.URL,
		Arch:     func(context.Context) (string, error) { return "arm64", nil },
		Get:      HTTPGet(s.srv.Client()),
	}
	if mod != nil {
		mod(&cfg)
	}
	return New(cfg)
}

func (s *GatebinSuite) TestDownloadsVerifiesAndCaches() {
	r := s.resolver(nil)
	path, err := r.Path(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(filepath.Join(s.dir, "cache", "2026.10.3", "linux_arm64", "loop"), path)
	body, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Require().Equal("ELF", string(body))
	info, err := os.Stat(path)
	s.Require().NoError(err)
	s.Require().Equal(os.FileMode(0o755), info.Mode().Perm())
	s.Require().Equal(2, s.gets)

	// The resolver remembers it, and a new one finds it in the cache.
	_, err = r.Path(context.Background())
	s.Require().NoError(err)
	again, err := s.resolver(func(c *Config) { c.Version = "v2026.10.3" }).Path(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(path, again)
	s.Require().Equal(2, s.gets)
	entries, err := os.ReadDir(filepath.Dir(path))
	s.Require().NoError(err)
	s.Require().Len(entries, 1)
}

func (s *GatebinSuite) TestOverride() {
	bin := filepath.Join(s.dir, "loop-linux")
	s.Require().NoError(os.WriteFile(bin, []byte("ELF"), 0o755))
	path, err := s.resolver(func(c *Config) { c.Override = bin; c.Version = "dev" }).Path(context.Background())
	s.Require().NoError(err)
	s.Require().Equal(bin, path)
	s.Require().Zero(s.gets)
}

func (s *GatebinSuite) TestErrors() {
	cacheFile := filepath.Join(s.dir, "file")
	s.Require().NoError(os.WriteFile(cacheFile, nil, 0o644))
	defaults := s.files
	notGzip := "not a gzip archive at all"
	tests := []struct {
		name  string
		mod   func(*Config)
		files map[string]string
		want  string
	}{
		{name: "override missing", mod: func(c *Config) { c.Override = filepath.Join(s.dir, "nope") }, want: "plugins.gate_binary: stat"},
		{name: "override a dir", mod: func(c *Config) { c.Override = s.dir }, want: "is not a file"},
		{name: "dev build", mod: func(c *Config) { c.Version = "v2026.10.3-4-gabc-dirty" }, want: "is a dev build with no release"},
		{name: "no arch", mod: func(c *Config) { c.Arch = func(context.Context) (string, error) { return "", errors.New("no docker") } }, want: "architecture: no docker"},
		{name: "get fails", mod: func(c *Config) {
			c.Get = func(context.Context, string) (*http.Response, error) { return nil, errors.New("offline") }
		}, want: "checksums.txt: offline"},
		{name: "no checksums", files: map[string]string{}, want: "checksums.txt: 404 Not Found"},
		{name: "not in checksums", files: map[string]string{"/v2026.10.3/checksums.txt": "abc  other.tar.gz\nmalformed\n"}, want: "has no loop_2026.10.3_linux_arm64.tar.gz"},
		{name: "no archive", files: map[string]string{"/v2026.10.3/checksums.txt": "abc  loop_2026.10.3_linux_arm64.tar.gz\n"}, want: "tar.gz: 404 Not Found"},
		{name: "checksum mismatch", files: map[string]string{
			"/v2026.10.3/checksums.txt":                     "abc  loop_2026.10.3_linux_arm64.tar.gz\n",
			"/v2026.10.3/loop_2026.10.3_linux_arm64.tar.gz": "x",
		}, want: "doesn't match its checksum"},
		{name: "not gzip", files: map[string]string{
			"/v2026.10.3/checksums.txt":                     sha([]byte(notGzip)) + "  loop_2026.10.3_linux_arm64.tar.gz\n",
			"/v2026.10.3/loop_2026.10.3_linux_arm64.tar.gz": notGzip,
		}, want: "gzip: invalid header"},
		{name: "no loop in it", files: s.archiveFiles(map[string]string{"bin/": "", "README.md": "hi"}), want: "no loop binary in it"},
		{name: "cut short", files: s.archiveFiles(nil), want: "unexpected EOF"},
		{name: "cache dir unwritable", mod: func(c *Config) { c.CacheDir = cacheFile }, want: "creating the gate binary dir"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.files = defaults
			if tt.files != nil {
				s.files = tt.files
			}
			_, err := s.resolver(tt.mod).Path(context.Background())
			s.Require().ErrorContains(err, tt.want)
		})
	}
}

// archiveFiles serves an archive of entries, or one cut short when entries
// is nil, with a matching checksum.
func (s *GatebinSuite) archiveFiles(entries map[string]string) map[string]string {
	archive := tarGz(s.T(), map[string]string{"loop": strings.Repeat("x", 4096)})
	archive = archive[:len(archive)/2]
	if entries != nil {
		archive = tarGz(s.T(), entries)
	}
	return map[string]string{
		"/v2026.10.3/checksums.txt":                     sha(archive) + "  loop_2026.10.3_linux_arm64.tar.gz\n",
		"/v2026.10.3/loop_2026.10.3_linux_arm64.tar.gz": string(archive),
	}
}

func (s *GatebinSuite) TestFetchLimits() {
	r := s.resolver(func(c *Config) {
		c.Get = func(context.Context, string) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(io.LimitReader(zeros{}, maxArchiveBytes+1))}, nil
		}
	})
	_, err := r.fetch(context.Background(), "u")
	s.Require().ErrorContains(err, fmt.Sprintf("over %d bytes", maxArchiveBytes))

	r = s.resolver(func(c *Config) {
		c.Get = func(context.Context, string) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(failingReader{})}, nil
		}
	})
	_, err = r.fetch(context.Background(), "u")
	s.Require().ErrorContains(err, "downloading u: read failed")
}

func (s *GatebinSuite) TestHTTPGetBadURL() {
	_, err := HTTPGet(http.DefaultClient)(context.Background(), "://bad")
	s.Require().Error(err)
}

func (s *GatebinSuite) TestWriteAtomicErrors() {
	// A dir in the way of the binary fails the rename.
	dir := filepath.Join(s.dir, "w")
	path := filepath.Join(dir, "loop")
	s.Require().NoError(os.MkdirAll(filepath.Join(path, "x"), 0o755))
	s.Require().ErrorContains(writeAtomic(dir, path, []byte("x")), "writing the gate binary")
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	s.Require().Len(entries, 1)

	// A dir that can't be created in.
	s.Require().ErrorContains(writeAtomic("/dev/null/x", "/dev/null/x/loop", nil), "creating the gate binary dir")
}

func (s *GatebinSuite) TestWriteAtomicCreateTempFails() {
	// The dir exists but can't be written in, even by root on linux, where
	// procfs takes no new files.
	dir := "/proc/self"
	if _, err := os.Stat(dir); err != nil {
		dir = filepath.Join(s.dir, "ro")
		s.Require().NoError(os.Mkdir(dir, 0o555))
	}
	s.Require().ErrorContains(writeAtomic(dir, filepath.Join(dir, "loop"), nil), "writing the gate binary")
}

func (s *GatebinSuite) TestDefaultBaseURL() {
	s.Require().Equal(ReleaseURL, New(Config{}).cfg.BaseURL)
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
