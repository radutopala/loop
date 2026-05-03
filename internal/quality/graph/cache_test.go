package graph

import (
	"sync"
	"testing"

	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CacheSuite struct {
	suite.Suite
}

func TestCacheSuite(t *testing.T) {
	suite.Run(t, new(CacheSuite))
}

func (s *CacheSuite) sampleGraph() *Graph {
	return Build([]*parser.FileFacts{{Path: "main.go"}})
}

func (s *CacheSuite) TestNewCacheStartsEmpty() {
	c := NewCache()
	g, dirty := c.Get("ch-1")
	require.Nil(s.T(), g)
	require.False(s.T(), dirty)
}

func (s *CacheSuite) TestSetThenGetReturnsGraphCleanState() {
	c := NewCache()
	g := s.sampleGraph()

	c.Set("ch-1", g)

	got, dirty := c.Get("ch-1")
	require.Same(s.T(), g, got)
	require.False(s.T(), dirty)
}

func (s *CacheSuite) TestSetIgnoresNilGraph() {
	c := NewCache()
	c.Set("ch-1", nil)

	got, dirty := c.Get("ch-1")
	require.Nil(s.T(), got)
	require.False(s.T(), dirty)
}

func (s *CacheSuite) TestInvalidateMarksDirtyKeepsGraph() {
	c := NewCache()
	g := s.sampleGraph()
	c.Set("ch-1", g)

	c.Invalidate("ch-1")

	got, dirty := c.Get("ch-1")
	require.Same(s.T(), g, got, "previous snapshot must remain available for the panel")
	require.True(s.T(), dirty)
}

func (s *CacheSuite) TestSetClearsDirtyFlag() {
	c := NewCache()
	c.Set("ch-1", s.sampleGraph())
	c.Invalidate("ch-1")

	fresh := s.sampleGraph()
	c.Set("ch-1", fresh)

	got, dirty := c.Get("ch-1")
	require.Same(s.T(), fresh, got)
	require.False(s.T(), dirty)
}

func (s *CacheSuite) TestInvalidateOnUnknownChannelIsNoOp() {
	c := NewCache()
	c.Invalidate("never-seen")

	got, dirty := c.Get("never-seen")
	require.Nil(s.T(), got)
	require.True(s.T(), dirty, "dirty flag is set even without a prior graph")
}

func (s *CacheSuite) TestDropRemovesGraphAndDirty() {
	c := NewCache()
	c.Set("ch-1", s.sampleGraph())
	c.Invalidate("ch-1")

	c.Drop("ch-1")

	got, dirty := c.Get("ch-1")
	require.Nil(s.T(), got)
	require.False(s.T(), dirty)
}

func (s *CacheSuite) TestDropOnUnknownChannelIsNoOp() {
	c := NewCache()
	c.Drop("never-seen")

	got, dirty := c.Get("never-seen")
	require.Nil(s.T(), got)
	require.False(s.T(), dirty)
}

func (s *CacheSuite) TestConcurrentReadsAndWrites() {
	// Race detector is on under -race; this exercises the RWMutex without
	// asserting an outcome — the goal is to detect data races.
	c := NewCache()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			c.Set("ch-1", s.sampleGraph())
		}(i)
		go func() {
			defer wg.Done()
			c.Get("ch-1")
		}()
		go func() {
			defer wg.Done()
			c.Invalidate("ch-1")
		}()
	}
	wg.Wait()
}
