package browsercookies

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// testKey is a 16-byte AES key standing in for the one PBKDF2 would derive
// from a Keychain password.
var testKey = []byte("0123456789abcdef")

// testKeychainPassword is what the fake `security` binary hands back.
const testKeychainPassword = "keychain-password"

// deriveTestKey runs the same derivation the reader does, so a fixture
// encrypted here is one the production path can actually open.
func deriveTestKey(t *testing.T, password string) []byte {
	t.Helper()
	key, err := pbkdf2.Key(sha1.New, password, []byte(chromiumSalt), chromiumIterations, chromiumKeyLength)
	require.NoError(t, err)
	return key
}

type chromiumRow struct {
	host      string
	name      string
	plain     string
	encrypted []byte
	path      string
	expires   any
	secure    int
	httpOnly  int
	sameSite  int
}

// writeChromiumStore builds a fixture with the columns Chrome's schema
// exposes to us. expires_utc is typed loosely so a test can put a value in
// that will not scan.
func writeChromiumStore(t *testing.T, path string, rows []chromiumRow) {
	t.Helper()
	execFixture(t, path, `CREATE TABLE cookies (
		host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, path TEXT,
		expires_utc, is_secure INTEGER, is_httponly INTEGER, samesite INTEGER)`,
		func(db *sql.DB) {
			for _, r := range rows {
				_, err := db.Exec(`INSERT INTO cookies VALUES (?,?,?,?,?,?,?,?,?)`,
					r.host, r.name, r.plain, r.encrypted, r.path, r.expires, r.secure, r.httpOnly, r.sameSite)
				require.NoError(t, err)
			}
		})
}

type firefoxRow struct {
	host     string
	name     string
	value    string
	path     string
	expiry   any
	secure   int
	httpOnly int
	sameSite int
}

func writeFirefoxStore(t *testing.T, path string, rows []firefoxRow) {
	t.Helper()
	execFixture(t, path, `CREATE TABLE moz_cookies (
		host TEXT, name TEXT, value TEXT, path TEXT, expiry,
		isSecure INTEGER, isHttpOnly INTEGER, sameSite INTEGER)`,
		func(db *sql.DB) {
			for _, r := range rows {
				_, err := db.Exec(`INSERT INTO moz_cookies VALUES (?,?,?,?,?,?,?,?)`,
					r.host, r.name, r.value, r.path, r.expiry, r.secure, r.httpOnly, r.sameSite)
				require.NoError(t, err)
			}
		})
}

func execFixture(t *testing.T, path, ddl string, insert func(*sql.DB)) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(ddl)
	require.NoError(t, err)
	insert(db)
}

// encryptChromiumValue is the inverse of decryptChromiumValue, so the
// decryptor gets a known-answer test rather than a fixture nobody can
// regenerate.
func encryptChromiumValue(t *testing.T, key []byte, host, value string, withDomainHash bool) []byte {
	t.Helper()
	plain := []byte(value)
	if withDomainHash {
		sum := sha256.Sum256([]byte(host))
		plain = append(sum[:], plain...)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, chromiumIV).CryptBlocks(out, plain)
	return append([]byte(chromiumPrefixV10), out...)
}

type ReaderSuite struct {
	suite.Suite

	home   string
	reader *Reader
}

func TestReaderSuite(t *testing.T) {
	suite.Run(t, new(ReaderSuite))
}

// SetupTest lays out a macOS-shaped home with two Chrome profiles, one Edge
// profile and one Firefox profile, plus the near-misses a real profile root
// contains: a directory with no store, a file named like a profile, and a
// directory that is not a profile at all.
func (s *ReaderSuite) SetupTest() {
	s.home = s.T().TempDir()
	support := filepath.Join(s.home, "Library", "Application Support")
	chrome := filepath.Join(support, "Google", "Chrome")

	writeChromiumStore(s.T(), filepath.Join(chrome, "Default", "Cookies"), []chromiumRow{{
		host:      ".example.com",
		name:      "session",
		encrypted: encryptChromiumValue(s.T(), deriveTestKey(s.T(), testKeychainPassword), ".example.com", "abc123", false),
		path:      "/",
		expires:   int64(13400000000000000),
		secure:    1,
		sameSite:  2,
	}})
	writeChromiumStore(s.T(), filepath.Join(chrome, "Profile 1", "Cookies"), nil)
	s.Require().NoError(os.MkdirAll(filepath.Join(chrome, "Profile 7"), 0o755))
	s.Require().NoError(os.MkdirAll(filepath.Join(chrome, "Extensions"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(chrome, "Profile 8"), []byte("not a dir"), 0o600))
	s.Require().NoError(os.WriteFile(filepath.Join(chrome, "Local State"),
		[]byte(`{"profile":{"info_cache":{"Profile 1":{"name":"Work"},"Default":{"name":""}}}}`), 0o600))

	writeChromiumStore(s.T(), filepath.Join(support, "Microsoft Edge", "Default", "Cookies"), nil)

	firefox := filepath.Join(support, "Firefox")
	writeFirefoxStore(s.T(), filepath.Join(firefox, "Profiles", "abc.default", "cookies.sqlite"), []firefoxRow{{
		host: "example.org", name: "sid", value: "v", path: "/", expiry: int64(1893456000),
	}})
	s.Require().NoError(os.WriteFile(filepath.Join(firefox, "profiles.ini"), []byte(
		"[Install123]\nDefault=Profiles/abc.default\n\n[Profile0]\nName=default\nIsRelative=1\nPath=Profiles/abc.default\n"), 0o600))

	s.reader = &Reader{
		Home: s.home,
		GOOS: "darwin",
		runCmd: func(string, ...string) ([]byte, error) {
			return []byte(testKeychainPassword + "\n"), nil
		},
	}
}

func (s *ReaderSuite) TestSources() {
	sources := s.reader.Sources()

	ids := make([]string, 0, len(sources))
	for _, src := range sources {
		ids = append(ids, src.ID())
	}
	s.Equal([]string{"chrome:Default", "chrome:Work", "edge:Default", "firefox:default"}, ids)
}

// A profile directory with no Cookies file, a non-directory named like a
// profile and a directory that is not a profile must all be ignored.
func (s *ReaderSuite) TestSourcesSkipsNonProfiles() {
	for _, src := range s.reader.Sources() {
		s.NotContains(src.Path, "Profile 7")
		s.NotContains(src.Path, "Profile 8")
		s.NotContains(src.Path, "Extensions")
	}
}

// The daemon inside a container sees no browser roots at all. That is a
// normal state — an empty list, never an error.
func (s *ReaderSuite) TestSourcesWithoutBrowsers() {
	s.Empty((&Reader{Home: s.T().TempDir(), GOOS: "darwin"}).Sources())
	s.Empty((&Reader{Home: "", GOOS: "darwin"}).Sources())
	s.Empty((&Reader{Home: s.home, GOOS: "plan9"}).Sources())
}

func (s *ReaderSuite) TestBrowserRoots() {
	tests := []struct {
		goos string
		want map[string]string
	}{
		{"darwin", map[string]string{
			BrowserChrome:  "/h/Library/Application Support/Google/Chrome",
			BrowserEdge:    "/h/Library/Application Support/Microsoft Edge",
			BrowserFirefox: "/h/Library/Application Support/Firefox",
		}},
		{"linux", map[string]string{
			BrowserChrome:  "/h/.config/google-chrome",
			BrowserEdge:    "/h/.config/microsoft-edge",
			BrowserFirefox: "/h/.mozilla/firefox",
		}},
		{"windows", map[string]string{
			BrowserChrome:  "/h/AppData/Local/Google/Chrome/User Data",
			BrowserEdge:    "/h/AppData/Local/Microsoft/Edge/User Data",
			BrowserFirefox: "/h/AppData/Roaming/Mozilla/Firefox",
		}},
		{"plan9", nil},
	}

	for _, tt := range tests {
		s.Run(tt.goos, func() {
			s.Equal(tt.want, browserRoots("/h", tt.goos))
		})
	}
	s.Nil(browserRoots("", "darwin"))
}

// A user-visible profile name replaces the directory name, but only when
// Local State actually has one: an empty name would leave a blank row.
func (s *ReaderSuite) TestProfileNames() {
	root := s.T().TempDir()
	s.Nil(chromiumProfileNames(root), "missing Local State")

	s.Require().NoError(os.WriteFile(filepath.Join(root, "Local State"), []byte("{not json"), 0o600))
	s.Nil(chromiumProfileNames(root), "malformed Local State")

	s.Require().NoError(os.WriteFile(filepath.Join(root, "Local State"),
		[]byte(`{"profile":{"info_cache":{"Default":{"name":"Personal"}}}}`), 0o600))
	s.Equal(map[string]string{"Default": "Personal"}, chromiumProfileNames(root))
}

func (s *ReaderSuite) TestFindSource() {
	src, err := s.reader.FindSource("chrome:Work")
	s.Require().NoError(err)
	s.Equal(BrowserChrome, src.Browser)
	s.Equal("Work", src.Name)

	_, err = s.reader.FindSource("chrome:Nope")
	s.ErrorContains(err, `no browser profile "chrome:Nope"`)
}

func (s *ReaderSuite) TestCookiesFromChromium() {
	src, err := s.reader.FindSource("chrome:Default")
	s.Require().NoError(err)

	cookies, err := s.reader.Cookies(src)
	s.Require().NoError(err)
	s.Require().Len(cookies, 1)
	s.Equal("abc123", cookies[0].Value)
	s.Equal("Strict", cookies[0].SameSite)
}

// A Keychain that will not answer fails the source, and the message must not
// pretend the import worked.
func (s *ReaderSuite) TestCookiesFromChromiumKeyError() {
	s.reader.runCmd = func(string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("user denied access")
	}
	src, err := s.reader.FindSource("chrome:Default")
	s.Require().NoError(err)

	_, err = s.reader.Cookies(src)
	s.ErrorContains(err, "user denied access")
}

func (s *ReaderSuite) TestCookiesFromFirefox() {
	src, err := s.reader.FindSource("firefox:default")
	s.Require().NoError(err)

	cookies, err := s.reader.Cookies(src)
	s.Require().NoError(err)
	s.Require().Len(cookies, 1)
	s.Equal("v", cookies[0].Value)
}

func (s *ReaderSuite) TestSourceHelpers() {
	s.Equal("chrome:Default", Source{Browser: BrowserChrome, Name: "Default"}.ID())
	s.True(Source{Browser: BrowserEdge}.isChromium())
	s.False(Source{Browser: BrowserFirefox}.isChromium())
}

func (s *ReaderSuite) TestNewReader() {
	s.T().Setenv("HOME", s.home)
	r := NewReader()
	s.Equal(s.home, r.Home)
	s.NotEmpty(r.GOOS)
	s.NotNil(r.runCmd)

	// No home directory is a degraded machine, not a crash: Sources simply
	// finds nothing.
	s.T().Setenv("HOME", "")
	s.Empty(NewReader().Home)
}

func (s *ReaderSuite) TestRunCmd() {
	out, err := runCmd("/bin/echo", "hello")
	s.Require().NoError(err)
	s.Equal("hello\n", string(out))
}
