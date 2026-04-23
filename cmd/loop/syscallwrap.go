package main

import (
	"os"

	"github.com/spf13/cobra"
)

// newSyscallwrapCmd is the hidden subcommand entrypoint.sh invokes inside the
// agent container. It takes everything after "--" (or after the subcommand
// name if "--" is omitted) and forwards it to the syscallwrap package, which
// implements the seccomp fork-parent / child-install flow.
//
// DisableFlagParsing=true so cobra doesn't try to consume "-p" etc. that are
// meant for the target command (claude).
func (a *app) newSyscallwrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "syscallwrap [--] <cmd> [args...]",
		Short:              "Seccomp-gate wrapper (internal; invoked by agent container entrypoint)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			a.osExit(a.syscallwrapRun(args, os.Args))
			return nil
		},
	}
}
