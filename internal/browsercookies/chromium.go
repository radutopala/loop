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
	"strings"
)

// Chromium's macOS cookie encryption: AES-128-CBC with a key derived from a
// Keychain password, a fixed salt and a fixed all-spaces IV. These constants
// are Chromium's, not ours — see components/os_crypt/sync/os_crypt_mac.mm.
const (
	chromiumSalt       = "saltysalt"
	chromiumIterations = 1003
	chromiumKeyLength  = 16
	chromiumPrefixV10  = "v10"
)

// chromiumIV is the 16-space initialisation vector Chromium uses for every
// value; it is not a secret and not per-cookie.
var chromiumIV = bytes.Repeat([]byte{' '}, aes.BlockSize)

// keychainNames returns the Keychain service and account for a browser's
// "Safe Storage" password entry.
func keychainNames(browser string) (service, account string, ok bool) {
	switch browser {
	case BrowserChrome:
		return "Chrome Safe Storage", "Chrome", true
	case BrowserEdge:
		return "Microsoft Edge Safe Storage", "Microsoft Edge", true
	default:
		return "", "", false
	}
}

// chromiumKey derives the AES key for a Chromium-family browser.
//
// Reading the Keychain shells out to /usr/bin/security rather than linking
// Security.framework: loop is built with CGO_ENABLED=0, and the shell-out
// makes macOS show its own consent prompt, which is a system-level gate on
// this whole feature that loop cannot fake or bypass.
func (r *Reader) chromiumKey(browser string) ([]byte, error) {
	if r.GOOS != "darwin" {
		return nil, fmt.Errorf("importing %s cookies is only supported on macOS", browser)
	}
	service, account, ok := keychainNames(browser)
	if !ok {
		return nil, fmt.Errorf("unknown browser %q", browser)
	}

	out, err := r.runCmd("/usr/bin/security", "find-generic-password", "-w", "-s", service, "-a", account)
	if err != nil {
		return nil, fmt.Errorf("reading the %q Keychain entry: %w", service, err)
	}
	password := strings.TrimRight(string(out), "\r\n")
	if password == "" {
		return nil, fmt.Errorf("the %q Keychain entry is empty", service)
	}

	// pbkdf2.Key only rejects a non-positive iteration count or key length,
	// and both are Chromium's fixed constants above.
	key, _ := pbkdf2.Key(sha1.New, password, []byte(chromiumSalt), chromiumIterations, chromiumKeyLength)
	return key, nil
}

// decryptChromiumValue turns one encrypted_value blob back into a cookie
// value. host is the cookie's host_key, needed to recognise the domain hash
// newer Chrome versions prepend to the plaintext.
func decryptChromiumValue(encrypted []byte, key []byte, host string) (string, error) {
	if !bytes.HasPrefix(encrypted, []byte(chromiumPrefixV10)) {
		return "", fmt.Errorf("unsupported cookie encryption version")
	}
	body := encrypted[len(chromiumPrefixV10):]
	if len(body) == 0 || len(body)%aes.BlockSize != 0 {
		return "", fmt.Errorf("cookie ciphertext is not a whole number of blocks")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("building the cookie cipher: %w", err)
	}
	plain := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, chromiumIV).CryptBlocks(plain, body)

	plain, err = stripPKCS7(plain)
	if err != nil {
		return "", err
	}
	return string(stripDomainHash(plain, host)), nil
}

// stripDomainHash removes the 32-byte SHA-256 domain hash that Chrome began
// prepending to cookie plaintext.
//
// The version that introduced it is not worth encoding: hashing the host and
// comparing is self-validating, so one code path handles stores written
// before and after the change with no version sniffing at all.
func stripDomainHash(plain []byte, host string) []byte {
	if len(plain) < sha256.Size {
		return plain
	}
	want := sha256.Sum256([]byte(host))
	if !bytes.Equal(plain[:sha256.Size], want[:]) {
		return plain
	}
	return plain[sha256.Size:]
}

// stripPKCS7 removes CBC block padding.
func stripPKCS7(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("cookie plaintext is empty")
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(b) {
		return nil, fmt.Errorf("cookie plaintext has invalid padding")
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, fmt.Errorf("cookie plaintext has invalid padding")
		}
	}
	return b[:len(b)-pad], nil
}

// chromiumSameSite maps the cookies.samesite column to the CDP spelling.
func chromiumSameSite(v int) string {
	switch v {
	case 0:
		return "None"
	case 1:
		return "Lax"
	case 2:
		return "Strict"
	default:
		return ""
	}
}

// chromiumEpoch converts Chromium's microseconds-since-1601 timestamp to Unix
// seconds. The offset is the number of seconds between 1601-01-01 and the
// Unix epoch.
const chromiumEpochOffset = 11644473600

func chromiumExpiry(microseconds int64) int64 {
	return microseconds/1_000_000 - chromiumEpochOffset
}

// readChromiumCookies reads and decrypts every persistent cookie in a
// Chromium-family store.
//
// A row that will not decrypt is skipped, not fatal: a store built up over
// years holds values written under a key the user has since rotated, and one
// of those must not cost them the entire import.
func readChromiumCookies(store string, key []byte) ([]Cookie, error) {
	db, cleanup, err := openStoreCopy(store)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return scanChromiumCookies(db, key)
}

// scanChromiumCookies is split out from readChromiumCookies so the query and
// decrypt loop can be driven against a stub database.
func scanChromiumCookies(db *sql.DB, key []byte) ([]Cookie, error) {
	rows, err := db.Query(`SELECT host_key, name, value, encrypted_value, path, expires_utc,
		is_secure, is_httponly, samesite FROM cookies WHERE expires_utc > 0`)
	if err != nil {
		return nil, fmt.Errorf("querying Chromium cookies: %w", err)
	}
	defer rows.Close()

	var out []Cookie
	for rows.Next() {
		var (
			c                          Cookie
			plain                      string
			encrypted                  []byte
			expires                    int64
			secure, httpOnly, sameSite int
		)
		if err := rows.Scan(&c.Domain, &c.Name, &plain, &encrypted, &c.Path, &expires,
			&secure, &httpOnly, &sameSite); err != nil {
			return nil, fmt.Errorf("scanning Chromium cookie: %w", err)
		}

		value := plain
		if len(encrypted) > 0 {
			decrypted, err := decryptChromiumValue(encrypted, key, c.Domain)
			if err != nil {
				continue
			}
			value = decrypted
		}
		if value == "" {
			continue
		}

		c.Value = value
		c.Expires = chromiumExpiry(expires)
		c.Secure = secure != 0
		c.HTTPOnly = httpOnly != 0
		c.SameSite = normaliseSameSite(chromiumSameSite(sameSite), c.Secure)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading Chromium cookies: %w", err)
	}
	return out, nil
}
