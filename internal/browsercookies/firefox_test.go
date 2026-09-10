package browsercookies

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/suite"
)

type FirefoxSuite struct {
	suite.Suite
}

func TestFirefoxSuite(t *testing.T) {
	suite.Run(t, new(FirefoxSuite))
}

func (s *FirefoxSuite) TestSources() {
	root := s.T().TempDir()
	absolute := s.T().TempDir()

	writeFirefoxStore(s.T(), filepath.Join(root, "Profiles", "abc.default", "cookies.sqlite"), nil)
	writeFirefoxStore(s.T(), filepath.Join(absolute, "cookies.sqlite"), nil)
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "Profiles", "empty"), 0o755))

	s.Require().NoError(os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(strings.Join([]string{
		"[General]",
		"StartWithLastProfile=1",
		"[Profile0]",
		"Name=personal",
		"IsRelative=1",
		"Path=Profiles/abc.default",
		"[Profile1]",
		// No Name: the directory name has to stand in, or the picker shows
		// a blank row.
		"IsRelative=0",
		"Path=" + absolute,
		"[Profile2]",
		"Name=never-launched",
		"IsRelative=1",
		"Path=Profiles/empty",
		"[Profile3]",
		"Name=no-path-at-all",
		"[Install4F96D1932A9F858E]",
		"Default=Profiles/abc.default",
	}, "\n")), 0o600))

	sources := firefoxSources(root)
	s.Require().Len(sources, 2)
	s.Equal("personal", sources[0].Name)
	s.Equal(filepath.Join(root, "Profiles", "abc.default"), sources[0].Path)
	s.Equal(Source{Browser: BrowserFirefox, Name: filepath.Base(absolute), Path: absolute}, sources[1])
}

func (s *FirefoxSuite) TestSourcesWithoutProfilesINI() {
	s.Nil(firefoxSources(s.T().TempDir()))
}

// A profiles.ini whose lines are unreadable is reported as an error rather
// than as "this user has no Firefox profiles".
func (s *FirefoxSuite) TestParseProfilesINIScannerError() {
	path := filepath.Join(s.T().TempDir(), "profiles.ini")
	s.Require().NoError(os.WriteFile(path, []byte("[Profile0]\nName="+strings.Repeat("x", 128*1024)), 0o600))

	_, err := parseProfilesINI(path)
	s.ErrorContains(err, "reading Firefox profiles.ini")
}

func (s *FirefoxSuite) TestParseProfilesINISkipsJunk() {
	path := filepath.Join(s.T().TempDir(), "profiles.ini")
	s.Require().NoError(os.WriteFile(path, []byte(strings.Join([]string{
		"stray line before any section",
		"[Profile0]",
		"a line with no equals sign",
		"Unknown=key",
		"Path=p",
	}, "\n")), 0o600))

	got, err := parseProfilesINI(path)
	s.Require().NoError(err)
	s.Equal([]firefoxProfile{{path: "p"}}, got)
}

func (s *FirefoxSuite) TestSameSite() {
	s.Equal("None", firefoxSameSite(0))
	s.Equal("Lax", firefoxSameSite(1))
	s.Equal("Strict", firefoxSameSite(2))
	s.Empty(firefoxSameSite(9))
}

func (s *FirefoxSuite) TestNormaliseSameSite() {
	s.Empty(normaliseSameSite("None", false), "Chrome rejects None without Secure")
	s.Equal("None", normaliseSameSite("None", true))
	s.Equal("Lax", normaliseSameSite("Lax", false))
}

func (s *FirefoxSuite) TestReadCookies() {
	path := filepath.Join(s.T().TempDir(), "cookies.sqlite")
	writeFirefoxStore(s.T(), path, []firefoxRow{
		{host: ".example.org", name: "sid", value: "abc", path: "/", expiry: int64(1893456000),
			secure: 1, httpOnly: 1, sameSite: 2},
		{host: "plain.example", name: "n", value: "v", path: "/", expiry: int64(1893456000), sameSite: 0},
		{host: "session.example", name: "s", value: "v", path: "/", expiry: int64(0)},
	})

	cookies, err := readFirefoxCookies(path)
	s.Require().NoError(err)
	s.Require().Len(cookies, 2)
	s.Equal(Cookie{
		Domain: ".example.org", Name: "sid", Value: "abc", Path: "/",
		Expires: 1893456000, Secure: true, HTTPOnly: true, SameSite: "Strict",
	}, cookies[0])
	s.Empty(cookies[1].SameSite)
}

func (s *FirefoxSuite) TestReadCookiesErrors() {
	_, err := readFirefoxCookies(filepath.Join(s.T().TempDir(), "missing"))
	s.ErrorContains(err, "opening cookie store")

	empty := filepath.Join(s.T().TempDir(), "cookies.sqlite")
	execFixture(s.T(), empty, `CREATE TABLE other (x TEXT)`, func(*sql.DB) {})
	_, err = readFirefoxCookies(empty)
	s.ErrorContains(err, "querying Firefox cookies")

	bad := filepath.Join(s.T().TempDir(), "cookies.sqlite")
	writeFirefoxStore(s.T(), bad, []firefoxRow{{
		host: "example.org", name: "n", value: "v", path: "/", expiry: "not-a-number",
	}})
	_, err = readFirefoxCookies(bad)
	s.ErrorContains(err, "scanning Firefox cookie")
}

func (s *FirefoxSuite) TestScanCookiesRowError() {
	db, mock, err := sqlmock.New()
	s.Require().NoError(err)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"host", "name", "value", "path", "expiry", "isSecure", "isHttpOnly", "sameSite"}).
		AddRow("example.org", "n", "v", "/", 1893456000, 0, 0, 1).
		RowError(0, errors.New("disk went away"))
	mock.ExpectQuery("SELECT host, name").WillReturnRows(rows)

	_, err = scanFirefoxCookies(db)
	s.ErrorContains(err, "disk went away")
	s.NoError(mock.ExpectationsWereMet())
}
