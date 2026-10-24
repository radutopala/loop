package agentgate

import (
	"context"
	"syscall"

	"github.com/radutopala/loop/internal/types"
)

// Spec: a path rule names a socket, not a spelling. Every sun_path the
// kernel resolves to the denied socket must get the rule's decision.
//
// Aliases exercised: duplicate slashes, "." and ".." components, a path
// relative to the caller's cwd, the Debian /var/run -> /run directory link,
// and a symlink the agent planted in /tmp.
func (s *ServerSuite) TestDispatchConnectDeniedSocketAliasesAreDenied() {
	const denied = "/var/run/docker.sock.host"
	policy := s.mustPolicy(
		types.DecisionAllow,
		[]types.PathRule{{Pattern: denied, Decision: types.DecisionDeny}},
		nil, nil,
	)
	links := map[string]string{
		"/run/docker.sock.host":     denied,
		"/var/run/docker.sock.host": denied,
		"/tmp/s":                    denied,
	}
	for _, alias := range []struct{ name, path, cwd string }{
		{"canonical", denied, "/"},
		{"double slash", "/var/run//docker.sock.host", "/"},
		{"dot", "/var/run/./docker.sock.host", "/"},
		{"dotdot", "/var/lib/../run/docker.sock.host", "/"},
		{"relative", "docker.sock.host", "/var/run"},
		{"run link", "/run/docker.sock.host", "/"},
		{"planted symlink", "/tmp/s", "/"},
	} {
		s.Run(alias.name, func() {
			addr := append([]byte{1, 0}, []byte(alias.path)...)
			addr = append(addr, 0)
			tr := &FakeTracee{
				Bytes:    map[uintptr][]byte{0x100: addr},
				Dirfds:   map[int32]string{AtFDCWD: alias.cwd},
				Symlinks: links,
			}
			srv := s.newServer(tr, nil, nil, NewConnectHandler(policy, nil))
			got := srv.Dispatch(context.Background(), Trap{
				ID: 30, Syscall: "connect",
				Args: [6]uint64{3, 0x100, uint64(len(addr))},
			})
			s.Require().False(got.Allow, "connect(%q) from cwd %s reached the denied socket", alias.path, alias.cwd)
			s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
		})
	}
}

// Same spec with the link pointing the way Debian images actually have it:
// /var/run is a symlink to /run, so the rule's own spelling is the alias.
func (s *ServerSuite) TestDispatchConnectRuleSpelledThroughDirLinkStillMatches() {
	policy := s.mustPolicy(
		types.DecisionAllow,
		[]types.PathRule{{Pattern: "/var/run/docker.sock.host", Decision: types.DecisionDeny}},
		nil, nil,
	)
	for _, p := range []string{"/run/docker.sock.host", "/run/./docker.sock.host", "/var/run/docker.sock.host"} {
		addr := append(append([]byte{1, 0}, []byte(p)...), 0)
		tr := &FakeTracee{
			Bytes:    map[uintptr][]byte{0x100: addr},
			Symlinks: map[string]string{"/var/run/docker.sock.host": "/run/docker.sock.host"},
		}
		srv := s.newServer(tr, nil, nil, NewConnectHandler(policy, nil))
		got := srv.Dispatch(context.Background(), Trap{ID: 31, Syscall: "connect", Args: [6]uint64{3, 0x100, uint64(len(addr))}})
		s.Require().False(got.Allow, "connect(%q) reached the denied socket", p)
	}
}

func (s *ServerSuite) TestDispatchConnectRelativePathUnreadableCwdDenies() {
	addr := append(append([]byte{1, 0}, []byte("x.sock")...), 0)
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x100: addr}} // no cwd mapped
	srv := s.newServer(tr, nil, nil, NewConnectHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil))
	got := srv.Dispatch(context.Background(), Trap{ID: 32, Syscall: "connect", Args: [6]uint64{3, 0x100, uint64(len(addr))}})
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

func (s *ServerSuite) TestDispatchConnectAbstractSocketMatchesByName() {
	addr := append([]byte{1, 0, 0}, []byte("loop-x")...)
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x100: addr}}
	policy := s.mustPolicy(types.DecisionAllow, []types.PathRule{{Pattern: "@loop-x", Decision: types.DecisionDeny}}, nil, nil)
	srv := s.newServer(tr, nil, nil, NewConnectHandler(policy, nil))
	got := srv.Dispatch(context.Background(), Trap{ID: 33, Syscall: "connect", Args: [6]uint64{3, 0x100, uint64(len(addr))}})
	s.Require().False(got.Allow)
}
