package embeddings

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EmbeddingsSuite struct {
	suite.Suite
}

func TestEmbeddingsSuite(t *testing.T) {
	suite.Run(t, new(EmbeddingsSuite))
}

func (s *EmbeddingsSuite) TestCosineSimilarityIdentical() {
	a := []float32{1, 2, 3}
	require.InDelta(s.T(), 1.0, float64(CosineSimilarity(a, a)), 1e-6)
}

func (s *EmbeddingsSuite) TestCosineSimilarityOrthogonal() {
	a := []float32{1, 0}
	b := []float32{0, 1}
	require.InDelta(s.T(), 0.0, float64(CosineSimilarity(a, b)), 1e-6)
}

func (s *EmbeddingsSuite) TestCosineSimilarityOpposite() {
	a := []float32{1, 0}
	b := []float32{-1, 0}
	require.InDelta(s.T(), -1.0, float64(CosineSimilarity(a, b)), 1e-6)
}

func (s *EmbeddingsSuite) TestCosineSimilaritySimilar() {
	a := []float32{1, 2, 3}
	b := []float32{1, 2, 2.8}
	sim := CosineSimilarity(a, b)
	require.Greater(s.T(), sim, float32(0.99))
}

func (s *EmbeddingsSuite) TestCosineSimilarityDifferentLengths() {
	a := []float32{1, 2}
	b := []float32{1, 2, 3}
	require.Equal(s.T(), float32(0), CosineSimilarity(a, b))
}

func (s *EmbeddingsSuite) TestCosineSimilarityEmpty() {
	require.Equal(s.T(), float32(0), CosineSimilarity(nil, nil))
	require.Equal(s.T(), float32(0), CosineSimilarity([]float32{}, []float32{}))
}

func (s *EmbeddingsSuite) TestCosineSimilarityZeroVector() {
	a := []float32{0, 0, 0}
	b := []float32{1, 2, 3}
	require.Equal(s.T(), float32(0), CosineSimilarity(a, b))
}

func (s *EmbeddingsSuite) TestSerializeDeserializeRoundTrip() {
	original := []float32{0.1, -0.5, 3.14, 0, math.MaxFloat32}
	serialized := SerializeFloat32(original)
	require.Len(s.T(), serialized, len(original)*4)

	result := DeserializeFloat32(serialized)
	require.Equal(s.T(), original, result)
}

func (s *EmbeddingsSuite) TestSerializeEmpty() {
	require.Empty(s.T(), SerializeFloat32(nil))
	require.Empty(s.T(), SerializeFloat32([]float32{}))
}

func (s *EmbeddingsSuite) TestDeserializeInvalidLength() {
	require.Nil(s.T(), DeserializeFloat32([]byte{1, 2, 3}))
}

func (s *EmbeddingsSuite) TestDeserializeEmpty() {
	result := DeserializeFloat32(nil)
	require.Empty(s.T(), result)
}
