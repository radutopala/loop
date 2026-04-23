package agentgate

// File-op syscall classification. The server-side dispatcher uses these helpers
// to turn a raw seccomp trap into a FileRequest: which argv slot holds the
// path, which holds the dirfd, which holds flags, and what FileOp the syscall
// effectively performs.
//
// Keeping this build-tag-free means the table is the single source of truth on
// all platforms; bpf.go and server.go (linux-only) consume it, and tests can
// run on any host.

// Syscall name constants. String literals — we do not pin to
// golang.org/x/sys/unix.SYS_* values here because the dispatcher keys by the
// canonical name (filled in by the notify receiver from the raw number), not
// the number itself. That lets the classifier table run on darwin for tests.
const (
	syscallOpenat    = "openat"
	syscallOpenat2   = "openat2"
	syscallRenameat2 = "renameat2"
	syscallUnlinkat  = "unlinkat"
	syscallLinkat    = "linkat"
	syscallSymlinkat = "symlinkat"
	syscallFchmodat  = "fchmodat"
	syscallFchownat  = "fchownat"
	syscallMkdirat   = "mkdirat"
)

// openat(2) flag bits. Mirrors include/uapi/asm-generic/fcntl.h. We redefine
// locally so the classifier works without a unix import.
const (
	oRDONLY  uint64 = 0x0
	oWRONLY  uint64 = 0x1
	oRDWR    uint64 = 0x2
	oAccMode uint64 = 0x3
	oCreat   uint64 = 0x40
	oTrunc   uint64 = 0x200
	oAppend  uint64 = 0x400
)

// unlinkat(2) flag: remove directory (equivalent to rmdir).
const atRemoveDir uint64 = 0x200

// SyscallSpec describes one trapped file-op syscall's argv layout.
//
// For syscalls that reference two paths (renameat2, linkat), only the primary
// path used for policy matching is recorded here — the dispatcher handles the
// second path by issuing a second MatchFile call with SecondaryOp.
type SyscallSpec struct {
	Name           string
	PrimaryOp      string // op for the primary path
	PathArgIdx     int    // argv index of the (primary) path string
	DirfdArgIdx    int    // argv index of the dirfd; -1 if none
	FlagsArgIdx    int    // argv index of the flags word; -1 if none
	SecondaryOp    string // non-empty for two-path syscalls (renameat2 → delete+create)
	SecondPathIdx  int    // argv index of the secondary path; -1 if none
	SecondDirfdIdx int    // argv index of the secondary dirfd; -1 if none
}

// syscallTable is the classifier's lookup. First hit wins; the dispatcher
// rejects unknown names as a bug (the BPF filter and this table must agree).
var syscallTable = map[string]SyscallSpec{
	// openat(dirfd, path, flags, mode) — op depends on flags; classify in ClassifyOpenatFlags.
	syscallOpenat: {
		Name: syscallOpenat, PrimaryOp: "", // filled in by caller after flag classification
		PathArgIdx: 1, DirfdArgIdx: 0, FlagsArgIdx: 2,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
	// openat2(dirfd, path, *open_how, size) — flags live inside open_how at offset 0.
	// We expose FlagsArgIdx=-1 because the flags word is not a direct argv slot;
	// the dispatcher reads 8 bytes from *open_how and calls ClassifyOpenatFlags.
	syscallOpenat2: {
		Name: syscallOpenat2, PrimaryOp: "",
		PathArgIdx: 1, DirfdArgIdx: 0, FlagsArgIdx: -1,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
	// renameat2(olddirfd, oldpath, newdirfd, newpath, flags)
	// primary = delete on oldpath; secondary = create on newpath.
	syscallRenameat2: {
		Name: syscallRenameat2, PrimaryOp: OpDelete,
		PathArgIdx: 1, DirfdArgIdx: 0, FlagsArgIdx: 4,
		SecondaryOp: OpCreate, SecondPathIdx: 3, SecondDirfdIdx: 2,
	},
	// unlinkat(dirfd, path, flags) — AT_REMOVEDIR flips delete→delete (dir), same op.
	syscallUnlinkat: {
		Name: syscallUnlinkat, PrimaryOp: OpDelete,
		PathArgIdx: 1, DirfdArgIdx: 0, FlagsArgIdx: 2,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
	// linkat(olddirfd, oldpath, newdirfd, newpath, flags) — policy matches newpath.
	syscallLinkat: {
		Name: syscallLinkat, PrimaryOp: OpLink,
		PathArgIdx: 3, DirfdArgIdx: 2, FlagsArgIdx: 4,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
	// symlinkat(target, newdirfd, linkpath) — policy matches linkpath.
	syscallSymlinkat: {
		Name: syscallSymlinkat, PrimaryOp: OpLink,
		PathArgIdx: 2, DirfdArgIdx: 1, FlagsArgIdx: -1,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
	// fchmodat(dirfd, path, mode, flags)
	syscallFchmodat: {
		Name: syscallFchmodat, PrimaryOp: OpChmod,
		PathArgIdx: 1, DirfdArgIdx: 0, FlagsArgIdx: 3,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
	// fchownat(dirfd, path, uid, gid, flags)
	syscallFchownat: {
		Name: syscallFchownat, PrimaryOp: OpChown,
		PathArgIdx: 1, DirfdArgIdx: 0, FlagsArgIdx: 4,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
	// mkdirat(dirfd, path, mode)
	syscallMkdirat: {
		Name: syscallMkdirat, PrimaryOp: OpCreate,
		PathArgIdx: 1, DirfdArgIdx: 0, FlagsArgIdx: -1,
		SecondPathIdx: -1, SecondDirfdIdx: -1,
	},
}

// SyscallByName looks up a spec by canonical syscall name; ok=false when
// unknown (dispatcher should treat that as a bug, not a silent allow).
func SyscallByName(name string) (SyscallSpec, bool) {
	s, ok := syscallTable[name]
	return s, ok
}

// ClassifyOpenatFlags maps the `flags` word of openat(2)/openat2(2) to a FileOp:
//
//	O_CREAT set                                               → create
//	access mode WRONLY|RDWR, or O_TRUNC/O_APPEND (no O_CREAT) → write
//	access mode RDONLY with no truncation/append              → read
//
// This is the only place openat flag semantics live — if we ever add op
// variants (O_PATH = stat, O_DIRECTORY = list), they extend here.
func ClassifyOpenatFlags(flags uint64) string {
	if flags&oCreat != 0 {
		return OpCreate
	}
	if flags&(oTrunc|oAppend) != 0 {
		return OpWrite
	}
	switch flags & oAccMode {
	case oWRONLY, oRDWR:
		return OpWrite
	case oRDONLY:
		return OpRead
	default:
		// Unknown access-mode bits (kernel may add future values); treat as
		// read — handlers can still apply a rule against it. We do NOT
		// silently allow; the caller runs the policy regardless.
		return OpRead
	}
}

// IsRemoveDir reports whether the unlinkat(2) flags word asks the kernel to
// remove a directory (AT_REMOVEDIR). Dispatchers use this for audit detail
// — the policy op stays "delete" either way.
func IsRemoveDir(flags uint64) bool {
	return flags&atRemoveDir != 0
}
