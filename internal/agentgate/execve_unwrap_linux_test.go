//go:build linux

package agentgate

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// TestUnwrapCommandMatchesRealWrappers runs each table row through the real
// wrapper binary and checks the command it execs is the one unwrapCommand
// names. A row whose wrapper isn't installed is skipped.
func (s *ExecveSuite) TestUnwrapCommandMatchesRealWrappers() {
	// The stubs first on PATH: the intended payload, plus every name a
	// mis-read option value or operand could turn into a command, so the
	// argv a real wrapper execs is always recorded.
	stubs := []string{"payloadcmd", "5", "root", "X", "INT", "KILL", "L", "0", "1", "3", "a", "foo", "nobody", "-", "--", "-u"}
	dir := s.T().TempDir()
	out := filepath.Join(dir, "out")
	for _, name := range stubs {
		script := "#!/bin/sh\nprintf '%s\\0' \"$0\" \"$@\" > '" + out + "'\n"
		s.Require().NoError(os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))
	}
	loaders, _ := filepath.Glob("/lib*/ld-linux*.so*")
	path := dir + string(os.PathListSeparator) + os.Getenv("PATH")

	for _, tc := range unwrapCases() {
		s.Run(tc.name, func() {
			if tc.noRun {
				s.T().Skip("needs a privilege, a terminal or sudo")
			}
			loader := ""
			if len(loaders) > 0 {
				loader = loaders[0]
			}
			argv := resolveUnwrapArgs(tc.argv, dir, loader)
			bin, err := exec.LookPath(argv[0])
			if err != nil {
				s.T().Skipf("%s isn't installed", tc.argv[0])
			}
			got := unwrapCommand(argv)

			s.Require().NoError(os.RemoveAll(out))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, argv[1:]...)
			cmd.Args[0] = argv[0]
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+path)
			_ = cmd.Run() // the wrapper's exit status doesn't matter, only what it ran
			s.Require().NoError(ctx.Err(), "wrapper timed out")

			raw, err := os.ReadFile(out)
			if errors.Is(err, fs.ErrNotExist) {
				ranStub := slices.Contains(stubs, filepath.Base(got[0])) && !tc.fails
				s.Require().Falsef(ranStub, "wrapper ran nothing, unwrapCommand named %q", got)
				return
			}
			s.Require().NoError(err)
			ran := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			if !slices.Equal(got, argv) || !tc.runs {
				s.Require().Falsef(tc.fails, "wrapper ran %q though the row says it fails", ran)
				s.Require().Equal(filepath.Base(got[0]), filepath.Base(ran[0]), "command")
				s.Require().Equal(got[1:], ran[1:], "arguments")
			}
		})
	}
}
