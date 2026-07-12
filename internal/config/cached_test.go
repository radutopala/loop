package config

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CachedReloaderSuite struct {
	suite.Suite
}

func TestCachedReloaderSuite(t *testing.T) {
	suite.Run(t, new(CachedReloaderSuite))
}

// fakeFileInfo implements just enough of fs.FileInfo for the mtime+size gate.
type fakeFileInfo struct {
	os.FileInfo
	mtime time.Time
	size  int64
}

func (f fakeFileInfo) ModTime() time.Time { return f.mtime }
func (f fakeFileInfo) Size() int64        { return f.size }

// newCachedForTest wires a CachedReloader against an in-memory config file
// with injectable stat results and a parse counter.
func newCachedForTest(content *string, parses *int, statFn func(string) (os.FileInfo, error)) *CachedReloader {
	return &CachedReloader{
		loader: &Loader{
			userHomeDir: func() (string, error) { return "/home/test", nil },
			readFile: func(_ string) ([]byte, error) {
				*parses++
				return []byte(*content), nil
			},
		},
		stat: statFn,
	}
}

func (s *CachedReloaderSuite) TestReloadCachesUntilFileChanges() {
	content := `{"platforms":["local"]}`
	parses := 0
	fi := fakeFileInfo{mtime: time.Unix(100, 0), size: int64(len(content))}
	c := newCachedForTest(&content, &parses, func(path string) (os.FileInfo, error) {
		require.Equal(s.T(), "/home/test/.loop/config.json", path)
		return fi, nil
	})

	cfg1, err := c.Reload()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), cfg1)
	require.Equal(s.T(), 1, parses)

	// Unchanged mtime+size → served from cache, no reparse.
	cfg2, err := c.Reload()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, parses)

	// Each caller gets its own shallow copy: top-level mutation doesn't leak.
	cfg2.ClaudeModel = "mutated"
	cfg3, err := c.Reload()
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), "mutated", cfg3.ClaudeModel)
	require.Equal(s.T(), 1, parses)

	// mtime bump → reparse picks up the edit (hot-reload preserved).
	content = `{"platforms":["local"],"claude_model":"claude-opus-4-8"}`
	fi = fakeFileInfo{mtime: time.Unix(200, 0), size: int64(len(content))}
	cfg4, err := c.Reload()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude-opus-4-8", cfg4.ClaudeModel)
	require.Equal(s.T(), 2, parses)

	// Same mtime but different size (sub-second rewrite) → also reparses.
	content = `{"platforms":["local"],"claude_model":"claude-opus-4-6[1m]"}`
	fi = fakeFileInfo{mtime: time.Unix(200, 0), size: int64(len(content))}
	cfg5, err := c.Reload()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "claude-opus-4-6[1m]", cfg5.ClaudeModel)
	require.Equal(s.T(), 3, parses)
}

func (s *CachedReloaderSuite) TestReloadStatErrorFallsThrough() {
	content := `{"platforms":["local"]}`
	parses := 0
	c := newCachedForTest(&content, &parses, func(string) (os.FileInfo, error) {
		return nil, fs.ErrNotExist
	})

	// Stat failure → plain reload each time (uncached error-surface parity).
	_, err := c.Reload()
	require.NoError(s.T(), err)
	_, err = c.Reload()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 2, parses)
}

func (s *CachedReloaderSuite) TestReloadParseErrorNotCached() {
	content := `not json`
	parses := 0
	fi := fakeFileInfo{mtime: time.Unix(100, 0), size: 8}
	c := newCachedForTest(&content, &parses, func(string) (os.FileInfo, error) { return fi, nil })

	_, err := c.Reload()
	require.Error(s.T(), err)

	// A later fix (same mtime scenario is irrelevant here — the error was
	// never cached) parses again and succeeds.
	content = `{"platforms":["local"]}`
	cfg, err := c.Reload()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), cfg)
	require.Equal(s.T(), 2, parses)
}

func (s *CachedReloaderSuite) TestReloadHomeDirError() {
	c := &CachedReloader{
		loader: &Loader{
			userHomeDir: func() (string, error) { return "", errors.New("no home") },
			readFile:    func(string) ([]byte, error) { return nil, nil },
		},
		stat: os.Stat,
	}
	_, err := c.Reload()
	require.ErrorContains(s.T(), err, "no home")
}

func (s *CachedReloaderSuite) TestNewCachedReloaderRealFS() {
	c := NewCachedReloader()
	require.NotNil(s.T(), c.loader)
	require.NotNil(s.T(), c.stat)
}
