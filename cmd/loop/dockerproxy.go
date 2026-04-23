package main

import (
	"os"

	"github.com/spf13/cobra"
)

// newDockerproxyCmd is the hidden subcommand invoked by the agent container
// entrypoint. It runs the in-container docker-socket reverse proxy until
// SIGTERM/SIGINT.
func (a *app) newDockerproxyCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "dockerproxy",
		Short:  "In-container docker-socket proxy (internal; invoked by agent container entrypoint)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			a.osExit(a.dockerproxyRun(os.Stdout, os.Stderr))
			return nil
		},
	}
}
