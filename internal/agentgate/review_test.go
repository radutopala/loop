package agentgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type RenameReviewSuite struct {
	suite.Suite
	fs       *fakeGuardFS
	auditor  *collectAuditor
	approver *stubApprover
	review   *RenameReview
	now      time.Time
	rule     MatchResult
}

func TestRenameReviewSuite(t *testing.T) {
	suite.Run(t, new(RenameReviewSuite))
}

func (s *RenameReviewSuite) SetupTest() {
	s.now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	s.fs = &fakeGuardFS{}
	s.auditor = &collectAuditor{}
	s.approver = &stubApprover{out: Outcome{Decision: types.DecisionAllow, Actor: "user-1"}}
	s.review = &RenameReview{
		FS:       s.fs,
		Approver: s.approver,
		Auditor:  s.auditor,
		Now:      func() time.Time { return s.now },
	}
	s.rule = MatchResult{Decision: types.DecisionApprove, Message: "project config", RuleID: "file[1]"}
}

const reviewSrc, reviewDst = "/work/.loop/config.json.tmp.43.ab", "/work/.loop/config.json"

func (s *RenameReviewSuite) rename(flags uint64) GuardRename {
	return GuardRename{
		TrapID: 7, PID: 42, ChannelID: "ch-1", Syscall: syscallRenameat2,
		Src: reviewSrc, Dst: reviewDst, Flags: flags,
		Tracee: &FakeTracee{UID: 501, GID: 20},
	}
}

func (s *RenameReviewSuite) TestAsksWithDiffAndInstalls() {
	s.fs.snap = &GuardSnapshot{Src: reviewSrc, Dst: reviewDst, Old: []byte("{}\n"), OldExists: true, Data: []byte("{\"mounts\": []}\n")}
	s.review.PeerSource = func(int) string { return "terminal:leaf-1" }

	got, handled := s.review.Rename(context.Background(), s.rename(0), s.rule)
	s.Require().True(handled)
	s.Require().Equal(TrapResponse{ID: 7, Performed: true}, got)
	s.Require().Equal(reviewSrc, s.fs.gotSrc)
	s.Require().Equal(reviewDst, s.fs.gotDst)
	s.Require().Equal(501, s.fs.gotUID)
	s.Require().Equal(20, s.fs.gotGID)
	s.Require().Same(s.fs.snap, s.fs.installed)
	s.Require().False(s.fs.gotNoReplace)

	sum := sha256.Sum256(s.fs.snap.Data)
	req := s.approver.got
	s.Require().Equal("file", req.Kind)
	s.Require().Equal("write "+reviewDst, req.Target)
	s.Require().Equal("terminal:leaf-1", req.Source)
	s.Require().Equal("project config", req.Message)
	s.Require().Equal("file:review:"+reviewDst+":"+hex.EncodeToString(sum[:]), req.CacheKey)
	s.Require().Equal(reviewSrc, req.Details["source"])
	s.Require().Contains(req.Details["diff"], "-{}")
	s.Require().Contains(req.Details["diff"], "+{\"mounts\": []}")
	s.Require().Equal([]AuditEntry{
		{Ts: s.now, Channel: "ch-1", PID: 42, Kind: "file", Target: "write " + reviewDst, RuleID: "file[1]", Event: "request"},
		{Ts: s.now, Channel: "ch-1", PID: 42, Kind: "file", Target: "write " + reviewDst, RuleID: "file[1]", Decision: string(types.DecisionAllow), PromptedWho: "user-1"},
	}, s.auditor.entries)
}

func (s *RenameReviewSuite) TestNoReplacePassesThrough() {
	s.fs.snap = &GuardSnapshot{Data: []byte("x\n")}
	_, handled := s.review.Rename(context.Background(), s.rename(renameNoReplace), s.rule)
	s.Require().True(handled)
	s.Require().True(s.fs.gotNoReplace)
}

func (s *RenameReviewSuite) TestDenied() {
	s.approver.out = Outcome{Decision: types.DecisionDeny, Actor: "user-1"}
	s.fs.snap = &GuardSnapshot{Data: []byte("x\n")}
	got, handled := s.review.Rename(context.Background(), s.rename(0), s.rule)
	s.Require().True(handled)
	s.Require().Equal(TrapResponse{ID: 7, ErrorNum: int32(syscall.EPERM)}, got)
	s.Require().Zero(s.fs.installs)
	last := s.auditor.entries[len(s.auditor.entries)-1]
	s.Require().Equal(string(types.DecisionDeny), last.Decision)
}

func (s *RenameReviewSuite) TestFallsBackWhenContentCantBeShown() {
	cases := []struct {
		name   string
		review func() *RenameReview
		flags  uint64
		creds  error
		snap   *GuardSnapshot
		err    error
	}{
		{"nil review", func() *RenameReview { return nil }, 0, nil, nil, nil},
		{"no approver", func() *RenameReview { return &RenameReview{FS: s.fs} }, 0, nil, nil, nil},
		{"exchange", func() *RenameReview { return s.review }, renameExchange, nil, nil, nil},
		{"whiteout", func() *RenameReview { return s.review }, renameWhiteout, nil, nil, nil},
		{"creds fail", func() *RenameReview { return s.review }, 0, errors.New("gone"), nil, nil},
		{"not regular", func() *RenameReview { return s.review }, 0, nil, nil, ErrGuardNotRegular},
		{"too large", func() *RenameReview { return s.review }, 0, nil, nil, ErrGuardTooLarge},
		{"binary", func() *RenameReview { return s.review }, 0, nil, &GuardSnapshot{Data: []byte{0, 1, 2}}, nil},
		{"binary target", func() *RenameReview { return s.review }, 0, nil, &GuardSnapshot{Data: []byte("x\n"), Old: []byte{0}, OldExists: true}, nil},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.fs.snap, s.fs.snapErr = c.snap, c.err
			r := s.rename(c.flags)
			r.Tracee = &FakeTracee{CredsErr: c.creds}
			got, handled := c.review().Rename(context.Background(), r, s.rule)
			s.Require().False(handled)
			s.Require().Equal(TrapResponse{}, got)
			s.Require().Empty(s.approver.got.Kind)
			s.Require().Zero(s.fs.installs)
			s.Require().Empty(s.auditor.entries)
		})
	}
}

func (s *RenameReviewSuite) TestFilesystemErrorsMapToErrno() {
	cases := []struct {
		name       string
		snapErr    error
		installErr error
		want       syscall.Errno
	}{
		{"snapshot enoent", syscall.ENOENT, nil, syscall.ENOENT},
		{"snapshot other", errors.New("boom"), nil, syscall.EPERM},
		{"snapshot zero errno", syscall.Errno(0), nil, syscall.EPERM},
		{"install eexist", nil, syscall.EEXIST, syscall.EEXIST},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			s.SetupTest()
			s.fs.snap = &GuardSnapshot{Data: []byte("x\n")}
			s.fs.snapErr, s.fs.installErr = c.snapErr, c.installErr
			got, handled := s.review.Rename(context.Background(), s.rename(0), s.rule)
			s.Require().True(handled)
			s.Require().Equal(TrapResponse{ID: 7, ErrorNum: int32(c.want)}, got)
			last := s.auditor.entries[len(s.auditor.entries)-1]
			s.Require().Equal(string(types.DecisionDeny), last.Decision)
			s.Require().Equal("file[1]", last.RuleID)
			s.Require().NotEmpty(last.Extra["error"])
		})
	}
}

func (s *RenameReviewSuite) TestWithoutAuditorOrClock() {
	s.fs.snap = &GuardSnapshot{Data: []byte("x\n")}
	v := &RenameReview{FS: s.fs, Approver: s.approver}
	_, handled := v.Rename(context.Background(), s.rename(0), s.rule)
	s.Require().True(handled)
	s.Require().WithinDuration(time.Now(), v.now(), time.Minute)
}
