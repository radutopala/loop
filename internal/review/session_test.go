package review

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

func (s *SessionSuite) TestUpdateStatus() {
	store := NewStore()
	store.Put("ch1", &Session{Status: StatusIdle})
	require.True(s.T(), store.UpdateStatus("ch1", StatusError, "boom"))
	got := store.Get("ch1")
	require.Equal(s.T(), StatusError, got.Status)
	require.Equal(s.T(), "boom", got.Error)
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
