package browsercookies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type StoreSuite struct {
	suite.Suite
}

func TestStoreSuite(t *testing.T) {
	suite.Run(t, new(StoreSuite))
}

// The live store is never opened, so the copy — sidecars included — has to
// carry everything the reader will need.
func (s *StoreSuite) TestOpenStoreCopy() {
	dir := s.T().TempDir()
	store := filepath.Join(dir, "Cookies")
	writeChromiumStore(s.T(), store, nil)
	s.Require().NoError(os.WriteFile(store+"-wal", []byte(""), 0o600))

	db, cleanup, err := openStoreCopy(store)
	s.Require().NoError(err)
	s.Require().NoError(db.Ping())

	cleanup()
	s.FileExists(store, "the original is left untouched")
}

func (s *StoreSuite) TestOpenStoreCopyMissingFile() {
	_, _, err := openStoreCopy(filepath.Join(s.T().TempDir(), "nope"))
	s.ErrorContains(err, "opening cookie store")
}

func (s *StoreSuite) TestOpenStoreCopyWithoutTempDir() {
	s.T().Setenv("TMPDIR", filepath.Join(s.T().TempDir(), "does", "not", "exist"))

	_, _, err := openStoreCopy(filepath.Join(s.T().TempDir(), "Cookies"))
	s.ErrorContains(err, "creating temp dir for cookie store")
}

func (s *StoreSuite) TestCopyFileErrors() {
	dir := s.T().TempDir()
	src := filepath.Join(dir, "src")
	s.Require().NoError(os.WriteFile(src, []byte("data"), 0o600))

	s.ErrorContains(copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "dst")),
		"opening cookie store")
	s.ErrorContains(copyFile(src, filepath.Join(dir, "no-such-dir", "dst")),
		"creating cookie store copy")
	s.ErrorContains(copyFile(dir, filepath.Join(dir, "dst")),
		"copying cookie store")
}
