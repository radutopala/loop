package agentgate

// archSyscallNames is empty on arm64: the architecture only has the *at
// forms, which the common table already traps.
var archSyscallNames = map[int32]string{}
