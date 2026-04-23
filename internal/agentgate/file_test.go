package agentgate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type FileSuite struct {
	suite.Suite
}

func TestFileSuite(t *testing.T) {
	suite.Run(t, new(FileSuite))
}

// --- helpers ---

func (s *FileSuite) mustFilePolicy(def types.Decision, rules ...types.FileRule) *Policy {
	p, err := CompilePolicy(def, nil, nil, rules)
	require.NoError(s.T(), err)
	return p
}

// --- NewFileHandler / NewFileDecisionCache ---

func (s *FileSuite) TestNewFileHandlerWiresCache() {
	p := s.mustFilePolicy(types.DecisionAllow)
	ap := &fakeApprover{}
	h := NewFileHandler(p, ap, 32)
	s.Require().Same(p, h.Policy)
	s.Require().Same(ap, h.Approver.(*fakeApprover))
	s.Require().NotNil(h.Cache)
	s.Require().Equal(32, h.Cache.max)
}

func (s *FileSuite) TestNewFileDecisionCacheClampsZeroOrNegative() {
	s.Require().Equal(1024, NewFileDecisionCache(0).max)
	s.Require().Equal(1024, NewFileDecisionCache(-5).max)
	s.Require().Equal(8, NewFileDecisionCache(8).max)
}

// --- FileDecisionCache behaviour ---

func (s *FileSuite) TestCacheGetMissThenPutThenHit() {
	c := NewFileDecisionCache(4)
	k := fileCacheKey{op: OpRead, path: "/a"}
	_, ok := c.Get(k)
	s.Require().False(ok)
	c.Put(k, types.DecisionAllow)
	d, ok := c.Get(k)
	s.Require().True(ok)
	s.Require().Equal(types.DecisionAllow, d)
	s.Require().Equal(1, c.Len())
}

func (s *FileSuite) TestCachePutReplacesInPlaceWithoutReordering() {
	c := NewFileDecisionCache(2)
	k1 := fileCacheKey{op: OpRead, path: "/a"}
	k2 := fileCacheKey{op: OpRead, path: "/b"}
	c.Put(k1, types.DecisionAllow)
	c.Put(k2, types.DecisionDeny)
	// Re-putting k1 updates value but doesn't change ordering.
	c.Put(k1, types.DecisionDeny)
	got, _ := c.Get(k1)
	s.Require().Equal(types.DecisionDeny, got)
	s.Require().Equal(2, c.Len())
	// k1 is still the oldest — adding a third entry should evict it.
	c.Put(fileCacheKey{op: OpRead, path: "/c"}, types.DecisionAllow)
	_, ok := c.Get(k1)
	s.Require().False(ok, "k1 should have been evicted as the oldest")
	_, ok = c.Get(k2)
	s.Require().True(ok)
}

func (s *FileSuite) TestCacheEvictsOldestAtCapacity() {
	c := NewFileDecisionCache(3)
	keys := []fileCacheKey{
		{op: OpRead, path: "/1"},
		{op: OpRead, path: "/2"},
		{op: OpRead, path: "/3"},
	}
	for _, k := range keys {
		c.Put(k, types.DecisionAllow)
	}
	s.Require().Equal(3, c.Len())
	c.Put(fileCacheKey{op: OpRead, path: "/4"}, types.DecisionAllow)
	s.Require().Equal(3, c.Len())
	_, ok := c.Get(keys[0])
	s.Require().False(ok, "oldest should be evicted")
	_, ok = c.Get(fileCacheKey{op: OpRead, path: "/4"})
	s.Require().True(ok)
}

func (s *FileSuite) TestCacheResetClearsEverything() {
	c := NewFileDecisionCache(4)
	c.Put(fileCacheKey{op: OpRead, path: "/a"}, types.DecisionAllow)
	c.Put(fileCacheKey{op: OpRead, path: "/b"}, types.DecisionDeny)
	s.Require().Equal(2, c.Len())
	c.Reset()
	s.Require().Equal(0, c.Len())
	_, ok := c.Get(fileCacheKey{op: OpRead, path: "/a"})
	s.Require().False(ok)
}

// --- Handle: rule paths ---

func (s *FileSuite) TestHandleRuleAllowCachesAndReports() {
	policy := s.mustFilePolicy(types.DecisionDeny, types.FileRule{
		Paths:      []string{"/work/**"},
		Operations: []string{"read"},
		Decision:   types.DecisionAllow,
	})
	h := NewFileHandler(policy, nil, 8)

	out := h.Handle(context.Background(), FileRequest{
		Op:   OpRead,
		Path: "/work/main.go",
	})
	s.Require().Equal(types.DecisionAllow, out.Decision)
	s.Require().False(out.FromCache)
	s.Require().Equal("file[0]", out.Reason)

	// Second call with same (op, path) must hit the cache.
	out2 := h.Handle(context.Background(), FileRequest{
		Op:   OpRead,
		Path: "/work/main.go",
	})
	s.Require().Equal(types.DecisionAllow, out2.Decision)
	s.Require().True(out2.FromCache)
	s.Require().Equal("cache-hit", out2.Reason)
}

func (s *FileSuite) TestHandleRuleDenyCaches() {
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"**/.ssh/**"},
		Operations: []string{"read"},
		Decision:   types.DecisionDeny,
		Message:    "ssh key read blocked",
	})
	h := NewFileHandler(policy, nil, 8)

	out := h.Handle(context.Background(), FileRequest{
		Op:   OpRead,
		Path: "/home/agent/.ssh/id_rsa",
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)
	s.Require().Equal("file[0]", out.Reason)

	out2 := h.Handle(context.Background(), FileRequest{
		Op:   OpRead,
		Path: "/home/agent/.ssh/id_rsa",
	})
	s.Require().True(out2.FromCache)
	s.Require().Equal(types.DecisionDeny, out2.Decision)
}

func (s *FileSuite) TestHandleCleansPathBeforeMatch() {
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"/etc/shadow"},
		Operations: []string{"read"},
		Decision:   types.DecisionDeny,
	})
	h := NewFileHandler(policy, nil, 8)

	// /work/../etc/shadow → Clean → /etc/shadow → rule fires.
	out := h.Handle(context.Background(), FileRequest{
		Op:   OpRead,
		Path: "/work/../etc/shadow",
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)
}

// --- Handle: approve dispatch ---

func (s *FileSuite) TestHandleApproveWithoutApproverDenies() {
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"/work/*.txt"},
		Operations: []string{"write"},
		Decision:   types.DecisionApprove,
	})
	h := NewFileHandler(policy, nil, 8)

	out := h.Handle(context.Background(), FileRequest{
		Op:   OpWrite,
		Path: "/work/notes.txt",
	})
	s.Require().Equal(types.DecisionDeny, out.Decision)
	s.Require().Equal("no-approver", out.Reason)
}

func (s *FileSuite) TestHandleApproveDispatchesToApprover() {
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"/work/*.txt"},
		Operations: []string{"write"},
		Decision:   types.DecisionApprove,
		Message:    "workspace write",
	})
	ap := &fakeApprover{outcome: Outcome{Decision: types.DecisionAllow, Actor: "u7"}}
	h := NewFileHandler(policy, ap, 8)

	out := h.Handle(context.Background(), FileRequest{
		ChannelID: "chan",
		Op:        OpWrite,
		Path:      "/work/notes.txt",
	})
	s.Require().Equal(types.DecisionAllow, out.Decision)
	s.Require().Equal("u7", out.Actor)
	s.Require().Equal(1, ap.calls)
	s.Require().Equal("file", ap.captured.Kind)
	s.Require().Equal("write /work/notes.txt", ap.captured.Target)
	s.Require().Equal("workspace write", ap.captured.Message)
	s.Require().Equal("file:write:/work/notes.txt", ap.captured.CacheKey)
}

func (s *FileSuite) TestHandleApproveOnceDoesNotPopulateLocalCache() {
	// Approver returns an Allow that is NOT marked FromCache — mirroring a
	// fresh "Allow once" click. The file handler should forward the allow but
	// not store it, so a repeat request re-dispatches.
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"/work/*.txt"},
		Operations: []string{"write"},
		Decision:   types.DecisionApprove,
	})
	ap := &fakeApprover{outcome: Outcome{Decision: types.DecisionAllow}}
	h := NewFileHandler(policy, ap, 8)

	_ = h.Handle(context.Background(), FileRequest{Op: OpWrite, Path: "/work/a.txt"})
	_ = h.Handle(context.Background(), FileRequest{Op: OpWrite, Path: "/work/a.txt"})
	s.Require().Equal(2, ap.calls, "allow-once must not be cached")
	s.Require().Equal(0, h.Cache.Len())
}

func (s *FileSuite) TestHandleApproveSessionPopulatesLocalCache() {
	// FromCache=true is how Manager.Request signals a persisted session
	// decision. File handler mirrors it into the local cache.
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"/work/*.txt"},
		Operations: []string{"write"},
		Decision:   types.DecisionApprove,
	})
	ap := &fakeApprover{outcome: Outcome{Decision: types.DecisionAllow, FromCache: true}}
	h := NewFileHandler(policy, ap, 8)

	out1 := h.Handle(context.Background(), FileRequest{Op: OpWrite, Path: "/work/a.txt"})
	s.Require().Equal(types.DecisionAllow, out1.Decision)
	s.Require().True(out1.FromCache)
	s.Require().Equal(1, ap.calls)
	s.Require().Equal(1, h.Cache.Len())

	// Second call should hit the local handler cache (not the approver).
	out2 := h.Handle(context.Background(), FileRequest{Op: OpWrite, Path: "/work/a.txt"})
	s.Require().Equal(types.DecisionAllow, out2.Decision)
	s.Require().True(out2.FromCache)
	s.Require().Equal("cache-hit", out2.Reason)
	s.Require().Equal(1, ap.calls, "second call should not reach approver")
}

func (s *FileSuite) TestHandleApproveDeniedOutcomeNotCachedLocally() {
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"/work/*.txt"},
		Operations: []string{"write"},
		Decision:   types.DecisionApprove,
	})
	// Approver returns a deny; file handler must NOT cache (deny from approver
	// is per-request policy — next identical op should re-prompt).
	ap := &fakeApprover{outcome: Outcome{Decision: types.DecisionDeny}}
	h := NewFileHandler(policy, ap, 8)

	out := h.Handle(context.Background(), FileRequest{Op: OpWrite, Path: "/work/a.txt"})
	s.Require().Equal(types.DecisionDeny, out.Decision)
	s.Require().Equal(0, h.Cache.Len())
}

// --- SyscallByName / syscallTable ---

func (s *FileSuite) TestSyscallByNameKnownEntries() {
	cases := []struct {
		name  string
		op    string // expected PrimaryOp ("" means openat-style, classified by flags)
		path  int
		dirfd int
		flags int
		secOp string
		sPath int
		sDfd  int
	}{
		{syscallOpenat, "", 1, 0, 2, "", -1, -1},
		{syscallOpenat2, "", 1, 0, -1, "", -1, -1},
		{syscallRenameat2, OpDelete, 1, 0, 4, OpCreate, 3, 2},
		{syscallUnlinkat, OpDelete, 1, 0, 2, "", -1, -1},
		{syscallLinkat, OpLink, 3, 2, 4, "", -1, -1},
		{syscallSymlinkat, OpLink, 2, 1, -1, "", -1, -1},
		{syscallFchmodat, OpChmod, 1, 0, 3, "", -1, -1},
		{syscallFchownat, OpChown, 1, 0, 4, "", -1, -1},
		{syscallMkdirat, OpCreate, 1, 0, -1, "", -1, -1},
	}
	for _, c := range cases {
		spec, ok := SyscallByName(c.name)
		s.Require().Truef(ok, "SyscallByName(%q) should be known", c.name)
		s.Require().Equal(c.name, spec.Name)
		s.Require().Equal(c.op, spec.PrimaryOp, "PrimaryOp for %s", c.name)
		s.Require().Equal(c.path, spec.PathArgIdx, "PathArgIdx for %s", c.name)
		s.Require().Equal(c.dirfd, spec.DirfdArgIdx, "DirfdArgIdx for %s", c.name)
		s.Require().Equal(c.flags, spec.FlagsArgIdx, "FlagsArgIdx for %s", c.name)
		s.Require().Equal(c.secOp, spec.SecondaryOp, "SecondaryOp for %s", c.name)
		s.Require().Equal(c.sPath, spec.SecondPathIdx, "SecondPathIdx for %s", c.name)
		s.Require().Equal(c.sDfd, spec.SecondDirfdIdx, "SecondDirfdIdx for %s", c.name)
	}
}

func (s *FileSuite) TestSyscallByNameUnknown() {
	_, ok := SyscallByName("not-a-syscall")
	s.Require().False(ok)
}

// --- ClassifyOpenatFlags ---

func (s *FileSuite) TestClassifyOpenatFlagsCreate() {
	s.Require().Equal(OpCreate, ClassifyOpenatFlags(oCreat))
	s.Require().Equal(OpCreate, ClassifyOpenatFlags(oCreat|oWRONLY))
	s.Require().Equal(OpCreate, ClassifyOpenatFlags(oCreat|oRDWR|oTrunc))
}

func (s *FileSuite) TestClassifyOpenatFlagsWriteVariants() {
	s.Require().Equal(OpWrite, ClassifyOpenatFlags(oWRONLY))
	s.Require().Equal(OpWrite, ClassifyOpenatFlags(oRDWR))
	s.Require().Equal(OpWrite, ClassifyOpenatFlags(oRDONLY|oTrunc))
	s.Require().Equal(OpWrite, ClassifyOpenatFlags(oRDONLY|oAppend))
}

func (s *FileSuite) TestClassifyOpenatFlagsRead() {
	s.Require().Equal(OpRead, ClassifyOpenatFlags(oRDONLY))
	s.Require().Equal(OpRead, ClassifyOpenatFlags(0))
}

func (s *FileSuite) TestClassifyOpenatFlagsUnknownAccessModeFallsBackToRead() {
	// Access-mode bits 0x3 = both WRONLY|RDWR set, which is an illegal
	// combination in practice but a valid input shape for the kernel.
	// The switch's default arm must return "read" so the policy still gets
	// a chance to match.
	s.Require().Equal(OpRead, ClassifyOpenatFlags(oWRONLY|oRDWR))
}

// --- audit emission ---

// TestHandleAuditsOnlyFirstMissNotCacheHits guards the invariant that a
// directory walk (same op+path repeated) produces exactly one audit entry,
// not one per hit. A prior revision logged every cache-hit too, which buried
// real decisions under thousands of near-identical lines during `find /work`.
func (s *FileSuite) TestHandleAuditsOnlyFirstMissNotCacheHits() {
	policy := s.mustFilePolicy(types.DecisionAllow, types.FileRule{
		Paths:      []string{"/work/**"},
		Operations: []string{"read"},
		Decision:   types.DecisionAllow,
	})
	rec := &recordingAuditor{}
	h := NewFileHandler(policy, nil, 8)
	h.Auditor = rec
	h.Now = stepClock()

	_ = h.Handle(context.Background(), FileRequest{PID: 4242, ChannelID: "c1", Op: OpRead, Path: "/work/main.go"})
	out2 := h.Handle(context.Background(), FileRequest{PID: 4242, ChannelID: "c1", Op: OpRead, Path: "/work/main.go"})

	// Cache hit still returns the right decision, just silently.
	s.Require().Equal(types.DecisionAllow, out2.Decision)
	s.Require().True(out2.FromCache)
	s.Require().Equal("cache-hit", out2.Reason)

	entries := rec.snapshot()
	s.Require().Len(entries, 1, "only the first-miss gets audited; cache hits are silent")
	s.Require().Equal("file", entries[0].Kind)
	s.Require().Equal("read /work/main.go", entries[0].Target)
	s.Require().Equal("file[0]", entries[0].RuleID)
	s.Require().Equal("allow", entries[0].Decision)
	s.Require().Equal("c1", entries[0].Channel)
	s.Require().Equal(4242, entries[0].PID, "requesting PID must be plumbed into audit")
	s.Require().Greater(entries[0].Latency, time.Duration(0))
}

// --- IsRemoveDir ---

func (s *FileSuite) TestIsRemoveDir() {
	s.Require().True(IsRemoveDir(atRemoveDir))
	s.Require().True(IsRemoveDir(atRemoveDir | 0x1000))
	s.Require().False(IsRemoveDir(0))
	s.Require().False(IsRemoveDir(0x1000))
}
