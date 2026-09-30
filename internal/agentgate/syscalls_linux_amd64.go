package agentgate

import "golang.org/x/sys/unix"

// archSyscallNames lists the x86_64 legacy path syscalls. glibc's rename()
// calls rename(2) here, and static or older binaries still use open(2),
// creat(2) and friends; without these traps they would skip the gate.
var archSyscallNames = map[int32]string{
	int32(unix.SYS_OPEN):    syscallOpen,
	int32(unix.SYS_CREAT):   syscallCreat,
	int32(unix.SYS_RENAME):  syscallRename,
	int32(unix.SYS_MKDIR):   syscallMkdir,
	int32(unix.SYS_RMDIR):   syscallRmdir,
	int32(unix.SYS_LINK):    syscallLink,
	int32(unix.SYS_UNLINK):  syscallUnlink,
	int32(unix.SYS_SYMLINK): syscallSymlink,
	int32(unix.SYS_CHMOD):   syscallChmod,
	int32(unix.SYS_CHOWN):   syscallChown,
	int32(unix.SYS_LCHOWN):  syscallLchown,
	int32(unix.SYS_MKNOD):   syscallMknod,
}
