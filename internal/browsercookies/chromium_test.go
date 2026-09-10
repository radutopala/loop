package browsercookies

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/suite"
)

type ChromiumSuite struct {
	suite.Suite
}

func TestChromiumSuite(t *testing.T) {
	suite.Run(t, new(ChromiumSuite))
}

func (s *ChromiumSuite) TestKeychainNames() {
	service, account, ok := keychainNames(BrowserChrome)
	s.True(ok)
	s.Equal("Chrome Safe Storage", service)
	s.Equal("Chrome", account)

	service, account, ok = keychainNames(BrowserEdge)
	s.True(ok)
	s.Equal("Microsoft Edge Safe Storage", service)
	s.Equal("Microsoft Edge", account)

	_, _, ok = keychainNames(BrowserFirefox)
	s.False(ok)
}

func (s *ChromiumSuite) TestChromiumKey() {
	var gotArgs []string
	r := &Reader{GOOS: "darwin", runCmd: func(name string, args ...string) ([]byte, error) {
		gotArgs = append([]string{name}, args...)
		return []byte(testKeychainPassword + "\n"), nil
	}}

	key, err := r.chromiumKey(BrowserChrome)
	s.Require().NoError(err)
	s.Equal(deriveTestKey(s.T(), testKeychainPassword), key)
	s.Equal([]string{
		"/usr/bin/security", "find-generic-password", "-w",
		"-s", "Chrome Safe Storage", "-a", "Chrome",
	}, gotArgs)
}

func (s *ChromiumSuite) TestChromiumKeyErrors() {
	tests := []struct {
		name   string
		reader *Reader
		browse string
		want   string
	}{
		{
			name:   "linux has no keychain",
			reader: &Reader{GOOS: "linux"},
			browse: BrowserChrome,
			want:   "only supported on macOS",
		},
		{
			name:   "browser with no keychain entry",
			reader: &Reader{GOOS: "darwin"},
			browse: BrowserFirefox,
			want:   `unknown browser "firefox"`,
		},
		{
			name: "the user dismissed the prompt",
			reader: &Reader{GOOS: "darwin", runCmd: func(string, ...string) ([]byte, error) {
				return nil, errors.New("exit status 128")
			}},
			browse: BrowserChrome,
			want:   `reading the "Chrome Safe Storage" Keychain entry`,
		},
		{
			name: "empty entry",
			reader: &Reader{GOOS: "darwin", runCmd: func(string, ...string) ([]byte, error) {
				return []byte("\n"), nil
			}},
			browse: BrowserEdge,
			want:   `the "Microsoft Edge Safe Storage" Keychain entry is empty`,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := tt.reader.chromiumKey(tt.browse)
			s.ErrorContains(err, tt.want)
		})
	}
}

// The round trip is the point: encrypt here, decrypt with the production
// code, in both the plain and domain-hash-prefixed layouts Chrome writes.
func (s *ChromiumSuite) TestDecryptRoundTrip() {
	for _, withHash := range []bool{false, true} {
		s.Run(fmt.Sprintf("domainHash=%v", withHash), func() {
			blob := encryptChromiumValue(s.T(), testKey, ".example.com", "session-token", withHash)

			got, err := decryptChromiumValue(blob, testKey, ".example.com")
			s.Require().NoError(err)
			s.Equal("session-token", got)
		})
	}
}

// A value long enough to look like it carries a domain hash, but whose first
// 32 bytes are not one, must survive intact.
func (s *ChromiumSuite) TestDecryptKeepsHashLikePlaintext() {
	value := "0123456789012345678901234567890123456789"
	blob := encryptChromiumValue(s.T(), testKey, ".example.com", value, false)

	got, err := decryptChromiumValue(blob, testKey, ".example.com")
	s.Require().NoError(err)
	s.Equal(value, got)
}

// The hash is checked against the cookie's own host, so a value copied
// between hosts is not silently truncated by 32 bytes.
func (s *ChromiumSuite) TestStripDomainHash() {
	sum := sha256.Sum256([]byte("example.com"))
	plain := append(sum[:], []byte("value")...)

	s.Equal([]byte("value"), stripDomainHash(plain, "example.com"))
	s.Equal(plain, stripDomainHash(plain, "other.com"))
	s.Equal([]byte("short"), stripDomainHash([]byte("short"), "example.com"))
}

func (s *ChromiumSuite) TestDecryptErrors() {
	valid := encryptChromiumValue(s.T(), testKey, "example.com", "v", false)

	tests := []struct {
		name      string
		encrypted []byte
		key       []byte
		want      string
	}{
		{"no v10 prefix", []byte("plaintext"), testKey, "unsupported cookie encryption version"},
		{"empty body", []byte(chromiumPrefixV10), testKey, "whole number of blocks"},
		{"partial block", append([]byte(chromiumPrefixV10), make([]byte, 7)...), testKey, "whole number of blocks"},
		{"wrong key length", valid, []byte("short"), "building the cookie cipher"},
		{"wrong key", valid, []byte("fedcba9876543210"), "invalid padding"},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := decryptChromiumValue(tt.encrypted, tt.key, "example.com")
			s.ErrorContains(err, tt.want)
		})
	}
}

func (s *ChromiumSuite) TestStripPKCS7() {
	got, err := stripPKCS7([]byte("value\x03\x03\x03"))
	s.Require().NoError(err)
	s.Equal([]byte("value"), got)

	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, "cookie plaintext is empty"},
		{"zero pad", []byte("value\x00"), "invalid padding"},
		{"pad wider than a block", []byte("value\x20"), "invalid padding"},
		{"pad longer than the value", []byte("\x08"), "invalid padding"},
		{"inconsistent filler", []byte("value\x02\x03"), "invalid padding"},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, err := stripPKCS7(tt.in)
			s.ErrorContains(err, tt.want)
		})
	}
}

func (s *ChromiumSuite) TestSameSiteAndExpiry() {
	s.Equal("None", chromiumSameSite(0))
	s.Equal("Lax", chromiumSameSite(1))
	s.Equal("Strict", chromiumSameSite(2))
	s.Empty(chromiumSameSite(-1), "Chromium's unspecified")

	// 13400000000000000µs since 1601 is 2025-08-18T14:13:20Z.
	s.Equal(int64(1755526400), chromiumExpiry(13400000000000000))
	s.Equal(int64(-chromiumEpochOffset), chromiumExpiry(0))
}

func (s *ChromiumSuite) TestReadCookies() {
	key := testKey
	path := filepath.Join(s.T().TempDir(), "Cookies")
	writeChromiumStore(s.T(), path, []chromiumRow{
		{
			host: ".example.com", name: "sid", path: "/",
			encrypted: encryptChromiumValue(s.T(), key, ".example.com", "abc", true),
			expires:   int64(13400000000000000), secure: 1, httpOnly: 1, sameSite: 1,
		},
		{
			// An unencrypted row: older stores and some platforms keep the
			// value in the clear.
			host: "plain.example", name: "p", plain: "clear", path: "/x",
			expires: int64(13400000000000000), sameSite: 0,
		},
		{
			// SameSite=None on a non-secure cookie: Chrome would reject it,
			// so it lands unspecified rather than being lost.
			host: "nosecure.example", name: "n", plain: "v", path: "/",
			expires: int64(13400000000000000), sameSite: 0,
		},
		{
			// Written under a key the user has since rotated: dropped, not
			// fatal.
			host: ".example.com", name: "stale", encrypted: []byte("v10garbage"),
			path: "/", expires: int64(13400000000000000),
		},
		{
			// Nothing to import and nothing to say about it.
			host: ".example.com", name: "empty", path: "/",
			expires: int64(13400000000000000),
		},
		{
			// Session cookie: filtered out by the query.
			host: ".example.com", name: "session", plain: "x", path: "/", expires: int64(0),
		},
	})

	cookies, err := readChromiumCookies(path, key)
	s.Require().NoError(err)
	s.Require().Len(cookies, 3)

	s.Equal(Cookie{
		Domain: ".example.com", Name: "sid", Value: "abc", Path: "/",
		Expires: 1755526400, Secure: true, HTTPOnly: true, SameSite: "Lax",
	}, cookies[0])
	s.Equal("clear", cookies[1].Value)
	s.Empty(cookies[2].SameSite, "SameSite=None downgraded on a non-secure cookie")
}

func (s *ChromiumSuite) TestReadCookiesStoreErrors() {
	_, err := readChromiumCookies(filepath.Join(s.T().TempDir(), "missing"), testKey)
	s.ErrorContains(err, "opening cookie store")

	empty := filepath.Join(s.T().TempDir(), "Cookies")
	execFixture(s.T(), empty, `CREATE TABLE other (x TEXT)`, func(*sql.DB) {})
	_, err = readChromiumCookies(empty, testKey)
	s.ErrorContains(err, "querying Chromium cookies")
}

func (s *ChromiumSuite) TestReadCookiesScanError() {
	path := filepath.Join(s.T().TempDir(), "Cookies")
	writeChromiumStore(s.T(), path, []chromiumRow{{
		host: "example.com", name: "n", plain: "v", path: "/", expires: "not-a-number",
	}})

	_, err := readChromiumCookies(path, testKey)
	s.ErrorContains(err, "scanning Chromium cookie")
}

// A driver that fails mid-iteration must surface, not truncate the import
// into a silently short list.
func (s *ChromiumSuite) TestScanCookiesRowError() {
	db, mock, err := sqlmock.New()
	s.Require().NoError(err)
	defer db.Close()

	rows := sqlmock.NewRows([]string{
		"host_key", "name", "value", "encrypted_value", "path",
		"expires_utc", "is_secure", "is_httponly", "samesite",
	}).AddRow("example.com", "n", "v", nil, "/", 13400000000000000, 0, 0, 1).
		RowError(0, errors.New("disk went away"))
	mock.ExpectQuery("SELECT host_key").WillReturnRows(rows)

	_, err = scanChromiumCookies(db, testKey)
	s.ErrorContains(err, "disk went away")
	s.NoError(mock.ExpectationsWereMet())
}
