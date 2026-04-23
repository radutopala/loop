package agentgate

import "errors"

// Tracee abstracts reads against a traced process's memory + /proc view.
// The server-side notify loop wraps a real Tracee (ProcTracee on Linux) per
// trap; tests use a FakeTracee with canned responses.
//
// Every method is fail-closed: an error return must translate to a Deny at
// the handler layer. Callers MUST NOT retry — the notify window is short and
// the tracee may have exited.
type Tracee interface {
	// ReadString returns a NULL-terminated C string at addr, truncated to
	// PATHMAX. Returns os.ErrClosed (wrapped ErrTraceeGone) when the process
	// has already exited.
	ReadString(addr uintptr) (string, error)

	// ReadBytes reads exactly n bytes of raw memory at addr. Used for fixed-
	// width structs (sockaddr, struct open_how). Short reads return
	// ErrTraceeGone. n must be positive and small (kernel caps individual
	// process_vm_readv iovecs).
	ReadBytes(addr uintptr, n int) ([]byte, error)

	// ReadPointerArray walks a NULL-terminated array of pointers starting at
	// addr and reads each referenced string. Capped at maxEntries entries and
	// PATHMAX bytes per string. Truncation is silent — caller should assume
	// the full argv may be longer than what we see.
	ReadPointerArray(addr uintptr, maxEntries int) ([]string, error)

	// ResolveDirfd returns the absolute path that dirfd points at. Handles
	// the AtFDCWD sentinel (−100 on every Linux arch) by reading
	// /proc/<pid>/cwd. Returns ErrTraceeGone on ESRCH / ENOENT for
	// /proc/<pid>/fd, so the caller can treat vanished tracees as deny-closed.
	ResolveDirfd(dirfd int32) (string, error)

	// EvalSymlinks returns the fully-dereferenced path. On error (broken
	// symlink, permission, loop), returns (path, err) — the caller denies.
	EvalSymlinks(path string) (string, error)
}

// ErrTraceeGone is returned by Tracee methods when the traced process has
// already exited. Handlers respond to this by denying the trap (the kernel
// will surface the real failure when the tracee is reaped).
var ErrTraceeGone = errors.New("agentgate: tracee process is gone")

// AtFDCWD is the sentinel "current working directory" dirfd on every Linux
// architecture (−100 cast to int32). Mirrors unix.AT_FDCWD.
const AtFDCWD int32 = -100

// PATHMAX caps a single path read from tracee memory. Linux PATH_MAX is 4096;
// we use the same value.
const PATHMAX = 4096

// ArgvMax caps the number of entries we read from an argv/envp pointer
// array. Agent argv in the wild tops out near 200 elements (long go build
// command lines); 1024 is comfortable headroom without unbounded memory.
const ArgvMax = 1024

// FakeTracee is an in-memory Tracee for tests. Populate the maps; any key
// not in a map returns ErrTraceeGone (mimics tracee exit).
type FakeTracee struct {
	Strings      map[uintptr]string   // addr → string
	Bytes        map[uintptr][]byte   // addr → raw bytes (ReadBytes)
	PointerLists map[uintptr][]string // argv/envp head → list
	Dirfds       map[int32]string     // dirfd → resolved absolute path
	Symlinks     map[string]string    // evaluated-from → evaluated-to
	StringErr    error                // if non-nil, ReadString returns this unconditionally
	BytesErr     error                // if non-nil, ReadBytes returns this unconditionally
}

// ReadString returns t.Strings[addr] or ErrTraceeGone when absent. Pre-empted
// by StringErr when set.
func (t *FakeTracee) ReadString(addr uintptr) (string, error) {
	if t.StringErr != nil {
		return "", t.StringErr
	}
	s, ok := t.Strings[addr]
	if !ok {
		return "", ErrTraceeGone
	}
	return s, nil
}

// ReadBytes returns t.Bytes[addr] (copy, capped to n) or ErrTraceeGone when
// absent. Pre-empted by BytesErr when set. Short stored-byte slices yield
// ErrTraceeGone — mirrors ProcTracee's behaviour on a partial read.
func (t *FakeTracee) ReadBytes(addr uintptr, n int) ([]byte, error) {
	if t.BytesErr != nil {
		return nil, t.BytesErr
	}
	b, ok := t.Bytes[addr]
	if !ok {
		return nil, ErrTraceeGone
	}
	if len(b) < n {
		return nil, ErrTraceeGone
	}
	out := make([]byte, n)
	copy(out, b[:n])
	return out, nil
}

// ReadPointerArray returns t.PointerLists[addr], truncated to maxEntries.
// Returns ErrTraceeGone when absent.
func (t *FakeTracee) ReadPointerArray(addr uintptr, maxEntries int) ([]string, error) {
	list, ok := t.PointerLists[addr]
	if !ok {
		return nil, ErrTraceeGone
	}
	if maxEntries > 0 && len(list) > maxEntries {
		list = list[:maxEntries]
	}
	return append([]string(nil), list...), nil
}

// ResolveDirfd returns t.Dirfds[dirfd] or ErrTraceeGone.
func (t *FakeTracee) ResolveDirfd(dirfd int32) (string, error) {
	p, ok := t.Dirfds[dirfd]
	if !ok {
		return "", ErrTraceeGone
	}
	return p, nil
}

// EvalSymlinks returns t.Symlinks[path] if present, else (path, nil) — mirrors
// filepath.EvalSymlinks for a path that contains no links.
func (t *FakeTracee) EvalSymlinks(path string) (string, error) {
	if p, ok := t.Symlinks[path]; ok {
		return p, nil
	}
	return path, nil
}
