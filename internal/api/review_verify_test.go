package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/review"
)

// verifyOn turns the verification step on in the stubbed config, with
// prompt as its instructions.
func (s *ReviewHandlerSuite) verifyOn(prompt string) {
	s.srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Review: config.ReviewConfig{Enabled: true, Verify: true, VerifyPrompt: prompt}}, nil
	}
}

func (s *ReviewHandlerSuite) postVerify() *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/verify", nil))
	return w
}

// postVerifyAndWait starts the verification step and waits for it to give
// the run slot back.
func (s *ReviewHandlerSuite) postVerifyAndWait() {
	w := s.postVerify()
	require.Equal(s.T(), http.StatusAccepted, w.Code)
	require.JSONEq(s.T(), `{"status":"started"}`, w.Body.String())
	s.waitFor(func() bool { return !s.srv.review.isReviewRunActive("ch1") })
}

// verifyRunner answers the review run (not read-only) with nothing, the
// dedup pass with dedup and the verification agent (read-only, allowed to
// record verdicts) with verify. It records the status the session showed
// at each call.
type verifyRunner struct {
	*mockReviewRunner
	statuses []review.Status
}

func newVerifyRunner(s *ReviewHandlerSuite, dedup, verify func(ctx context.Context) (*agent.AgentResponse, error)) *verifyRunner {
	vr := &verifyRunner{mockReviewRunner: &mockReviewRunner{}}
	var mu sync.Mutex
	vr.runWithCtxFn = func(ctx context.Context) (*agent.AgentResponse, error) {
		vr.mu.Lock()
		req := vr.reqs[len(vr.reqs)-1]
		vr.mu.Unlock()
		mu.Lock()
		if sess := s.rs.Get("ch1"); sess != nil {
			vr.statuses = append(vr.statuses, sess.Status)
		}
		mu.Unlock()
		switch {
		case !req.ReadOnly:
			return &agent.AgentResponse{}, nil
		case len(req.AllowTools) == 0:
			return dedup(ctx)
		default:
			return verify(ctx)
		}
	}
	return vr
}

func reply(text string) func(context.Context) (*agent.AgentResponse, error) {
	return func(context.Context) (*agent.AgentResponse, error) {
		return &agent.AgentResponse{Response: text, SessionID: "sess"}, nil
	}
}

func (s *ReviewHandlerSuite) TestVerifyNotConfigured() {
	srv := newServerForReviewTests(s.T())
	w := httptest.NewRecorder()
	srv.buildMux().ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/verify", nil))
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)

	// Sessions but no runner.
	require.Equal(s.T(), http.StatusNotImplemented, s.postVerify().Code)
}

func (s *ReviewHandlerSuite) TestVerifyRejects() {
	cases := []struct {
		name  string
		setup func()
		want  int
		body  string
	}{
		{name: "no session", setup: func() {}, want: http.StatusNotFound},
		{name: "no worktree", setup: func() {
			s.rs.Put("ch1", &review.Session{Status: review.StatusReady})
		}, want: http.StatusConflict, body: "no worktree"},
		{name: "run in flight", setup: func() {
			s.wireReadySession()
			require.True(s.T(), s.srv.review.registerReviewRun("ch1", func() {}))
		}, want: http.StatusConflict, body: "in progress"},
		{name: "not ready", setup: func() {
			s.wireReadySession()
			s.rs.UpdateStatus("ch1", review.StatusError, "boom")
		}, want: http.StatusConflict, body: "status=error"},
		{name: "review disabled", setup: func() {
			s.wireReadySession()
			s.srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
		}, want: http.StatusForbidden, body: "disabled"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			runner := &mockReviewRunner{}
			s.srv.review.setAgent(runner, "", "")
			s.verifyOn("")
			tc.setup()
			w := s.postVerify()
			require.Equal(s.T(), tc.want, w.Code)
			require.Contains(s.T(), w.Body.String(), tc.body)
			require.Equal(s.T(), 0, runner.calls)
			if tc.name != "run in flight" {
				require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
			}
		})
	}
}

// With the step turned off in the config, the endpoint does nothing and
// says so; the session stays ready.
func (s *ReviewHandlerSuite) TestVerifyDisabled() {
	s.wireDedupSession()
	runner := &mockReviewRunner{}
	s.srv.review.setAgent(runner, "", "")

	w := s.postVerify()

	require.Equal(s.T(), http.StatusAccepted, w.Code)
	require.JSONEq(s.T(), `{"status":"disabled"}`, w.Body.String())
	require.Equal(s.T(), 0, runner.calls)
	require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
	require.False(s.T(), s.srv.review.isReviewRunActive("ch1"))
}

// The step dedups the whole session, then has a read-only agent that may
// only record verdicts check the rest, with the review's context, and posts
// its reply to the channel with the PR outcome. The session shows verifying
// throughout, and ends reviewed at its head.
func (s *ReviewHandlerSuite) TestVerifyDedupsThenChecks() {
	s.wireDedupSession()
	s.verifyOn("Judge each comment.")
	msgs := new(MockMessageSender)
	msgs.On("PostMessage", mock.Anything, "ch1", "a: real\nd: false_positive\n\nOutcome: request changes: 1 blocking (a)").Return(nil).Once()
	s.srv.review.messages = msgs
	s.withHome()
	// The verification agent records the verdicts, as its MCP tool would.
	verify := func(ctx context.Context) (*agent.AgentResponse, error) {
		s.rs.SetVerdict("ch1", review.DedupVerdict{ID: "a", Verdict: review.VerdictReal, Impact: review.ImpactBlocking})
		s.rs.SetVerdict("ch1", review.DedupVerdict{ID: "d", Verdict: review.VerdictFalsePositive})
		return reply("  a: real\nd: false_positive\n")(ctx)
	}
	runner := newVerifyRunner(s, reply(`{"clusters":[{"keep":"a","drop":["b"]}]}`), verify)
	s.srv.review.setAgent(runner, "", "")

	s.postVerifyAndWait()

	require.Equal(s.T(), 2, runner.calls)
	dedupReq, verifyReq := runner.reqs[0], runner.reqs[1]
	require.True(s.T(), dedupReq.ReadOnly)
	// The whole session is regrouped: no comment is marked new.
	require.NotContains(s.T(), dedupReq.Prompt, ", new]")
	require.True(s.T(), verifyReq.ReadOnly)
	require.Equal(s.T(), review.VerifyTools, verifyReq.AllowTools)
	require.Equal(s.T(), "/repo/.worktrees/pr-7", verifyReq.DirPath)
	require.Equal(s.T(), "/repo", verifyReq.ParentDirPath)
	for _, want := range []string{
		"Judge each comment.\n\nReview context:\n",
		"- PR: #7\n",
		"- PR URL: https://github.com/o/r/pull/7\n",
		"- Reviewed commit: abc\n",
		"- Checkout: /repo/.worktrees/pr-7\n",
		`- Dedup pass: {"removed":["b"],"clusters":[{"kept":"a","removed":["b"]}]`,
	} {
		require.Contains(s.T(), verifyReq.Prompt, want)
	}
	require.Equal(s.T(), []review.Status{review.StatusVerifying, review.StatusVerifying}, runner.statuses)
	sess := s.rs.Get("ch1")
	require.Equal(s.T(), review.StatusReady, sess.Status)
	require.Equal(s.T(), map[string]string{"b": "a"}, sess.Superseded)
	require.Equal(s.T(), "abc", sess.ReviewedSHA)
	// Each pass's transcript goes onto the comments it changed.
	transcript := []string{runTranscript("sess")}
	keptA, _ := s.rs.FindComment("ch1", "a")
	require.Equal(s.T(), transcript, keptA.Transcripts)
	judged, _ := s.rs.FindComment("ch1", "d")
	require.Equal(s.T(), transcript, judged.Transcripts)
	msgs.AssertExpectations(s.T())
}

// A failed dedup pass is reported to the verification agent, which runs
// anyway. A pass that folds nothing leaves Superseded alone.
func (s *ReviewHandlerSuite) TestVerifyDedupOutcomes() {
	cases := []struct {
		name  string
		dedup func(context.Context) (*agent.AgentResponse, error)
		want  string
	}{
		{"pass fails", func(context.Context) (*agent.AgentResponse, error) { return nil, errors.New("container died") },
			"- Dedup pass: failed: dedup run: container died\n"},
		{"nothing folded", reply(`{"clusters":[]}`), `- Dedup pass: {"removed":[],`},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wireDedupSession()
			s.verifyOn("")
			s.rs.SetSuperseded("ch1", map[string]string{"old": "kept"})
			runner := newVerifyRunner(s, tc.dedup, reply(""))
			s.srv.review.setAgent(runner, "", "")

			s.postVerifyAndWait()

			require.Equal(s.T(), 2, runner.calls)
			require.Contains(s.T(), runner.reqs[1].Prompt, review.DefaultVerifyPrompt)
			require.Contains(s.T(), runner.reqs[1].Prompt, tc.want)
			sess := s.rs.Get("ch1")
			require.Equal(s.T(), review.StatusReady, sess.Status)
			require.Equal(s.T(), map[string]string{"old": "kept"}, sess.Superseded)
		})
	}
}

// An agent comment without a path can't be grouped, so the dedup pass is
// skipped; the comment is still checked.
func (s *ReviewHandlerSuite) TestVerifyUnanchoredComment() {
	s.wireReadySession()
	s.verifyOn("")
	require.True(s.T(), s.rs.AddComment("ch1", &review.Comment{ID: "a", Body: "General remark.", Source: "agent"}))
	runner := newVerifyRunner(s, reply(""), reply(""))
	s.srv.review.setAgent(runner, "", "")

	s.postVerifyAndWait()

	require.Equal(s.T(), 1, runner.calls)
	require.Contains(s.T(), runner.reqs[0].Prompt, "- Dedup pass: skipped: no agent comment is anchored to a file\n")
	require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
}

// A session holding only resolved GitHub threads has nothing to check, and
// one whose agent comments the dedup pass folded into those is left with
// none; either is reviewed as it is. An unresolved thread a person started
// is checked on its own.
func (s *ReviewHandlerSuite) TestVerifyNothingToCheck() {
	cases := []struct {
		name      string
		comments  []*review.Comment
		wantCalls int
	}{
		{"only resolved github threads", []*review.Comment{{ID: "g", Path: "x.go", Line: 1, Body: "Theirs.", Source: "github", GitHubID: 9, Resolved: true}}, 0},
		{"all folded by the dedup pass", []*review.Comment{
			{ID: "g", Path: "x.go", Line: 1, Body: "Theirs.", Source: "github", GitHubID: 9, Resolved: true},
			{ID: "a", Path: "x.go", Line: 1, Body: "Same.", Source: "agent"},
		}, 1},
		{"an unresolved github thread", []*review.Comment{{ID: "g", Path: "x.go", Line: 1, Body: "Theirs.", Source: "github", GitHubID: 9}}, 1},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wireReadySession()
			s.verifyOn("")
			for _, c := range tc.comments {
				require.True(s.T(), s.rs.AddComment("ch1", c))
			}
			runner := newVerifyRunner(s, reply(`{"clusters":[{"keep":"g","drop":["a"]}]}`), reply("unexpected"))
			s.srv.review.setAgent(runner, "", "")

			s.postVerifyAndWait()

			require.Equal(s.T(), tc.wantCalls, runner.calls)
			require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
			require.Equal(s.T(), s.rs.Get("ch1").HeadSHA, s.rs.Get("ch1").ReviewedSHA)
		})
	}
}

// A failed or timed-out verification agent leaves the session in error.
func (s *ReviewHandlerSuite) TestVerifyAgentFails() {
	cases := []struct {
		name    string
		timeout time.Duration
		verify  func(context.Context) (*agent.AgentResponse, error)
		want    string
	}{
		{"run fails", 0, func(context.Context) (*agent.AgentResponse, error) {
			return &agent.AgentResponse{SessionID: "sess"}, errors.New("container died")
		}, "verification run: container died"},
		{"times out", 10 * time.Millisecond, func(ctx context.Context) (*agent.AgentResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, "verification timed out after 10ms"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wireDedupSession()
			s.verifyOn("")
			s.srv.review.setVerifyTimeout(tc.timeout)
			runner := newVerifyRunner(s, reply(`{"clusters":[]}`), tc.verify)
			s.srv.review.setAgent(runner, "", "")

			s.postVerifyAndWait()

			sess := s.rs.Get("ch1")
			require.Equal(s.T(), review.StatusError, sess.Status)
			require.Equal(s.T(), tc.want, sess.Error)
		})
	}
}

// Stopping the step, during the dedup pass or the verification, leaves the
// session ready, like a stopped review.
func (s *ReviewHandlerSuite) TestVerifyCancelled() {
	stop := func(ctx context.Context) (*agent.AgentResponse, error) {
		s.srv.review.cancelReviewRun("ch1")
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cases := []struct {
		name          string
		dedup, verify func(context.Context) (*agent.AgentResponse, error)
		wantCalls     int
	}{
		{"during the dedup pass", stop, reply(""), 1},
		{"during the verification", reply(`{"clusters":[]}`), stop, 2},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wireDedupSession()
			s.verifyOn("")
			runner := newVerifyRunner(s, tc.dedup, tc.verify)
			s.srv.review.setAgent(runner, "", "")

			w := s.postVerify()
			require.Equal(s.T(), http.StatusAccepted, w.Code)
			s.waitFor(func() bool { return !s.srv.review.isReviewRunActive("ch1") })

			require.Equal(s.T(), tc.wantCalls, runner.calls)
			require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
		})
	}
}

// A summary that fails to post is only logged.
func (s *ReviewHandlerSuite) TestVerifySummaryPostFails() {
	s.wireDedupSession()
	s.verifyOn("")
	msgs := new(MockMessageSender)
	msgs.On("PostMessage", mock.Anything, "ch1", "summary\n\nOutcome: unknown: 4 comment(s) not judged yet").Return(errors.New("chat down")).Once()
	s.srv.review.messages = msgs
	runner := newVerifyRunner(s, reply(`{"clusters":[]}`), reply("summary"))
	s.srv.review.setAgent(runner, "", "")

	s.postVerifyAndWait()

	require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
	msgs.AssertExpectations(s.T())
}

// A review run ends with the verification step when the config turns it on,
// in place of the dedup pass over what the run added; {"verify":false}
// keeps that pass instead.
func (s *ReviewHandlerSuite) TestRunEndsWithVerification() {
	cases := []struct {
		name       string
		body       string
		wantVerify bool
	}{
		{"config on", ``, true},
		{"config on, body without verify", `{}`, true},
		{"skipped by the run", `{"verify":false}`, false},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.wireDedupSession()
			s.verifyOn("")
			runner := newVerifyRunner(s, reply(`{"clusters":[]}`), reply(""))
			runner.findings = []*review.Comment{review.NewComment("x.go", 12, "", "new")}
			s.srv.review.setAgent(runner, "", "")

			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", strings.NewReader(tc.body)))
			require.Equal(s.T(), http.StatusAccepted, w.Code)
			s.waitFor(func() bool { return !s.srv.review.isReviewRunActive("ch1") })

			verified := slices.ContainsFunc(runner.reqs, func(r review.RunRequest) bool { return len(r.AllowTools) > 0 })
			require.Equal(s.T(), tc.wantVerify, verified)
			// The review, then one dedup pass, then the verification when on.
			require.Equal(s.T(), map[bool]int{true: 3, false: 2}[tc.wantVerify], runner.calls)
			if tc.wantVerify {
				// The pass covers the whole session, not just what the run added.
				require.NotContains(s.T(), runner.reqs[1].Prompt, ", new]")
				require.Equal(s.T(), []review.Status{review.StatusReviewing, review.StatusVerifying, review.StatusVerifying}, runner.statuses)
			}
			require.Equal(s.T(), review.StatusReady, s.rs.Get("ch1").Status)
		})
	}
}

func (s *ReviewHandlerSuite) TestRunInvalidBody() {
	s.wireReadySession()
	runner := &mockReviewRunner{}
	s.srv.review.setAgent(runner, "", "")

	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/channels/ch1/review/run", strings.NewReader(`{"verify":`)))

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid JSON body")
	require.Equal(s.T(), 0, runner.calls)
}

// Sync and Push all wait for the verdicts.
func (s *ReviewHandlerSuite) TestBusyWhileVerifying() {
	for _, path := range []string{"/api/channels/ch1/review/sync", "/api/channels/ch1/review/push-all"} {
		s.Run(path, func() {
			s.SetupTest()
			s.wireDedupSession()
			s.rs.UpdateStatus("ch1", review.StatusVerifying, "")

			w := s.postJSON(path, nil)

			require.Equal(s.T(), http.StatusConflict, w.Code)
			require.Contains(s.T(), w.Body.String(), "status=verifying")
		})
	}
}

func (s *ReviewHandlerSuite) TestWithReviewVerifyTimeout() {
	srv := NewServer(nil, nil, nil, nil, nil, s.srv.logger, WithReviewVerifyTimeout(time.Minute))
	require.Equal(s.T(), time.Minute, srv.review.verifyTimeout)
}

func (s *ReviewHandlerSuite) TestConfigReviewMaxIterations() {
	cases := []struct {
		name string
		load func() (*config.Config, error)
		want int
	}{
		{"load error keeps the default", func() (*config.Config, error) { return nil, errors.New("bad") }, 3},
		{"unset", func() (*config.Config, error) { return &config.Config{}, nil }, 3},
		{"set", func() (*config.Config, error) {
			return &config.Config{Review: config.ReviewConfig{MaxIterations: 5}}, nil
		}, 5},
		{"out of range", func() (*config.Config, error) {
			return &config.Config{Review: config.ReviewConfig{MaxIterations: 11}}, nil
		}, 3},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			r := &configResolver{load: tc.load}
			require.Equal(s.T(), tc.want, r.reviewMaxIterations("", ""))
		})
	}
}

func (s *ReviewHandlerSuite) TestConfigReviewUpdateBase() {
	cases := []struct {
		name string
		load func() (*config.Config, error)
		want bool
	}{
		{"load error keeps it off", func() (*config.Config, error) { return nil, errors.New("bad") }, false},
		{"unset", func() (*config.Config, error) { return &config.Config{}, nil }, false},
		{"set", func() (*config.Config, error) {
			return &config.Config{Review: config.ReviewConfig{UpdateBase: true}}, nil
		}, true},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			r := &configResolver{load: tc.load}
			require.Equal(s.T(), tc.want, r.reviewUpdateBase("", ""))
		})
	}
}

// TestUpdateBase checks the refresh hands review.update_base to the base
// update, and that every outcome only logs: the run goes on regardless.
func (s *ReviewHandlerSuite) TestUpdateBase() {
	cases := []struct {
		name       string
		updateBase bool
		res        review.BaseUpdate
		err        error
		want       string
	}{
		{"up to date", false, review.BaseUpdate{}, nil, ""},
		{"fast-forwarded", true, review.BaseUpdate{Updated: true}, nil, "fast-forwarded the base branch"},
		{"left as it is", false, review.BaseUpdate{Skipped: "main is checked out"}, nil, `reason="main is checked out"`},
		{"failed", true, review.BaseUpdate{}, errors.New("boom"), "updating the base branch failed"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			var logs bytes.Buffer
			s.srv.review.deps.logger = slog.New(slog.NewTextHandler(&logs, nil))
			s.srv.configs.load = func() (*config.Config, error) {
				return &config.Config{Review: config.ReviewConfig{UpdateBase: tc.updateBase}}, nil
			}
			s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
			wt := new(mockPR)
			wt.On("UpdateBase", mock.Anything, "/repo", "main", tc.updateBase).Return(tc.res, tc.err).Once()
			s.srv.review.worktree = wt

			s.srv.review.updateBase(context.Background(), "ch1", "/repo", "main")

			wt.AssertExpectations(s.T())
			if tc.want == "" {
				require.Empty(s.T(), logs.String())
			} else {
				require.Contains(s.T(), logs.String(), tc.want)
			}
		})
	}
}

func (s *ReviewHandlerSuite) TestConfigReviewVerify() {
	cases := []struct {
		name string
		load func() (*config.Config, error)
		want verifyStep
	}{
		{"load error keeps the default", func() (*config.Config, error) { return nil, errors.New("bad") }, verifyStep{on: true}},
		{"off", func() (*config.Config, error) { return &config.Config{}, nil }, verifyStep{}},
		{"on with a prompt", func() (*config.Config, error) {
			return &config.Config{Review: config.ReviewConfig{Verify: true, VerifyPrompt: "p"}}, nil
		}, verifyStep{on: true, prompt: "p"}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			r := &configResolver{load: tc.load}
			require.Equal(s.T(), tc.want, r.reviewVerify("", ""))
		})
	}
}
