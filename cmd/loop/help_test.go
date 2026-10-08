package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HelpSuite struct {
	suite.Suite
}

func TestHelpSuite(t *testing.T) {
	suite.Run(t, new(HelpSuite))
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func noop(*cobra.Command, []string) {}

// tree builds a small command tree: a top-level command whose name carries a
// colon, a group with two runnable children, and a hidden command.
func (s *HelpSuite) tree() *cobra.Command {
	root := &cobra.Command{Use: "loop", Long: rootLong}
	url := &cobra.Command{Use: "app:url", Short: "Print a URL", Aliases: []string{"u"}, Run: noop}
	url.Flags().String("base", "http://localhost:5173/", "Web UI URL")
	url.Flags().Bool("open", false, "Open it")
	url.Flags().String("secret", "", "Hidden flag")
	s.Require().NoError(url.Flags().MarkHidden("secret"))
	group := &cobra.Command{Use: "review", Short: "Drive review runs"}
	run := &cobra.Command{Use: "run", Short: "Trigger a run", Aliases: []string{"r", "go"}, Run: noop}
	run.Flags().String("timeout", "75m", "Max wait")
	dedup := &cobra.Command{Use: "dedup", Short: "Drop duplicates", Run: noop}
	group.AddCommand(run, dedup)
	hidden := &cobra.Command{Use: "internal", Hidden: true, Run: noop}
	root.AddCommand(url, group, hidden)
	nameHelpFlags(root)
	root.SetHelpFunc(help)
	root.SetUsageFunc(usage)
	return root
}

func (s *HelpSuite) TestExpandArgs() {
	root := s.tree()
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"nested path is split", []string{"review:run", "--timeout", "5m"}, []string{"review", "run", "--timeout", "5m"}},
		{"path after help is split", []string{"help", "review:dedup"}, []string{"help", "review", "dedup"}},
		{"top-level name with a colon is kept", []string{"app:url"}, []string{"app:url"}},
		{"flag value with a colon is kept", []string{"app:url", "--base", "http://h:1/"}, []string{"app:url", "--base", "http://h:1/"}},
		{"unknown path is kept", []string{"review:nope"}, []string{"review:nope"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, expandArgs(root, tt.in))
		})
	}
}

func (s *HelpSuite) TestRootHelpListsCommandsFromTree() {
	root := s.tree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	require.NoError(s.T(), root.Execute())

	got := out.String()
	require.Contains(s.T(), got, rootLong+"\n\nUsage:\n  loop [command]\n\nAvailable Commands:")
	require.Contains(s.T(), got, "  app:url                  Print a URL (alias: u)\n")
	require.Contains(s.T(), got, "    --base                 Web UI URL [default: http://localhost:5173/]\n")
	require.Contains(s.T(), got, "    --open                 Open it\n")
	require.Contains(s.T(), got, "  review:dedup             Drop duplicates\n")
	require.Contains(s.T(), got, "  review:run               Trigger a run (aliases: r, go)\n")
	require.Contains(s.T(), got, "    --timeout              Max wait [default: 75m]")
	require.NotContains(s.T(), got, "secret")
	require.NotContains(s.T(), got, "internal")
	require.NotContains(s.T(), got, "\n  review ")
	require.NotContains(s.T(), got, "\nFlags:")
	require.Contains(s.T(), got, "Use \"loop [command] --help\" for more information about a command.")
}

func (s *HelpSuite) TestCommandHelpUsesColonPath() {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "leaf",
			args: []string{"review", "run", "--help"},
			want: []string{"Trigger a run\n\nUsage:\n  loop review:run [flags]\n\nFlags:\n", "--timeout string", "help for review:run"},
		},
		{
			name: "group",
			args: []string{"review", "--help"},
			want: []string{"Drive review runs\n\nUsage:\n  loop [command]\n\nAvailable Commands:\n  review:dedup", "help for review\n"},
		},
		{
			name: "inherited flags",
			args: []string{"review", "run", "--help"},
			want: []string{"Global Flags:\n      --verbose   Log more"},
		},
		{
			name: "no description",
			args: []string{"internal", "--help"},
			want: []string{"Usage:\n  loop internal [flags]"},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			root := s.tree()
			root.PersistentFlags().Bool("verbose", false, "Log more")
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(tt.args)
			require.NoError(s.T(), root.Execute())
			for _, w := range tt.want {
				require.Contains(s.T(), out.String(), w)
			}
		})
	}
}

func (s *HelpSuite) TestUsageAfterError() {
	root := s.tree()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"review", "run", "--bogus"})
	require.Error(s.T(), root.Execute())
	require.Contains(s.T(), errOut.String(), "unknown flag: --bogus")
	require.Contains(s.T(), out.String(), "Usage:\n  loop review:run [flags]")
}

func (s *HelpSuite) TestUsageWriteError() {
	root := s.tree()
	root.SetOut(failingWriter{})
	require.EqualError(s.T(), usage(root), "closed")
}

func (s *HelpSuite) TestRealRootHelpListsAuthCommands() {
	root := newApp().newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	require.NoError(s.T(), root.Execute())
	for _, name := range []string{"app:url", "api:rotate-token", "review:run", "quality:scan", "serve"} {
		require.Contains(s.T(), out.String(), "\n  "+name+" ")
	}
	require.NotContains(s.T(), out.String(), "syscallwrap")
	require.NotContains(s.T(), out.String(), "dockerproxy")
}
