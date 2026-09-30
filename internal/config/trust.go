// trust.go holds project config trust: the fields of a project's
// .loop/config.json that reach past the container take effect only once the
// owner has approved them, like direnv's `direnv allow`.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/unidiff"
	"github.com/tailscale/hujson"
)

// ErrTrustChanged is returned when the project config changed after the
// owner looked at it: what they approved is no longer what's on disk.
var ErrTrustChanged = errors.New("the project config changed; review it again")

// trustedFields are the project config fields that reach past the
// container, or run on the host: an agent can write the project config, so
// these wait for the owner. Everything else in the project config only
// shapes the agent's own container and prompts.
type trustedFields struct {
	Mounts        []string               `json:"mounts,omitempty"`
	InheritMounts *bool                  `json:"inherit_mounts,omitempty"`
	ExtraDirs     []string               `json:"extra_dirs,omitempty"`
	CopyFiles     []string               `json:"copy_files,omitempty"`
	Envs          map[string]any         `json:"envs,omitempty"`
	Permissions   *jsonPermissionsConfig `json:"permissions,omitempty"`
	Gates         *jsonGatesConfig       `json:"gates,omitempty"`
	BashShortcuts []BashShortcut         `json:"bash_shortcuts,omitempty"`
	Browser       *trustedBrowser        `json:"browser,omitempty"`
	Memory        *trustedMemory         `json:"memory,omitempty"`
}

// trustedBrowser is the part of the browser block that reaches the host:
// driving the user's own Chrome, loading extensions from host paths, and
// importing the host browser's cookies.
type trustedBrowser struct {
	Mode         string                  `json:"mode,omitempty"`
	HostCDPPort  *int                    `json:"host_cdp_port,omitempty"`
	Extensions   []string                `json:"extensions,omitempty"`
	CookieImport *jsonCookieImportConfig `json:"cookie_import,omitempty"`
}

// trustedMemory is the part of the memory block that reaches the host: the
// daemon indexes these paths, and agents can search what it indexed.
type trustedMemory struct {
	Paths []string `json:"paths,omitempty"`
}

// trustedFieldsOf returns the fields of pc that need trust.
func trustedFieldsOf(pc *projectConfig) trustedFields {
	t := trustedFields{
		Mounts:        pc.Mounts,
		InheritMounts: pc.InheritMounts,
		ExtraDirs:     pc.ExtraDirs,
		CopyFiles:     pc.CopyFiles,
		Envs:          pc.Envs,
		Permissions:   pc.Permissions,
		Gates:         pc.Gates,
		BashShortcuts: pc.BashShortcuts,
	}
	if b := pc.Browser; b != nil && (b.Mode != "" || b.HostCDPPort != nil || b.Extensions != nil || b.CookieImport != nil) {
		t.Browser = &trustedBrowser{Mode: b.Mode, HostCDPPort: b.HostCDPPort, Extensions: b.Extensions, CookieImport: b.CookieImport}
	}
	if m := pc.Memory; m != nil && len(m.Paths) > 0 {
		t.Memory = &trustedMemory{Paths: m.Paths}
	}
	return t
}

// apply replaces pc's trusted fields with t.
func (t trustedFields) apply(pc *projectConfig) {
	pc.Mounts = t.Mounts
	pc.InheritMounts = t.InheritMounts
	pc.ExtraDirs = t.ExtraDirs
	pc.CopyFiles = t.CopyFiles
	pc.Envs = t.Envs
	pc.Permissions = t.Permissions
	pc.Gates = t.Gates
	pc.BashShortcuts = t.BashShortcuts
	if pc.Browser != nil || t.Browser != nil {
		if pc.Browser == nil {
			pc.Browser = &jsonBrowserConfig{}
		}
		b := t.Browser
		if b == nil {
			b = &trustedBrowser{}
		}
		pc.Browser.Mode, pc.Browser.HostCDPPort, pc.Browser.Extensions, pc.Browser.CookieImport = b.Mode, b.HostCDPPort, b.Extensions, b.CookieImport
	}
	if pc.Memory != nil || t.Memory != nil {
		if pc.Memory == nil {
			pc.Memory = &jsonMemoryConfig{}
		}
		pc.Memory.Paths = nil
		if t.Memory != nil {
			pc.Memory.Paths = t.Memory.Paths
		}
	}
}

// canonical returns t as canonical JSON: fixed field order, sorted map
// keys, no whitespace. Two configs that mean the same thing compare equal
// however they're formatted.
func (t trustedFields) canonical() []byte {
	b, _ := json.Marshal(t) // plain data decoded from JSON always encodes
	return b
}

// TrustStatus is where a project config stands.
type TrustStatus struct {
	// Trusted is whether the fields that need trust take effect as written.
	Trusted bool `json:"trusted"`
	// Current is those fields as the file has them now, as indented JSON.
	Current string `json:"current"`
	// Approved is the version the owner last trusted, as indented JSON, or
	// "" when they never did. It's what applies while Trusted is false.
	Approved string `json:"approved"`
	// Diff is the change from Approved to Current as a unified diff, from
	// /dev/null when nothing was ever trusted, or "" while Trusted.
	Diff string `json:"diff"`
	// Hash identifies Current; trusting it takes this back, so what the
	// owner looked at is what gets trusted.
	Hash string `json:"hash"`
}

// trustEntry is one project's approved fields in the trust file.
type trustEntry struct {
	Fields    json.RawMessage `json:"fields"`
	TrustedAt time.Time       `json:"trusted_at"`
}

// trustMu serializes this process's read-modify-write of the trust file.
var trustMu sync.Mutex

// TrustStore records which project configs the owner trusts. It lives next
// to the owner API token, in a dir that's never mounted into a container and
// never a channel dir, so an agent can neither read nor forge it.
type TrustStore struct {
	userConfigDir func() (string, error)
	readFile      func(string) ([]byte, error)
	writeFile     func(string, []byte, os.FileMode) error
	mkdirAll      func(string, os.FileMode) error
	rename        func(string, string) error
	now           func() time.Time
}

// NewTrustStore returns the TrustStore of this machine's user.
func NewTrustStore() *TrustStore {
	return NewTrustStoreIn(os.UserConfigDir)
}

// NewTrustStoreIn returns a TrustStore that keeps its file under the dir
// userConfigDir returns.
func NewTrustStoreIn(userConfigDir func() (string, error)) *TrustStore {
	return &TrustStore{
		userConfigDir: userConfigDir,
		readFile:      os.ReadFile,
		writeFile:     os.WriteFile,
		mkdirAll:      os.MkdirAll,
		rename:        os.Rename,
		now:           time.Now,
	}
}

func (s *TrustStore) path() (string, error) {
	dir, err := s.userConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating the config dir: %w", err)
	}
	return filepath.Join(dir, "loop", "project-trust.json"), nil
}

func (s *TrustStore) readAll() (map[string]trustEntry, error) {
	path, err := s.path()
	if err != nil {
		return nil, err
	}
	data, err := s.readFile(path)
	if os.IsNotExist(err) {
		return map[string]trustEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	all := map[string]trustEntry{}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return all, nil
}

func (s *TrustStore) writeAll(all map[string]trustEntry) error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := s.mkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	data, _ := json.MarshalIndent(all, "", "  ") // a map of raw JSON and times always encodes
	tmp := path + ".tmp"
	if err := s.writeFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := s.rename(tmp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// approved returns the fields the owner last trusted for dir.
func (s *TrustStore) approved(dir string) (trustedFields, bool, error) {
	all, err := s.readAll()
	if err != nil {
		return trustedFields{}, false, err
	}
	e, ok := all[filepath.Clean(dir)]
	if !ok {
		return trustedFields{}, false, nil
	}
	var t trustedFields
	if err := json.Unmarshal(e.Fields, &t); err != nil {
		return trustedFields{}, false, fmt.Errorf("parsing trusted config of %s: %w", dir, err)
	}
	return t, true, nil
}

// resolve returns the fields of pc that apply for the project in dir: pc's
// own when they're trusted, else the last trusted ones, else none. A project
// with nothing that needs trust is trusted as is. An unreadable trust file
// trusts nothing.
func (s *TrustStore) resolve(dir string, pc *projectConfig) trustedFields {
	cur := trustedFieldsOf(pc)
	canon := cur.canonical()
	if string(canon) == "{}" {
		return cur
	}
	approved, ok, err := s.approved(dir)
	if err != nil || !ok {
		return trustedFields{}
	}
	if bytes.Equal(approved.canonical(), canon) {
		return cur
	}
	return approved
}

// readProject returns the trusted fields of the project config in dir, as
// the file has them now. A missing file has none.
func (s *TrustStore) readProject(dir string) (trustedFields, error) {
	data, err := s.readFile(filepath.Join(dir, ".loop", "config.json"))
	if os.IsNotExist(err) {
		return trustedFields{}, nil
	}
	if err != nil {
		return trustedFields{}, fmt.Errorf("reading project config file: %w", err)
	}
	return fieldsOfData(data)
}

// MemoryPaths returns the project's memory paths that apply: the trusted
// ones. An unreadable project config has none.
func (s *TrustStore) MemoryPaths(dir string) []string {
	data, err := s.readFile(filepath.Join(dir, ".loop", "config.json"))
	if err != nil {
		return nil
	}
	pc, err := parseProjectConfig(data)
	if err != nil {
		return nil
	}
	if m := s.resolve(dir, pc).Memory; m != nil {
		return m.Paths
	}
	return nil
}

// Status reports whether the project config in dir is trusted.
func (s *TrustStore) Status(dir string) (TrustStatus, error) {
	cur, err := s.readProject(dir)
	if err != nil {
		return TrustStatus{}, err
	}
	approved, ok, err := s.approved(dir)
	if err != nil {
		return TrustStatus{}, err
	}
	canon := cur.canonical()
	st := TrustStatus{
		Current: indentJSON(canon),
		Hash:    hashFields(canon),
		Trusted: string(canon) == "{}" || (ok && bytes.Equal(approved.canonical(), canon)),
	}
	if ok {
		st.Approved = indentJSON(approved.canonical())
	}
	if !st.Trusted {
		st.Diff = trustDiff(st.Approved, st.Current, ok)
	}
	return st, nil
}

// trustDiff renders what changed since the owner last trusted the project
// config as a unified diff, from /dev/null when they never did.
func trustDiff(approved, current string, ok bool) string {
	from := ".loop/config.json (last trusted)"
	if !ok {
		from, approved = "/dev/null", ""
	}
	return unidiff.Diff(from, ".loop/config.json (now)", approved, current)
}

// Trust records the project config in dir as trusted. hash is the Hash of
// the Status the owner reviewed; when the file changed since, Trust returns
// ErrTrustChanged and trusts nothing. An empty hash trusts what's there.
func (s *TrustStore) Trust(dir, hash string) error {
	trustMu.Lock()
	defer trustMu.Unlock()
	cur, err := s.readProject(dir)
	if err != nil {
		return err
	}
	canon := cur.canonical()
	if hash != "" && hash != hashFields(canon) {
		return ErrTrustChanged
	}
	return s.record(dir, canon)
}

// Adopt trusts the project config in dir as it is when dir has no trusted
// version yet: the one-time upgrade to trust, for projects set up before
// Loop asked. A dir already trusted keeps what the owner approved, and a
// config Adopt can't read is left for the owner to review.
func (s *TrustStore) Adopt(dir string) error {
	trustMu.Lock()
	defer trustMu.Unlock()
	cur, err := s.readProject(dir)
	if err != nil {
		return nil
	}
	canon := cur.canonical()
	if string(canon) == "{}" {
		return nil
	}
	_, ok, err := s.approved(dir)
	if err != nil || ok {
		return err
	}
	return s.record(dir, canon)
}

// Keep records an owner's change to the project config in dir, from the
// file content before to after (nil when there was no file), and keeps the
// project trusted when before was. The owner wrote the change, so it needs
// no second look; but a project that was waiting for review stays waiting,
// so an owner edit elsewhere in the file doesn't approve what an agent put
// there. Both sides are the bytes the owner's edit read and wrote, not
// what's on disk now, so an agent writing the file around the owner's edit
// gets neither version trusted.
func (s *TrustStore) Keep(dir string, before, after []byte) error {
	trustMu.Lock()
	defer trustMu.Unlock()
	prev, err := fieldsOfData(before)
	if err != nil {
		// Unreadable before the write (say, not valid HJSON): nothing to
		// keep.
		return nil
	}
	next, err := fieldsOfData(after)
	if err != nil {
		return err
	}
	canon := prev.canonical()
	if string(canon) != "{}" {
		approved, ok, err := s.approved(dir)
		if err != nil {
			return err
		}
		if !ok || !bytes.Equal(approved.canonical(), canon) {
			return nil
		}
	}
	return s.record(dir, next.canonical())
}

// record stores canon as the trusted fields of dir. The caller holds
// trustMu.
func (s *TrustStore) record(dir string, canon []byte) error {
	all, err := s.readAll()
	if err != nil {
		return err
	}
	all[filepath.Clean(dir)] = trustEntry{Fields: canon, TrustedAt: s.now().UTC()}
	return s.writeAll(all)
}

// fieldsOfData returns the trusted fields of project config content; nil
// content, a missing file, has none.
func fieldsOfData(data []byte) (trustedFields, error) {
	if data == nil {
		return trustedFields{}, nil
	}
	pc, err := parseProjectConfig(data)
	if err != nil {
		return trustedFields{}, err
	}
	return trustedFieldsOf(pc), nil
}

func hashFields(canon []byte) string {
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}

func indentJSON(canon []byte) string {
	var buf bytes.Buffer
	_ = json.Indent(&buf, canon, "", "  ") // canon is valid JSON
	return buf.String()
}

// parseProjectConfig decodes a project config file. data is left as is
// (hujson.Standardize rewrites its input in place).
func parseProjectConfig(data []byte) (*projectConfig, error) {
	standardJSON, err := hujson.Standardize(bytes.Clone(data))
	if err != nil {
		return nil, fmt.Errorf("parsing project config file: %w", err)
	}
	var pc projectConfig
	if err := json.Unmarshal(standardJSON, &pc); err != nil {
		return nil, fmt.Errorf("parsing project config file: %w", err)
	}
	return &pc, nil
}
