package apiauth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CapsSuite struct {
	suite.Suite
	now    time.Time
	signer *Signer
}

func TestCapsSuite(t *testing.T) {
	suite.Run(t, new(CapsSuite))
}

func (s *CapsSuite) SetupTest() {
	s.now = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	var err error
	s.signer, err = newSigner(func(b []byte) (int, error) { return len(b), nil }, func() time.Time { return s.now })
	require.NoError(s.T(), err)
}

func (s *CapsSuite) TestMintVerify() {
	tok := s.signer.Mint(Cap{Kind: "raw", ChannelID: "ch1", Scope: "0"}, time.Hour)
	c, err := s.signer.Verify(tok)
	require.NoError(s.T(), err)
	require.Equal(s.T(), Cap{Kind: "raw", ChannelID: "ch1", Scope: "0", Expires: s.now.Add(time.Hour).Unix()}, c)

	s.now = s.now.Add(time.Hour)
	_, err = s.signer.Verify(tok)
	require.ErrorIs(s.T(), err, ErrBadCap, "expired")
}

func (s *CapsSuite) TestVerifyRejects() {
	good := s.signer.Mint(Cap{Kind: "raw", ChannelID: "ch1"}, time.Hour)
	payload, sig, _ := strings.Cut(good, ".")
	other, err := newSigner(func(b []byte) (int, error) { b[0] = 1; return len(b), nil }, func() time.Time { return s.now })
	require.NoError(s.T(), err)
	forgedPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"k":"raw","c":"ch2","e":9999999999}`))
	notJSON := base64.RawURLEncoding.EncodeToString([]byte("nope"))

	tests := map[string]string{
		"no dot":          "abc",
		"bad sig base64":  payload + ".!!",
		"swapped payload": forgedPayload + "." + sig,
		"other key":       other.Mint(Cap{Kind: "raw"}, time.Hour),
		"bad payload":     "!!." + base64.RawURLEncoding.EncodeToString(s.signer.mac("!!")),
		"not json":        notJSON + "." + base64.RawURLEncoding.EncodeToString(s.signer.mac(notJSON)),
	}
	for name, tok := range tests {
		s.Run(name, func() {
			_, err := s.signer.Verify(tok)
			require.ErrorIs(s.T(), err, ErrBadCap)
		})
	}
}

func (s *CapsSuite) TestNewSigner() {
	sg, err := NewSigner()
	require.NoError(s.T(), err)
	_, err = sg.Verify(sg.Mint(Cap{Kind: "raw"}, time.Minute))
	require.NoError(s.T(), err)

	_, err = newSigner(func([]byte) (int, error) { return 0, errors.New("boom") }, time.Now)
	require.ErrorContains(s.T(), err, "boom")
}
