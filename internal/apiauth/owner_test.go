package apiauth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type OwnerSuite struct {
	suite.Suite
}

func TestOwnerSuite(t *testing.T) {
	suite.Run(t, new(OwnerSuite))
}

func (s *OwnerSuite) TestOwnerTokenPath() {
	p, err := OwnerTokenPath(func() (string, error) { return "/cfg", nil })
	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join("/cfg", "loop", "api-token"), p)

	_, err = OwnerTokenPath(func() (string, error) { return "", errors.New("no home") })
	require.ErrorContains(s.T(), err, "no home")
}

func (s *OwnerSuite) TestLoadOrCreateThenReuse() {
	path := filepath.Join(s.T().TempDir(), "cfg", "loop", "api-token")
	f := NewTokenFile(path)

	tok, err := f.LoadOrCreate()
	require.NoError(s.T(), err)
	require.Len(s.T(), tok, 2*tokenBytes)

	info, err := os.Stat(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), os.FileMode(0o600), info.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(s.T(), err)
	require.Equal(s.T(), os.FileMode(0o700), dir.Mode().Perm())

	again, err := f.LoadOrCreate()
	require.NoError(s.T(), err)
	require.Equal(s.T(), tok, again)

	rotated, err := f.Rotate()
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), tok, rotated)
	loaded, err := f.Load()
	require.NoError(s.T(), err)
	require.Equal(s.T(), rotated, loaded)
}

func (s *OwnerSuite) TestRotateTightensLooseDir() {
	dir := filepath.Join(s.T().TempDir(), "loop")
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))
	_, err := NewTokenFile(filepath.Join(dir, "api-token")).Rotate()
	require.NoError(s.T(), err)
	info, err := os.Stat(dir)
	require.NoError(s.T(), err)
	require.Equal(s.T(), os.FileMode(0o700), info.Mode().Perm())
}

func (s *OwnerSuite) TestLoadErrors() {
	dir := s.T().TempDir()
	empty := filepath.Join(dir, "empty")
	require.NoError(s.T(), os.WriteFile(empty, []byte("\n"), 0o600))
	_, err := NewTokenFile(empty).Load()
	require.ErrorContains(s.T(), err, "is empty")

	_, err = NewTokenFile(empty).LoadOrCreate()
	require.ErrorContains(s.T(), err, "is empty", "an unreadable token is not replaced")
}

func (s *OwnerSuite) TestRotateErrors() {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		breakIt func(f *TokenFile)
		want    string
	}{
		{"rand", func(f *TokenFile) { f.readRand = func([]byte) (int, error) { return 0, boom } }, "generating a token"},
		{"mkdir", func(f *TokenFile) { f.mkdirAll = func(string, os.FileMode) error { return boom } }, "creating"},
		{"chmod", func(f *TokenFile) { f.chmod = func(string, os.FileMode) error { return boom } }, "securing"},
		{"write", func(f *TokenFile) { f.writeFile = func(string, []byte, os.FileMode) error { return boom } }, "writing"},
		{"rename", func(f *TokenFile) { f.rename = func(string, string) error { return boom } }, "replacing"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			f := NewTokenFile(filepath.Join(s.T().TempDir(), "loop", "api-token"))
			tc.breakIt(f)
			_, err := f.Rotate()
			require.ErrorIs(s.T(), err, boom)
			require.ErrorContains(s.T(), err, tc.want)
		})
	}
}
