package apiauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Cap is a content capability: read access to one channel root or one
// playground, for a while. The browser loads some content by URL (iframes,
// images, a preview's <base href>) and can't add a header to those
// requests, so their URLs carry a Cap instead of the owner token.
type Cap struct {
	Kind      string `json:"k"`
	ChannelID string `json:"c,omitempty"`
	Scope     string `json:"s,omitempty"`
	Expires   int64  `json:"e"`
}

// ErrBadCap is returned for a capability that's malformed, forged or
// expired.
var ErrBadCap = errors.New("invalid or expired content capability")

// Signer mints and verifies capabilities with a key that lives only in
// memory: a daemon restart invalidates every capability, and the UI mints
// new ones.
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner returns a Signer with a random key.
func NewSigner() (*Signer, error) {
	return newSigner(rand.Read, time.Now)
}

func newSigner(readRand func([]byte) (int, error), now func() time.Time) (*Signer, error) {
	key := make([]byte, tokenBytes)
	if _, err := readRand(key); err != nil {
		return nil, fmt.Errorf("generating a signing key: %w", err)
	}
	return &Signer{key: key, now: now}, nil
}

// Mint returns c, valid for ttl, as a URL-safe string.
func (s *Signer) Mint(c Cap, ttl time.Duration) string {
	c.Expires = s.now().Add(ttl).Unix()
	payload, _ := json.Marshal(c) // a struct of strings and an int always marshals
	p := base64.RawURLEncoding.EncodeToString(payload)
	return p + "." + base64.RawURLEncoding.EncodeToString(s.mac(p))
}

// Verify returns the capability tok holds, or ErrBadCap.
func (s *Signer) Verify(tok string) (Cap, error) {
	p, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return Cap{}, ErrBadCap
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(p)) {
		return Cap{}, ErrBadCap
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return Cap{}, ErrBadCap
	}
	var c Cap
	if err := json.Unmarshal(payload, &c); err != nil {
		return Cap{}, ErrBadCap
	}
	if s.now().Unix() >= c.Expires {
		return Cap{}, ErrBadCap
	}
	return c, nil
}

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}
