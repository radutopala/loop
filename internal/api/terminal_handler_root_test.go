package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// --- shellSingleQuote ---

func (s *TerminalHandlerSuite) TestShellSingleQuote() {
	cases := []struct{ in, want string }{
		{"/home/user/dev", "'/home/user/dev'"},
		{"/has space/dir", "'/has space/dir'"},
		{"a'b", `'a'\''b'`},
		{"", "''"},
	}
	for _, c := range cases {
		require.Equal(s.T(), c.want, shellSingleQuote(c.in))
	}
}

// --- resolveRootDir ---

func (s *TerminalHandlerSuite) TestResolveRootDir() {
	roots := []string{"/primary", "/extra/one", ""}
	withResolver := &terminalWSConn{
		rootDirs: func(context.Context, string) ([]string, error) { return roots, nil },
	}
	errResolver := &terminalWSConn{
		rootDirs: func(context.Context, string) ([]string, error) { return nil, errors.New("boom") },
	}
	noResolver := &terminalWSConn{}

	ctx := context.Background()
	// rootIndex <= 0 keeps the default.
	require.Equal(s.T(), "/def", withResolver.resolveRootDir(ctx, "ch", 0, "/def"))
	require.Equal(s.T(), "/def", withResolver.resolveRootDir(ctx, "ch", -1, "/def"))
	// no resolver / empty channel keep the default.
	require.Equal(s.T(), "/def", noResolver.resolveRootDir(ctx, "ch", 1, "/def"))
	require.Equal(s.T(), "/def", withResolver.resolveRootDir(ctx, "", 1, "/def"))
	// resolver error keeps the default.
	require.Equal(s.T(), "/def", errResolver.resolveRootDir(ctx, "ch", 1, "/def"))
	// out-of-range index keeps the default.
	require.Equal(s.T(), "/def", withResolver.resolveRootDir(ctx, "ch", 9, "/def"))
	// empty resolved path keeps the default.
	require.Equal(s.T(), "/def", withResolver.resolveRootDir(ctx, "ch", 2, "/def"))
	// valid index returns the selected root.
	require.Equal(s.T(), "/extra/one", withResolver.resolveRootDir(ctx, "ch", 1, "/def"))
}

// writeProjectExtraDirs creates {dir}/.loop/config.json declaring extra_dirs so
// the server's allDirPaths resolver returns multiple workspace roots.
func (s *TerminalHandlerSuite) writeProjectExtraDirs(dir string, extras ...string) {
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, ".loop"), 0o755))
	quoted := make([]string, len(extras))
	for i, e := range extras {
		quoted[i] = `"` + e + `"`
	}
	body := `{"extra_dirs":[` + strings.Join(quoted, ",") + `]}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, ".loop", "config.json"), []byte(body), 0o644))
}

// Host shell opened with root_index=1 starts in the selected extra root.
func (s *TerminalHandlerSuite) TestCreateHostSessionWithRootIndex() {
	primary := s.T().TempDir()
	extra := s.T().TempDir()
	s.writeProjectExtraDirs(primary, extra)

	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-root").
		Return(&db.Channel{ChannelID: "ch-root", DirPath: primary}, nil)
	s.srv.store = store

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, extra, []string(nil)).
		Return("host-root", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-root", Target: "host", RootIndex: 1})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "host-root", msg.SessionID)
	hostMgr.AssertExpectations(s.T())
	close(doneCh)
}

// Docker shell opened with root_index=1 cd's into the selected extra root,
// which is bind-mounted at its real path inside the shell container.
func (s *TerminalHandlerSuite) TestCreateDockerShellCdIntoRoot() {
	primary := s.T().TempDir()
	extra := s.T().TempDir()
	s.writeProjectExtraDirs(primary, extra)

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-root").
		Return(&db.Channel{ChannelID: "ch-root", DirPath: primary}, nil)
	s.srv.store = store

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", []string{"/bin/bash"}).
		Return("sess-root", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)

	cdCalled := make(chan []byte, 1)
	s.terminal.On("SendInput", "sess-root", mock.AnythingOfType("[]uint8")).
		Run(func(args mock.Arguments) { cdCalled <- args.Get(1).([]byte) }).
		Return(nil)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{
		Type: "create", ContainerID: "ctr-1", ChannelID: "ch-root",
		Cmd: []string{"/bin/bash"}, RootIndex: 1,
	})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)

	select {
	case got := <-cdCalled:
		require.Equal(s.T(), "cd "+shellSingleQuote(extra)+"\n", string(got))
	case <-doneCh:
		s.T().Fatal("session ended before cd input")
	}
	close(doneCh)
}

// A failed cd SendInput is logged and does not abort the session.
func (s *TerminalHandlerSuite) TestCreateDockerShellCdInputError() {
	primary := s.T().TempDir()
	extra := s.T().TempDir()
	s.writeProjectExtraDirs(primary, extra)

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-root").
		Return(&db.Channel{ChannelID: "ch-root", DirPath: primary}, nil)
	s.srv.store = store

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", []string{"/bin/bash"}).
		Return("sess-err", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)

	cdCalled := make(chan struct{}, 1)
	s.terminal.On("SendInput", "sess-err", mock.AnythingOfType("[]uint8")).
		Run(func(mock.Arguments) { cdCalled <- struct{}{} }).
		Return(errors.New("send failed"))

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{
		Type: "create", ContainerID: "ctr-1", ChannelID: "ch-root",
		Cmd: []string{"/bin/bash"}, RootIndex: 1,
	})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)

	select {
	case <-cdCalled:
	case <-doneCh:
		s.T().Fatal("session ended before cd input")
	}
	close(doneCh)
}
