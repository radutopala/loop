package review

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/githubapi"
)

type SessionSuite struct {
	suite.Suite
}

func TestSessionSuite(t *testing.T) {
	suite.Run(t, new(SessionSuite))
}

func (s *SessionSuite) TestGetMissingReturnsNil() {
	store := NewStore()
	require.Nil(s.T(), store.Get("nope"))
}

func (s *SessionSuite) TestPutThenGet() {
	store := NewStore()
	store.Put("ch1", &Session{Status: StatusReady, RawDiff: "diff"})
	got := store.Get("ch1")
	require.NotNil(s.T(), got)
	require.Equal(s.T(), "ch1", got.ChannelID)
	require.Equal(s.T(), StatusReady, got.Status)
	require.Equal(s.T(), "diff", got.RawDiff)
	require.False(s.T(), got.UpdatedAt.IsZero())
}

func (s *SessionSuite) TestGetReturnsCopyWithIndependentCommentsSlice() {
	store := NewStore()
	store.Put("ch1", &Session{})
	store.AddComment("ch1", &Comment{ID: "a", Path: "x", Line: 1, Body: "b"})
	got := store.Get("ch1")
	// Mutating the returned slice header should not affect the store.
	got.Comments = append(got.Comments, &Comment{ID: "z"})
	require.Len(s.T(), store.Get("ch1").Comments, 1)
}

// Regression: Go marshals a nil slice as JSON null, and the renderer
// reads `session.comments.length` directly — so Get must return a
// non-nil empty slice and the JSON must serialize as `[]`.
func (s *SessionSuite) TestGetEmptyCommentsMarshalsAsArrayNotNull() {
	store := NewStore()
	store.Put("ch1", &Session{Status: StatusReady})
	got := store.Get("ch1")
	require.NotNil(s.T(), got)
	require.NotNil(s.T(), got.Comments)
	require.Len(s.T(), got.Comments, 0)

	b, err := json.Marshal(got)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(b), `"comments":[]`)
	require.NotContains(s.T(), string(b), `"comments":null`)
}

func (s *SessionSuite) TestListEmpty() {
	store := NewStore()
	require.Empty(s.T(), store.List())
}

func (s *SessionSuite) TestListReturnsSummaries() {
	store := NewStore()
	store.Put("ch1", &Session{Status: StatusReady})
	store.Put("ch2", &Session{Status: StatusReviewing})
	store.Put("ch3", &Session{Status: StatusError})

	got := store.List()
	require.Len(s.T(), got, 3)

	statuses := map[string]Status{}
	for _, sum := range got {
		statuses[sum.ChannelID] = sum.Status
		require.Equal(s.T(), store.Get(sum.ChannelID).UpdatedAt, sum.UpdatedAt)
	}
	require.Equal(s.T(), StatusReady, statuses["ch1"])
	require.Equal(s.T(), StatusReviewing, statuses["ch2"])
	require.Equal(s.T(), StatusError, statuses["ch3"])
}

func (s *SessionSuite) TestDelete() {
	store := NewStore()
	store.Put("ch1", &Session{})
	store.Delete("ch1")
	require.Nil(s.T(), store.Get("ch1"))
	// Delete on missing is a no-op.
	store.Delete("ch1")
}

func (s *SessionSuite) TestUpdateStatusMissingReturnsFalse() {
	store := NewStore()
	require.False(s.T(), store.UpdateStatus("nope", StatusError, "x"))
}

func (s *SessionSuite) TestUpdateRawDiffMissingReturnsFalse() {
	store := NewStore()
	require.False(s.T(), store.UpdateRawDiff("nope", "diff"))
}

func (s *SessionSuite) TestUpdateRawDiffSwapsAndStampsTime() {
	store := NewStore()
	store.Put("ch1", &Session{RawDiff: "old"})
	require.True(s.T(), store.UpdateRawDiff("ch1", "new"))
	got := store.Get("ch1")
	require.Equal(s.T(), "new", got.RawDiff)
	require.False(s.T(), got.UpdatedAt.IsZero())
}

func (s *SessionSuite) TestUpdateForkMissingReturnsFalse() {
	store := NewStore()
	require.False(s.T(), store.UpdateFork("nope", ForkCurrent, ""))
}

// The id only sticks for ForkCustom — switching away must not leave a stale
// one behind that a later mode switch would silently pick up.
func (s *SessionSuite) TestUpdateForkStoresModeAndClearsIDUnlessCustom() {
	tests := []struct {
		name       string
		mode       ForkMode
		sessionID  string
		wantSessID string
	}{
		{name: "custom keeps id", mode: ForkCustom, sessionID: "sess-1", wantSessID: "sess-1"},
		{name: "current clears id", mode: ForkCurrent, sessionID: "sess-1"},
		{name: "none clears id", mode: ForkNone, sessionID: "sess-1"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			store := NewStore()
			store.Put("ch1", &Session{ForkMode: ForkCustom, ForkSessionID: "stale"})
			require.True(s.T(), store.UpdateFork("ch1", tc.mode, tc.sessionID))
			got := store.Get("ch1")
			require.Equal(s.T(), tc.mode, got.ForkMode)
			require.Equal(s.T(), tc.wantSessID, got.ForkSessionID)
			require.False(s.T(), got.UpdatedAt.IsZero())
		})
	}
}

func (s *SessionSuite) TestUpdateAgent() {
	store := NewStore()
	require.False(s.T(), store.UpdateAgent("nope", "m", "high"))

	store.Put("ch1", &Session{Model: "old", Effort: "low"})
	require.True(s.T(), store.UpdateAgent("ch1", "claude-opus-5-5", "max"))
	got := store.Get("ch1")
	require.Equal(s.T(), "claude-opus-5-5", got.Model)
	require.Equal(s.T(), "max", got.Effort)
	require.False(s.T(), got.UpdatedAt.IsZero())

	require.True(s.T(), store.UpdateAgent("ch1", "", ""))
	got = store.Get("ch1")
	require.Empty(s.T(), got.Model)
	require.Empty(s.T(), got.Effort)
}

func (s *SessionSuite) TestUpdateScopeAndLastRun() {
	store := NewStore()
	require.False(s.T(), store.UpdateScope("nope", ScopeFull))
	require.False(s.T(), store.SetLastRunScope("nope", ScopeFull, ""))

	store.Put("ch1", &Session{})
	require.True(s.T(), store.UpdateScope("ch1", ScopeIncremental))
	require.True(s.T(), store.SetLastRunScope("ch1", ScopeIncremental, "abc"))
	got := store.Get("ch1")
	require.Equal(s.T(), ScopeIncremental, got.Scope)
	require.Equal(s.T(), ScopeIncremental, got.LastRunScope)
	require.Equal(s.T(), "abc", got.LastRunFromSHA)
}

func (s *SessionSuite) TestReviewRuns() {
	store := NewStore()
	require.False(s.T(), store.StartReview("nope", "r1", ""))
	require.False(s.T(), store.AddReviewRun("nope", 1))

	store.Put("ch1", &Session{})
	require.True(s.T(), store.StartReview("ch1", "r1", "abc"))
	require.True(s.T(), store.AddReviewRun("ch1", 4))
	before := store.Get("ch1")
	require.True(s.T(), store.AddReviewRun("ch1", 5))
	got := store.Get("ch1")
	require.Equal(s.T(), "r1", got.ReviewID)
	require.Equal(s.T(), "abc", got.ReviewHead)
	require.Equal(s.T(), []int64{4, 5}, got.ReviewRuns)
	require.Equal(s.T(), []int64{4}, before.ReviewRuns, "a copy taken before keeps its runs")

	require.True(s.T(), store.StartReview("ch1", "r2", ""))
	got = store.Get("ch1")
	require.Equal(s.T(), "r2", got.ReviewID)
	require.Empty(s.T(), got.ReviewRuns)
	require.Empty(s.T(), got.ReviewHead, "a new review drops the old one's pin")
}

func (s *SessionSuite) TestAddTranscriptNoSession() {
	require.Nil(s.T(), NewStore().AddTranscript("nope", "/t/a.jsonl", map[string]bool{"c1": true}))
}

// A transcript goes once onto each listed agent comment, after the ones
// already there; GitHub comments and unlisted ones are left alone, and the
// comment a caller already holds doesn't change.
func (s *SessionSuite) TestAddTranscript() {
	store := NewStore()
	held := &Comment{ID: "c1", Transcripts: []string{"/t/a.jsonl"}}
	store.Put("ch1", &Session{Comments: []*Comment{
		held,
		{ID: "c2"},
		{ID: "gh", Source: "github"},
		{ID: "c3"},
	}})
	ids := map[string]bool{"c1": true, "c2": true, "gh": true}

	got := store.AddTranscript("ch1", "/t/b.jsonl", ids)
	require.Equal(s.T(), []*Comment{
		{ID: "c1", Transcripts: []string{"/t/a.jsonl", "/t/b.jsonl"}},
		{ID: "c2", Transcripts: []string{"/t/b.jsonl"}},
	}, got)
	require.Equal(s.T(), []string{"/t/a.jsonl"}, held.Transcripts)
	require.Nil(s.T(), store.AddTranscript("ch1", "/t/b.jsonl", ids))

	comments := store.Get("ch1").Comments
	require.Equal(s.T(), []string{"/t/a.jsonl", "/t/b.jsonl"}, comments[0].Transcripts)
	require.Empty(s.T(), comments[2].Transcripts)
	require.Empty(s.T(), comments[3].Transcripts)
}

func (s *SessionSuite) TestUpdateStatus() {
	store := NewStore()
	store.Put("ch1", &Session{Status: StatusIdle})
	require.True(s.T(), store.UpdateStatus("ch1", StatusError, "boom"))
	got := store.Get("ch1")
	require.Equal(s.T(), StatusError, got.Status)
	require.Equal(s.T(), "boom", got.Error)
}

func (s *SessionSuite) TestSetSuperseded() {
	store := NewStore()
	require.False(s.T(), store.SetSuperseded("nope", map[string]string{"b": "a"}))
	store.Put("ch1", &Session{})
	require.True(s.T(), store.SetSuperseded("ch1", map[string]string{"b": "a"}))
	require.Equal(s.T(), map[string]string{"b": "a"}, store.Get("ch1").Superseded)
	require.True(s.T(), store.SetSuperseded("ch1", nil))
	require.Nil(s.T(), store.Get("ch1").Superseded)
}

func (s *SessionSuite) TestAddCommentMissingReturnsFalse() {
	store := NewStore()
	require.False(s.T(), store.AddComment("nope", &Comment{}))
}

func (s *SessionSuite) TestAddCommentAppends() {
	store := NewStore()
	store.Put("ch1", &Session{})
	require.True(s.T(), store.AddComment("ch1", &Comment{ID: "a"}))
	require.True(s.T(), store.AddComment("ch1", &Comment{ID: "b"}))
	got := store.Get("ch1")
	require.Len(s.T(), got.Comments, 2)
	require.Equal(s.T(), "a", got.Comments[0].ID)
	require.Equal(s.T(), "b", got.Comments[1].ID)
}

// An agent retry re-reports the identical body, which hashes to the id the
// session already holds.
func (s *SessionSuite) TestAddCommentDropsSameID() {
	store := NewStore()
	store.Put("ch1", &Session{})
	require.True(s.T(), store.AddComment("ch1", &Comment{ID: "a", Path: "a.go", Line: 1, Body: "nil map write"}))
	require.False(s.T(), store.AddComment("ch1", &Comment{ID: "a", Path: "a.go", Line: 1, Body: "nil map write"}))
	require.Len(s.T(), store.Get("ch1").Comments, 1)
}

// Content dedup is the dedup pass's job: a reworded finding on the same line
// is kept here.
func (s *SessionSuite) TestAddCommentKeepsRewordedFinding() {
	store := NewStore()
	store.Put("ch1", &Session{})
	require.True(s.T(), store.AddComment("ch1", &Comment{ID: "a", Path: "a.go", Line: 1, Body: "nil map write"}))
	require.True(s.T(), store.AddComment("ch1", &Comment{ID: "b", Path: "a.go", Line: 1, Body: "writes to a nil map"}))
	require.Len(s.T(), store.Get("ch1").Comments, 2)
}

// Nil entries are skipped.
func (s *SessionSuite) TestAddCommentToleratesNilComments() {
	store := NewStore()
	store.Put("ch1", &Session{Comments: []*Comment{nil}})
	require.True(s.T(), store.AddComment("ch1", &Comment{ID: "a", Path: "a.go", Line: 1, Body: "nil map write"}))
	require.Len(s.T(), store.Get("ch1").Comments, 2)
}

func (s *SessionSuite) TestMarkPushedMissingSession() {
	store := NewStore()
	require.False(s.T(), store.MarkPushed("nope", "x", 0))
}

func (s *SessionSuite) TestMarkPushedMissingComment() {
	store := NewStore()
	store.Put("ch1", &Session{})
	require.False(s.T(), store.MarkPushed("ch1", "missing", 0))
}

func (s *SessionSuite) TestMarkPushedFlips() {
	store := NewStore()
	store.Put("ch1", &Session{})
	store.AddComment("ch1", &Comment{ID: "a"})
	require.True(s.T(), store.MarkPushed("ch1", "a", 0))
	got := store.Get("ch1")
	require.True(s.T(), got.Comments[0].Pushed)
	require.False(s.T(), got.Comments[0].PushedAt.IsZero())
	require.Equal(s.T(), int64(0), got.Comments[0].GitHubID)
}

func (s *SessionSuite) TestMarkPushedStampsGitHubIDWhenPositive() {
	store := NewStore()
	store.Put("ch1", &Session{})
	store.AddComment("ch1", &Comment{ID: "a"})
	require.True(s.T(), store.MarkPushed("ch1", "a", 99))
	require.Equal(s.T(), int64(99), store.Get("ch1").Comments[0].GitHubID)
}

func (s *SessionSuite) TestMarkPushedZeroGitHubIDDoesNotClearExisting() {
	// Edge case: a pushed agent comment with a known id should not have
	// its GitHubID zeroed by a subsequent MarkPushed call that passes 0.
	store := NewStore()
	store.Put("ch1", &Session{})
	store.AddComment("ch1", &Comment{ID: "a", GitHubID: 7})
	require.True(s.T(), store.MarkPushed("ch1", "a", 0))
	require.Equal(s.T(), int64(7), store.Get("ch1").Comments[0].GitHubID)
}

func (s *SessionSuite) TestRemoveCommentMissingSession() {
	store := NewStore()
	c, ok := store.RemoveComment("nope", "x")
	require.Nil(s.T(), c)
	require.False(s.T(), ok)
}

func (s *SessionSuite) TestRemoveCommentMissingCommentSessionExists() {
	store := NewStore()
	store.Put("ch1", &Session{})
	c, ok := store.RemoveComment("ch1", "missing")
	require.Nil(s.T(), c)
	require.True(s.T(), ok)
}

func (s *SessionSuite) TestRemoveCommentDropsAndReturnsIt() {
	store := NewStore()
	store.Put("ch1", &Session{})
	store.AddComment("ch1", &Comment{ID: "a", Path: "p", GitHubID: 11})
	store.AddComment("ch1", &Comment{ID: "b", Path: "q"})
	c, ok := store.RemoveComment("ch1", "a")
	require.True(s.T(), ok)
	require.NotNil(s.T(), c)
	require.Equal(s.T(), int64(11), c.GitHubID)
	remaining := store.Get("ch1").Comments
	require.Len(s.T(), remaining, 1)
	require.Equal(s.T(), "b", remaining[0].ID)
}

func (s *SessionSuite) TestFindCommentMissingSession() {
	store := NewStore()
	c, sess := store.FindComment("nope", "x")
	require.Nil(s.T(), c)
	require.Nil(s.T(), sess)
}

func (s *SessionSuite) TestFindCommentMissingComment() {
	store := NewStore()
	store.Put("ch1", &Session{})
	c, sess := store.FindComment("ch1", "missing")
	require.Nil(s.T(), c)
	require.NotNil(s.T(), sess)
}

func (s *SessionSuite) TestFindCommentReturnsHit() {
	store := NewStore()
	store.Put("ch1", &Session{})
	store.AddComment("ch1", &Comment{ID: "a", Path: "x", Line: 1, Body: "b"})
	c, sess := store.FindComment("ch1", "a")
	require.NotNil(s.T(), c)
	require.NotNil(s.T(), sess)
	require.Equal(s.T(), "x", c.Path)
}

// Only a local agent comment is edited; the session gets an updated copy,
// so a pointer handed out earlier keeps the old fields.
func (s *SessionSuite) TestSetVerdict() {
	cases := []struct {
		name    string
		channel string
		id      string
		want    bool
	}{
		{name: "local agent comment", channel: "ch1", id: "local", want: true},
		{name: "pushed agent comment", channel: "ch1", id: "pushed", want: true},
		{name: "github comment", channel: "ch1", id: "gh"},
		{name: "unknown comment", channel: "ch1", id: "zz"},
		{name: "no session", channel: "nope", id: "local"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := NewStore()
			store.Put("ch1", &Session{Comments: []*Comment{
				{ID: "local", Body: "Leak.", Source: "agent"},
				{ID: "pushed", Body: "Leak.", Source: "agent", Pushed: true},
				{ID: "gh", Body: "Leak.", Source: "github"},
			}})
			before, _ := store.FindComment("ch1", tc.id)

			got := store.SetVerdict(tc.channel, DedupVerdict{ID: tc.id, Verdict: VerdictReal, Impact: ImpactNiceToHave, Reason: "Closed in defer."})
			if !tc.want {
				require.Nil(s.T(), got)
				for _, c := range store.Get("ch1").Comments {
					require.Empty(s.T(), c.Verdict)
				}
				return
			}
			after, _ := store.FindComment("ch1", tc.id)
			require.Equal(s.T(), got, after)
			require.Equal(s.T(), VerdictReal, after.Verdict)
			require.Equal(s.T(), ImpactNiceToHave, after.Impact)
			require.Equal(s.T(), "Closed in defer.", after.VerdictReason)
			require.Empty(s.T(), before.Verdict)
		})
	}
}

func (s *SessionSuite) TestEditLocalComment() {
	cases := []struct {
		name    string
		channel string
		id      string
		want    bool
	}{
		{name: "local agent comment", channel: "ch1", id: "local", want: true},
		{name: "pushed comment", channel: "ch1", id: "pushed"},
		{name: "github comment", channel: "ch1", id: "gh"},
		{name: "unknown comment", channel: "ch1", id: "zz"},
		{name: "no session", channel: "nope", id: "local"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := NewStore()
			store.Put("ch1", &Session{Comments: []*Comment{
				{ID: "local", Line: 3, Body: "Leak.", Source: "agent"},
				{ID: "pushed", Line: 3, Body: "Leak.", Source: "agent", Pushed: true},
				{ID: "gh", Line: 3, Body: "Leak.", Source: "github"},
			}})
			before, _ := store.FindComment("ch1", tc.id)

			got := store.EditLocalComment(tc.channel, tc.id, func(c *Comment) {
				c.Line = 5
				c.Body = WithDedupNote(c.Body, "on retry too.")
			})
			if !tc.want {
				require.Nil(s.T(), got)
				for _, c := range store.Get("ch1").Comments {
					require.Equal(s.T(), 3, c.Line)
				}
				return
			}
			after, _ := store.FindComment("ch1", tc.id)
			require.Equal(s.T(), got, after)
			require.Equal(s.T(), 5, after.Line)
			require.Equal(s.T(), "Leak.\n\nAlso flagged: on retry too.", after.Body)
			require.Equal(s.T(), 3, before.Line)
		})
	}
}

// A verdict judged the comment's body: a new body drops it, a line move
// keeps it.
func (s *SessionSuite) TestEditLocalCommentVerdict() {
	cases := []struct {
		name        string
		edit        func(*Comment)
		wantVerdict string
		wantReason  string
		wantImpact  string
	}{
		{name: "body changed", edit: func(c *Comment) { c.Body = "Leak on retry." }},
		{name: "line moved", edit: func(c *Comment) { c.Line = 5 }, wantVerdict: VerdictReal, wantReason: "x.go:3 leaks", wantImpact: ImpactBlocking},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := NewStore()
			store.Put("ch1", &Session{Comments: []*Comment{
				{ID: "local", Line: 3, Body: "Leak.", Source: "agent", Verdict: VerdictReal, VerdictReason: "x.go:3 leaks", Impact: ImpactBlocking},
			}})
			got := store.EditLocalComment("ch1", "local", tc.edit)
			require.Equal(s.T(), tc.wantVerdict, got.Verdict)
			require.Equal(s.T(), tc.wantReason, got.VerdictReason)
			require.Equal(s.T(), tc.wantImpact, got.Impact)
		})
	}
}

type mockSaver struct{ mock.Mock }

func (m *mockSaver) SaveSession(saved *Saved)       { m.Called(saved) }
func (m *mockSaver) DeleteSession(channelID string) { m.Called(channelID) }

// The saver gets a session once it is on a PR, and again only when its
// choices change; the runs and the status aren't kept.
func (s *SessionSuite) TestSaver() {
	store := NewStore()
	store.Put("ch1", &Session{PR: &githubapi.PRInfo{Number: 6}})
	store.Delete("ch1")

	sv := new(mockSaver)
	store.SetSaver(sv)
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCurrent}).Return().Once()
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCurrent, Model: "opus", Effort: "high"}).Return().Once()
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high"}).Return().Once()
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: ScopeFull}).Return().Once()
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: ScopeFull, ReviewedSHA: "abc"}).Return().Once()
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: ScopeFull, ReviewedSHA: "abc", ApprovedSHA: "abc"}).Return().Once()
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: ScopeFull, ReviewedSHA: "abc", ApprovedSHA: "abc", Threads: `{"9":{"status":"addressed"}}`}).Return().Once()
	sv.On("SaveSession", &Saved{ChannelID: "ch1", PRNumber: 7, ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: ScopeFull, ReviewedSHA: "abc", ApprovedSHA: "abc"}).Return().Once()
	sv.On("DeleteSession", "ch1").Return().Once()

	store.Put("ch1", &Session{Status: StatusLoading})
	store.Put("ch1", &Session{PR: &githubapi.PRInfo{Number: 7}, ForkMode: ForkCurrent, HeadSHA: "abc", Comments: []*Comment{{ID: "gh-9", Source: "github", GitHubID: 9}}})
	store.UpdateStatus("ch1", StatusReviewing, "")
	store.UpdateAgent("ch1", "opus", "high")
	store.UpdateAgent("ch1", "opus", "high")
	store.UpdateFork("ch1", ForkCustom, "s0")
	store.UpdateScope("ch1", ScopeFull)
	store.SetLastRunScope("ch1", ScopeFull, "")
	store.MarkReviewed("ch1")
	store.MarkApproved("ch1", "abc")
	store.AssessThread("ch1", "gh-9", ThreadAssessment{Status: ThreadAddressed})
	store.StartVerify("ch1")
	store.Delete("ch1")
	sv.AssertExpectations(s.T())
}

func (s *SessionSuite) TestSavedApply() {
	sess := &Session{Model: "sonnet", HeadSHA: "abc"}
	(&Saved{ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: ScopeIncremental, ReviewedSHA: "abc", ApprovedSHA: "abd", Threads: `{"9":{"status":"open","impact":"blocking"}}`}).Apply(sess)
	require.Equal(s.T(), &Session{
		ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "high", Scope: ScopeIncremental, HeadSHA: "abc",
		ReviewedSHA: "abc", ApprovedSHA: "abd", Threads: map[int64]ThreadAssessment{9: {Status: ThreadOpen, Impact: ImpactBlocking}},
	}, sess)
}

// Kept is the last kept copy of a channel's session, on the PR asked for,
// whether or not a saver keeps it too.
func (s *SessionSuite) TestKept() {
	store := NewStore()
	require.Nil(s.T(), store.Kept("ch1", 7))
	store.Put("ch1", &Session{PR: &githubapi.PRInfo{Number: 7}, Model: "opus", Effort: "xhigh"})
	store.Put("ch1", &Session{Status: StatusLoading})
	want := &Saved{ChannelID: "ch1", PRNumber: 7, Model: "opus", Effort: "xhigh"}
	got := store.Kept("ch1", 7)
	require.Equal(s.T(), want, got)
	got.Effort = "low"
	require.Equal(s.T(), want, store.Kept("ch1", 7))
	require.Nil(s.T(), store.Kept("ch1", 8))
	store.Delete("ch1")
	require.Nil(s.T(), store.Kept("ch1", 7))
}

// Keep records the sessions a start restores as kept, so they aren't
// saved again unchanged; a channel kept since is left as it is.
func (s *SessionSuite) TestKeep() {
	store := NewStore()
	store.Put("ch2", &Session{PR: &githubapi.PRInfo{Number: 3}, Effort: "max"})
	sv := new(mockSaver)
	store.SetSaver(sv)
	store.Keep([]*Saved{{ChannelID: "ch1", PRNumber: 7, Effort: "high"}, {ChannelID: "ch2", PRNumber: 4}})
	require.Equal(s.T(), &Saved{ChannelID: "ch1", PRNumber: 7, Effort: "high"}, store.Kept("ch1", 7))
	require.Equal(s.T(), &Saved{ChannelID: "ch2", PRNumber: 3, Effort: "max"}, store.Kept("ch2", 3))
	store.Put("ch1", &Session{PR: &githubapi.PRInfo{Number: 7}, Effort: "high"})
	sv.AssertExpectations(s.T())
}

// Settle carries the choices made on the loading session over to the
// loaded one; with no loading session there, the loaded one's own stand.
func (s *SessionSuite) TestSettle() {
	cases := []struct {
		name string
		prev *Session
		want Session
	}{
		{"none", nil, Session{ForkMode: ForkCurrent, Model: "sonnet"}},
		{"ready", &Session{Status: StatusReady, Effort: "low"}, Session{ForkMode: ForkCurrent, Model: "sonnet"}},
		{"loading", &Session{Status: StatusLoading, ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "xhigh", Scope: ScopeFull}, Session{ForkMode: ForkCustom, ForkSessionID: "s0", Model: "opus", Effort: "xhigh", Scope: ScopeFull}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := NewStore()
			if tc.prev != nil {
				store.Put("ch1", tc.prev)
			}
			store.Settle("ch1", &Session{PR: &githubapi.PRInfo{Number: 7}, Status: StatusReady, ForkMode: ForkCurrent, Model: "sonnet"})
			got := store.Get("ch1")
			require.Equal(s.T(), StatusReady, got.Status)
			require.Equal(s.T(), "ch1", got.ChannelID)
			require.False(s.T(), got.UpdatedAt.IsZero())
			require.Equal(s.T(), tc.want.ForkMode, got.ForkMode)
			require.Equal(s.T(), tc.want.ForkSessionID, got.ForkSessionID)
			require.Equal(s.T(), tc.want.Model, got.Model)
			require.Equal(s.T(), tc.want.Effort, got.Effort)
			require.Equal(s.T(), tc.want.Scope, got.Scope)
			require.Equal(s.T(), tc.want.Effort, store.Kept("ch1", 7).Effort)
		})
	}
}
