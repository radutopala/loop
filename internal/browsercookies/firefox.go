package browsercookies

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// firefoxSources reads profiles.ini under the Firefox root and returns every
// profile that has a cookies.sqlite. Firefox stores cookie values in the
// clear, so there is no key to fetch for this family.
func firefoxSources(root string) []Source {
	profiles, err := parseProfilesINI(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return nil
	}

	var out []Source
	for _, p := range profiles {
		dir := p.path
		if p.relative {
			dir = filepath.Join(root, p.path)
		}
		if _, err := os.Stat(filepath.Join(dir, "cookies.sqlite")); err != nil {
			continue
		}
		name := p.name
		if name == "" {
			name = filepath.Base(dir)
		}
		out = append(out, Source{Browser: BrowserFirefox, Name: name, Path: dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

type firefoxProfile struct {
	name     string
	path     string
	relative bool
}

// parseProfilesINI reads the [ProfileN] sections of a Firefox profiles.ini.
// Other sections ([Install...], [General]) are ignored, as are keys we do not
// use — the file gains new ones between Firefox releases.
func parseProfilesINI(path string) ([]firefoxProfile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading Firefox profiles.ini: %w", err)
	}
	defer f.Close()

	var (
		out     []firefoxProfile
		current *firefoxProfile
	)
	flush := func() {
		if current != nil && current.path != "" {
			out = append(out, *current)
		}
		current = nil
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			flush()
			if strings.HasPrefix(line, "[Profile") {
				current = &firefoxProfile{}
			}
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Name":
			current.name = strings.TrimSpace(value)
		case "Path":
			current.path = strings.TrimSpace(value)
		case "IsRelative":
			current.relative = strings.TrimSpace(value) == "1"
		}
	}
	flush()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Firefox profiles.ini: %w", err)
	}
	return out, nil
}

// firefoxSameSite maps moz_cookies.sameSite to the CDP spelling.
func firefoxSameSite(v int) string {
	switch v {
	case 1:
		return "Lax"
	case 2:
		return "Strict"
	case 0:
		return "None"
	default:
		return ""
	}
}

// readFirefoxCookies reads moz_cookies from a copy of the store. Values are
// plaintext, so this is a straight read with an epoch conversion: Firefox
// keeps expiry in whole seconds since the Unix epoch already.
func readFirefoxCookies(store string) ([]Cookie, error) {
	db, cleanup, err := openStoreCopy(store)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return scanFirefoxCookies(db)
}

// scanFirefoxCookies is split out from readFirefoxCookies so the query loop
// can be driven against a stub database.
func scanFirefoxCookies(db *sql.DB) ([]Cookie, error) {
	rows, err := db.Query(`SELECT host, name, value, path, expiry, isSecure, isHttpOnly, sameSite
		FROM moz_cookies WHERE expiry > 0`)
	if err != nil {
		return nil, fmt.Errorf("querying Firefox cookies: %w", err)
	}
	defer rows.Close()

	var out []Cookie
	for rows.Next() {
		var (
			c                          Cookie
			expiry                     int64
			secure, httpOnly, sameSite int
		)
		if err := rows.Scan(&c.Domain, &c.Name, &c.Value, &c.Path, &expiry, &secure, &httpOnly, &sameSite); err != nil {
			return nil, fmt.Errorf("scanning Firefox cookie: %w", err)
		}
		c.Expires = expiry
		c.Secure = secure != 0
		c.HTTPOnly = httpOnly != 0
		c.SameSite = normaliseSameSite(firefoxSameSite(sameSite), c.Secure)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading Firefox cookies: %w", err)
	}
	return out, nil
}

// normaliseSameSite drops SameSite=None from a non-secure cookie. Chrome
// rejects that combination outright, so carrying it over would lose the cookie
// entirely; unspecified is the closest thing that still lands.
func normaliseSameSite(sameSite string, secure bool) string {
	if sameSite == "None" && !secure {
		return ""
	}
	return sameSite
}
