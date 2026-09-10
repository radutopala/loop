// Package browsercookies reads cookies out of the browsers installed on the
// machine running loop — Chrome, Edge and Firefox — so they can be handed to a
// channel's Chrome sidecar and the agent lands on pages already signed in.
//
// The package is deliberately a leaf: it imports nothing from loop, opens no
// network connections and speaks no CDP. Everything it does is read-only
// against the user's profile directories, and it never writes to a live
// browser store.
package browsercookies

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Browser identifiers. These are the three the picker offers; Safari is
// excluded because its store is a proprietary binary format behind TCC.
const (
	BrowserChrome  = "chrome"
	BrowserEdge    = "edge"
	BrowserFirefox = "firefox"
)

// Cookie is one decrypted cookie, normalised across browser families.
type Cookie struct {
	Domain   string
	Name     string
	Value    string
	Path     string
	Expires  int64 // Unix seconds; always > 0, session cookies are dropped
	Secure   bool
	HTTPOnly bool
	SameSite string // "Strict", "Lax", "None", or "" for unspecified
}

// Source is one browser profile that holds a cookie store.
type Source struct {
	Browser string `json:"browser"`
	Name    string `json:"name"` // display name, e.g. "Default" or "Work"
	Path    string `json:"path"` // profile directory
}

// ID is the stable identifier used to name a source in the API and config,
// e.g. "chrome:Default".
func (s Source) ID() string { return s.Browser + ":" + s.Name }

// family reports whether the source uses the Chromium cookie store layout
// (encrypted values, "Cookies" SQLite file) or the Firefox one.
func (s Source) isChromium() bool { return s.Browser != BrowserFirefox }

// Reader discovers browser profiles and reads their cookie stores.
//
// runCmd is the only injected seam: it runs the macOS `security` binary to
// fetch a browser's Keychain password. Everything else is plain filesystem
// access, exercised in tests against real fixture directories.
type Reader struct {
	Home string
	GOOS string

	runCmd func(name string, args ...string) ([]byte, error)
}

// NewReader returns a Reader bound to the current user and platform.
func NewReader() *Reader {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return &Reader{
		Home:   home,
		GOOS:   runtime.GOOS,
		runCmd: runCmd,
	}
}

func runCmd(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// browserRoots returns the per-OS profile roots for each supported browser.
// Mirrors the switch in browser.chromeUserDataDirForOS; keeping the shape
// identical is what makes adding a platform a table edit.
func browserRoots(home, goos string) map[string]string {
	if home == "" {
		return nil
	}
	switch goos {
	case "darwin":
		support := filepath.Join(home, "Library", "Application Support")
		return map[string]string{
			BrowserChrome:  filepath.Join(support, "Google", "Chrome"),
			BrowserEdge:    filepath.Join(support, "Microsoft Edge"),
			BrowserFirefox: filepath.Join(support, "Firefox"),
		}
	case "linux":
		return map[string]string{
			BrowserChrome:  filepath.Join(home, ".config", "google-chrome"),
			BrowserEdge:    filepath.Join(home, ".config", "microsoft-edge"),
			BrowserFirefox: filepath.Join(home, ".mozilla", "firefox"),
		}
	case "windows":
		local := filepath.Join(home, "AppData", "Local")
		return map[string]string{
			BrowserChrome:  filepath.Join(local, "Google", "Chrome", "User Data"),
			BrowserEdge:    filepath.Join(local, "Microsoft", "Edge", "User Data"),
			BrowserFirefox: filepath.Join(home, "AppData", "Roaming", "Mozilla", "Firefox"),
		}
	default:
		return nil
	}
}

// Sources lists every browser profile on this machine that has a cookie store.
// A machine with no supported browser — the daemon running inside a container,
// for instance — yields an empty slice rather than an error: "nothing to
// import" is a normal state, not a failure.
func (r *Reader) Sources() []Source {
	var out []Source
	roots := browserRoots(r.Home, r.GOOS)
	for _, browser := range []string{BrowserChrome, BrowserEdge, BrowserFirefox} {
		root, ok := roots[browser]
		if !ok {
			continue
		}
		if browser == BrowserFirefox {
			out = append(out, firefoxSources(root)...)
			continue
		}
		out = append(out, chromiumSources(browser, root)...)
	}
	return out
}

// chromiumSources finds the "Default" and "Profile N" directories under a
// Chromium-family root that actually contain a Cookies store.
func chromiumSources(browser, root string) []Source {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := chromiumProfileNames(root)

	var out []Source
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		if dir != "Default" && !strings.HasPrefix(dir, "Profile ") {
			continue
		}
		store := filepath.Join(root, dir, "Cookies")
		if _, err := os.Stat(store); err != nil {
			continue
		}
		name := dir
		if display, ok := names[dir]; ok && display != "" {
			name = display
		}
		out = append(out, Source{Browser: browser, Name: name, Path: filepath.Join(root, dir)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// chromiumProfileNames maps profile directory -> the name the user gave it,
// read from the root's Local State file. A missing or malformed Local State
// simply means the directory names are used as-is.
func chromiumProfileNames(root string) map[string]string {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	names := make(map[string]string, len(state.Profile.InfoCache))
	for dir, info := range state.Profile.InfoCache {
		names[dir] = info.Name
	}
	return names
}

// Cookies reads and decrypts every persistent cookie in a source's store.
//
// Rows that cannot be decrypted are dropped rather than returned as an error:
// a store accumulated over years will contain values written by an older key,
// and one of those must not sink the whole import.
func (r *Reader) Cookies(src Source) ([]Cookie, error) {
	if src.isChromium() {
		key, err := r.chromiumKey(src.Browser)
		if err != nil {
			return nil, err
		}
		return readChromiumCookies(filepath.Join(src.Path, "Cookies"), key)
	}
	return readFirefoxCookies(filepath.Join(src.Path, "cookies.sqlite"))
}

// FindSource returns the source with the given ID, e.g. "chrome:Default".
func (r *Reader) FindSource(id string) (Source, error) {
	for _, s := range r.Sources() {
		if s.ID() == id {
			return s, nil
		}
	}
	return Source{}, fmt.Errorf("no browser profile %q on this machine", id)
}
